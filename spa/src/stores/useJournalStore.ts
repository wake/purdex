// spa/src/stores/useJournalStore.ts — the workbook journal, per host and unit (J-2a). Never persisted, never synced: every
// window has its own sockets and reloads what it shows.
//
// Support has two layers. `support[host]` is the live answer of the current connection: absent = unknown (the host is
// disconnected or its probe has not answered). `sticky[host]` is the LAST yes/no answer ever received and survives a
// disconnect (`fence` only clears `support`), so a journal host never flashes back to the workbook view while it
// reconnects; a host that never answered has no sticky answer and uses the old UI. Only `forgetHost` (re-point / removal)
// clears it: what the old daemon said is not the new one's.
//
// Data. A unit lives under `byHost[host].units[unitKey]` (`unitKey` = `<kind>:<id>` from the daemon's answer);
// `unitOfSession` remembers which unit a session's answer named. Entries are merged by id and ALWAYS re-sorted by (at, id);
// the pinned band is the server's list, newest first. EVENTS NEVER FETCH: `markStale` only raises a flag, the loader decides
// whether to read (journal-loader.ts). A flag is cleared by a read that STARTED after the event (`staleAt` > `readAt`
// compare store-clock stamps), so an event landing during a read keeps the unit stale.
// A read that lands after its host was forgotten (`epoch`) or its connection ended (`gens`) is dropped.
import { create } from 'zustand'
import { getJournal, getRoadmap, getTasks, setPinned, type Outcome } from '../lib/journal/api'
import { useI18nStore } from './useI18nStore'
import { useUndoToast } from './useUndoToast'
import { cursorOf, mergeEntries, sortPinned } from '../lib/journal/merge'
import { forgetViewMemosOfHost } from '../lib/journal/view-memory'
import { unitKeyOf, type ChangedWhat, type JournalEntry, type JournalProject, type JournalTask, type JournalUnit, type RemoteLead, type RoadmapFeature } from '../lib/journal/types'

/** Units kept per host: the least recently touched past this are dropped (events can name any unit, the store must not grow with them). */
export const MAX_UNITS = 30
export const MAX_ROUTES = 200

type Stamps = Record<ChangedWhat, number>
const zero = (): Stamps => ({ entries: 0, tasks: 0, roadmap: 0 })

export interface UnitState {
  unit: JournalUnit
  project: JournalProject
  personName: string
  /** Oldest first, unique by id. */
  entries: JournalEntry[]
  /** Newest first. */
  pinned: JournalEntry[]
  /** The oldest loaded entry is not the unit's first: an older month can be loaded. */
  more: boolean
  openConfirms: number
  /** null = never loaded (loaded when the tasks / roadmap tab opens, then kept fresh). */
  tasks: JournalTask[] | null
  roadmap: RoadmapFeature[] | null
  /** Store-clock stamp of the last `journal.changed` per resource, and of the start of the last read that landed. */
  staleAt: Stamps
  readAt: Stamps
  touched: number
}

interface HostJournal {
  units: Record<string, UnitState>
  /** session id → the unit key its answer named. */
  unitOfSession: Record<string, string>
}

/** Where a session's journal is read from. `follow`: a member whose lead lives on another daemon (409): `leadHostId` is the
 *  daemon's id, `hostId` the configured host it maps to. `unreachable`: no configured host has that daemon id (yet). */
export type JournalRoute =
  | { kind: 'direct' }
  | { kind: 'none' }
  | { kind: 'follow'; hostId: string; sessionId: string; leadHostId: string }
  | { kind: 'unreachable'; leadHostId: string; leadSessionId: string }

export type LoadResult = { kind: 'ok' } | { kind: 'none' } | ({ kind: 'remote' } & RemoteLead) | { kind: 'failed' } | { kind: 'dropped' } | { kind: 'no_unit' }

interface JournalState {
  /** Live support of the current connection; absent = unknown. */
  support: Record<string, boolean>
  /** The last yes/no answer, kept through unknown; absent = never answered. */
  sticky: Record<string, boolean>
  /** Connection generation per host: bumped by `fence`. */
  gens: Record<string, number>
  byHost: Record<string, HostJournal>
  /** `<host>\0<session>` of the tab's own session → where to read. */
  routes: Record<string, JournalRoute>
  /** Bumped by `forgetHost`: tells a read still out that its host was forgotten. */
  epoch: Record<string, number>
  /** Bumped by every `markStale` (the loader's subscription trigger). */
  staleRev: number

  /** The connection is gone or a new probe starts: support is unknown, the sticky answer stays. */
  fence: (hostId: string) => void
  /** The probe's answer for the current generation. */
  setSupport: (hostId: string, yes: boolean) => void
  /** The latest page (no `older`) or the next older page of the unit this session names. */
  loadJournal: (hostId: string, sessionId: string, opts?: { older?: boolean }) => Promise<LoadResult>
  loadTasks: (hostId: string, sessionId: string) => Promise<LoadResult>
  loadRoadmap: (hostId: string, sessionId: string) => Promise<LoadResult>
  /** Pin / unpin an entry of the unit this session names: the screen follows at once, the PUT follows; see `pins` below for how a failure or a later read settles it. */
  setPin: (hostId: string, sessionId: string, entryId: string, pinned: boolean) => Promise<void>
  /** `journal.changed`: raise the flag of the named resource of that unit (`roadmap`: every unit of the host). A unit not held is ignored. */
  markStale: (hostId: string, unitKey: string, what: ChangedWhat) => void
  setRoute: (hostId: string, sessionId: string, route: JournalRoute) => void
  forgetHost: (hostId: string) => void
  reset: () => void
}

const drop = <T,>(m: Record<string, T>, hostId: string): Record<string, T> => { const { [hostId]: _g, ...rest } = m; return rest }
export const sessionKey = (hostId: string, sessionId: string): string => `${hostId}\u0000${sessionId}`

let clock = 0
const nextStamp = (): number => ++clock

/**
 * Pending optimistic pins, per (host, unit, entry). `gen` is the entry's newest mutation; `base` is the value the server is known
 * to hold (the entry's value before the first unconfirmed click, or that of the newest confirmed mutation, `baseGen`). A failed
 * PUT reverts to `base` only when its generation is still the newest (a later click owns the display), and only while the record
 * exists: a server read of the entry deletes it, because what the server says wins from then on. `at` is the store-clock stamp of the
 * newest click and `value` what that click shows: only a read that STARTED after `at` speaks for the entry; one sent before the
 * click and landing after it carries the old state, so the click keeps the display.
 */
interface PinRec { gen: number; base: boolean; baseGen: number; at: number; value: boolean }
const pins = new Map<string, PinRec>()
let pinGen = 0
/** PUTs out per entry (`overlapped`: more than one was out at the same time, so the server may hold them in a different order). */
const flights = new Map<string, { n: number; overlapped: boolean }>()
const pinKey = (hostId: string, unitKey: string, entryId: string): string => `${hostId}\u0000${unitKey}\u0000${entryId}`
/** A read that started at `startedAt` ends the pending pins it speaks for (those clicked before it started, and named by `only` when given). */
const dropPins = (hostId: string, unitKey: string, startedAt: number, only?: ReadonlySet<string>): void => {
  const prefix = `${hostId}\u0000${unitKey}\u0000`
  for (const [k, rec] of [...pins]) if (k.startsWith(prefix) && rec.at < startedAt && (!only || only.has(k.slice(prefix.length)))) pins.delete(k)
}
/** Pins still pending after a read: the click keeps the display over what the (older) read carried. */
function keepPins(hostId: string, unitKey: string, u: UnitState): UnitState {
  const prefix = `${hostId}\u0000${unitKey}\u0000`
  let out = u
  for (const [k, rec] of pins) {
    if (!k.startsWith(prefix)) continue
    const entry = out.entries.find((e) => e.id === k.slice(prefix.length))
    if (entry) out = withPin(out, entry, rec.value)
  }
  return out
}
/** `entry` with its pin set to `value`, in the stream and in the band (newest first). */
function withPin(u: UnitState, entry: JournalEntry, value: boolean): UnitState {
  const next = { ...entry, pinned: value }
  return {
    ...u,
    entries: u.entries.map((e) => (e.id === entry.id ? next : e)),
    pinned: value ? sortPinned([...u.pinned, next]) : u.pinned.filter((e) => e.id !== entry.id),
  }
}

export const useJournalStore = create<JournalState>()((set, get) => {
  /** The unit a session names on a host, if its answer was loaded. */
  const unitOf = (hostId: string, sessionId: string): UnitState | undefined => {
    const h = get().byHost[hostId]
    const key = h?.unitOfSession[sessionId]
    return key === undefined ? undefined : h?.units[key]
  }

  /** Newest request token per (host, session, slot): a response that is no longer the newest of its slot is dropped. */
  const latest = new Map<string, number>()

  /** Run one read; `apply` runs only when the host was neither forgotten nor reconnected meanwhile and no newer read of the same slot started. */
  async function read<T>(hostId: string, slot: string, call: () => Promise<Outcome<T>>, apply: (value: T, startedAt: number) => void): Promise<LoadResult> {
    const epoch = get().epoch[hostId] ?? 0
    const gen = get().gens[hostId] ?? 0
    const startedAt = nextStamp()
    const slotKey = `${hostId}\u0000${slot}`
    latest.set(slotKey, startedAt)
    let out: Outcome<T>
    const current = (): boolean => (get().epoch[hostId] ?? 0) === epoch && (get().gens[hostId] ?? 0) === gen && latest.get(slotKey) === startedAt
    try {
      out = await call()
    } catch {
      return current() ? { kind: 'failed' } : { kind: 'dropped' }
    }
    if (!current()) return { kind: 'dropped' }
    if (out.kind === 'none') return { kind: 'none' }
    if (out.kind === 'remote') return { kind: 'remote', leadHostId: out.leadHostId, leadSessionId: out.leadSessionId }
    apply(out.value, startedAt)
    return { kind: 'ok' }
  }

  /** Slot of a read: sessions that map to one unit share its slots (a known session is keyed by its unit, an unknown one by itself). */
  const slotOf = (hostId: string, sessionId: string, what: string): string => `${get().byHost[hostId]?.unitOfSession[sessionId] ?? `session:${sessionId}`}\u0000${what}`

  const trim = (units: Record<string, UnitState>, unitOfSession: Record<string, string>): HostJournal => {
    const keys = Object.keys(units)
    if (keys.length <= MAX_UNITS) return { units, unitOfSession }
    const keep = new Set(keys.sort((a, b) => units[b].touched - units[a].touched).slice(0, MAX_UNITS))
    return {
      units: Object.fromEntries(Object.entries(units).filter(([k]) => keep.has(k))),
      unitOfSession: Object.fromEntries(Object.entries(unitOfSession).filter(([, k]) => keep.has(k))),
    }
  }

  return {
    support: {}, sticky: {}, gens: {}, byHost: {}, routes: {}, epoch: {}, staleRev: 0,

    fence: (hostId) => set((s) => ({ support: drop(s.support, hostId), gens: { ...s.gens, [hostId]: (s.gens[hostId] ?? 0) + 1 } })),
    setSupport: (hostId, yes) => set((s) => ({ support: { ...s.support, [hostId]: yes }, sticky: { ...s.sticky, [hostId]: yes } })),

    loadJournal: async (hostId, sessionId, opts) => {
      const older = opts?.older === true
      let before: string | undefined
      if (older) {
        const u = unitOf(hostId, sessionId)
        const oldest = u?.entries[0]
        if (!u || !oldest) return { kind: 'no_unit' }
        if (!u.more) return { kind: 'ok' }
        before = cursorOf(oldest)
      }
      return read(hostId, slotOf(hostId, sessionId, older ? 'older' : 'newest'), () => getJournal(hostId, sessionId, before), (page, startedAt) => set((s) => {
        const key = unitKeyOf(page.unit)
        const h = s.byHost[hostId] ?? { units: {}, unitOfSession: {} }
        const held = h.units[key]
        // What the server says about an entry wins over a pending optimistic pin of it (the newest page speaks for the whole band).
        dropPins(hostId, key, startedAt, older ? new Set(page.entries.map((e) => e.id)) : undefined)
        let entries = mergeEntries(held?.entries ?? [], page.entries)
        if (!older) {
          // Held rows the page does not carry (older months) follow the band the server just sent.
          const band = new Set(page.pinned.map((e) => e.id))
          const inPage = new Set(page.entries.map((e) => e.id))
          entries = entries.map((e) => (inPage.has(e.id) || e.pinned === band.has(e.id) ? e : { ...e, pinned: band.has(e.id) }))
        }
        const readAt = { ...(held?.readAt ?? zero()) }
        // Only a read of the newest page speaks for "entries are fresh"; an older page says nothing about new events.
        if (!older) readAt.entries = startedAt
        const next: UnitState = {
          unit: page.unit, project: page.project, personName: page.personName, entries,
          // The band and the open count are the unit's live state: only the newest page carries them.
          pinned: older ? (held?.pinned ?? []) : sortPinned(page.pinned),
          openConfirms: older ? (held?.openConfirms ?? 0) : page.openConfirms,
          // `more` describes the OLDEST loaded entry: an older page sets it; a newest-page re-read keeps it once older months are held.
          more: older || !held || held.entries.length === 0 ? page.more : held.more,
          tasks: held?.tasks ?? null, roadmap: held?.roadmap ?? null,
          staleAt: held?.staleAt ?? zero(), readAt, touched: nextStamp(),
        }
        const units = { ...h.units, [key]: keepPins(hostId, key, next) }
        return { byHost: { ...s.byHost, [hostId]: trim(units, { ...h.unitOfSession, [sessionId]: key }) } }
      }))
    },

    loadTasks: async (hostId, sessionId) => {
      if (!unitOf(hostId, sessionId)) return { kind: 'no_unit' }
      const startedKey = get().byHost[hostId]?.unitOfSession[sessionId] // the answer belongs to the unit asked for, even if the session is remapped meanwhile
      return read(hostId, slotOf(hostId, sessionId, 'tasks'), () => getTasks(hostId, sessionId), (tasks, startedAt) => set((s) => {
        const h = s.byHost[hostId]
        const key = h?.unitOfSession[sessionId]
        const held = key === undefined ? undefined : h?.units[key]
        if (!h || key === undefined || key !== startedKey || !held) return {}
        return { byHost: { ...s.byHost, [hostId]: { ...h, units: { ...h.units, [key]: { ...held, tasks, readAt: { ...held.readAt, tasks: startedAt }, touched: nextStamp() } } } } }
      }))
    },

    loadRoadmap: async (hostId, sessionId) => {
      if (!unitOf(hostId, sessionId)) return { kind: 'no_unit' }
      const startedKey = get().byHost[hostId]?.unitOfSession[sessionId] // the answer belongs to the unit asked for, even if the session is remapped meanwhile
      return read(hostId, slotOf(hostId, sessionId, 'roadmap'), () => getRoadmap(hostId, sessionId), (roadmap, startedAt) => set((s) => {
        const h = s.byHost[hostId]
        const key = h?.unitOfSession[sessionId]
        const held = key === undefined ? undefined : h?.units[key]
        if (!h || key === undefined || key !== startedKey || !held) return {}
        return { byHost: { ...s.byHost, [hostId]: { ...h, units: { ...h.units, [key]: { ...held, roadmap, readAt: { ...held.readAt, roadmap: startedAt }, touched: nextStamp() } } } } }
      }))
    },

    setPin: async (hostId, sessionId, entryId, pinned) => {
      const u = unitOf(hostId, sessionId)
      const entry = u?.entries.find((e) => e.id === entryId) ?? u?.pinned.find((e) => e.id === entryId)
      if (!u || !entry) return
      const unitKey = unitKeyOf(u.unit)
      const k = pinKey(hostId, unitKey, entryId)
      const shown = u.pinned.some((e) => e.id === entryId)
      const pending = pins.get(k)
      if (shown === pinned && !pending) return
      const gen = ++pinGen
      pins.set(k, { gen, base: pending?.base ?? shown, baseGen: pending?.baseGen ?? 0, at: nextStamp(), value: pinned })
      const apply = (value: boolean): void => set((s) => {
        const h = s.byHost[hostId]
        const cur = h?.units[unitKey]
        return h && cur ? { byHost: { ...s.byHost, [hostId]: { ...h, units: { ...h.units, [unitKey]: withPin(cur, entry, value) } } } } : {}
      })
      apply(pinned)
      const epoch = get().epoch[hostId] ?? 0
      const flight = flights.get(k) ?? { n: 0, overlapped: false }
      flight.n++
      if (flight.n > 1) flight.overlapped = true
      flights.set(k, flight)
      let ok = false
      try { ok = (await setPinned(hostId, sessionId, entryId, pinned)).kind === 'ok' } catch { /* ok stays false */ }
      // PUTs of one entry can reach the server in the opposite order of the clicks: when the last one ends, read the unit once more.
      const reread = --flight.n === 0 && flight.overlapped
      if (flight.n === 0 && flights.get(k) === flight) flights.delete(k)
      try {
        const rec = pins.get(k)
        if ((get().epoch[hostId] ?? 0) !== epoch || !rec) return // the host was forgotten, or a server read has the last word
        if (ok) {
          if (rec.gen === gen) pins.delete(k)
          else if (gen > rec.baseGen) pins.set(k, { ...rec, base: pinned, baseGen: gen })
          return
        }
        if (rec.gen !== gen) return // a later click owns the display now
        pins.delete(k)
        apply(rec.base)
        useUndoToast.getState().show(useI18nStore.getState().t('journal.pin.failed'))
      } finally {
        if (reread && (get().epoch[hostId] ?? 0) === epoch) await get().loadJournal(hostId, sessionId)
      }
    },

    markStale: (hostId, unitKey, what) => set((s) => {
      const h = s.byHost[hostId]
      if (!h) return {}
      const at = nextStamp()
      // The roadmap is the project's, not the unit's: every unit of the host (any of them may share the project) is stale.
      const keys = what === 'roadmap' ? Object.keys(h.units) : h.units[unitKey] ? [unitKey] : []
      if (keys.length === 0) return {}
      const units = { ...h.units }
      for (const k of keys) units[k] = { ...units[k], staleAt: { ...units[k].staleAt, [what]: at } }
      return { byHost: { ...s.byHost, [hostId]: { ...h, units } }, staleRev: s.staleRev + 1 }
    }),

    setRoute: (hostId, sessionId, route) => set((s) => {
      const k = sessionKey(hostId, sessionId)
      const { [k]: _old, ...rest } = s.routes
      const routes = { ...rest, [k]: route }
      const keys = Object.keys(routes)
      for (const old of keys.slice(0, Math.max(0, keys.length - MAX_ROUTES))) delete routes[old]
      return { routes }
    }),

    forgetHost: (hostId) => {
      forgetViewMemosOfHost(hostId)
      for (const k of [...pins.keys()]) if (k.startsWith(`${hostId}\u0000`)) pins.delete(k)
      for (const k of [...flights.keys()]) if (k.startsWith(`${hostId}\u0000`)) flights.delete(k)
      set((s) => ({
        support: drop(s.support, hostId), sticky: drop(s.sticky, hostId), gens: drop(s.gens, hostId), byHost: drop(s.byHost, hostId),
        routes: Object.fromEntries(Object.entries(s.routes).filter(([k, r]) => !k.startsWith(`${hostId}\u0000`) && !(r.kind === 'follow' && r.hostId === hostId))),
        epoch: { ...s.epoch, [hostId]: (s.epoch[hostId] ?? 0) + 1 },
      }))
    },

    reset: () => { clock = 0; latest.clear(); pins.clear(); flights.clear(); set({ support: {}, sticky: {}, gens: {}, byHost: {}, routes: {}, epoch: {}, staleRev: 0 }) },
  }
})

/** The host's last known journal.v1 answer: true / false, or undefined when it never answered (use the old UI). */
export const stickySupport = (hostId: string, state: Pick<JournalState, 'sticky'> = useJournalStore.getState()): boolean | undefined => state.sticky[hostId]
/** The live answer of this connection: true / false, or undefined while unknown. */
export const liveSupport = (hostId: string, state: Pick<JournalState, 'support'> = useJournalStore.getState()): boolean | undefined => state.support[hostId]

/** The unit a session names on a host (undefined until its answer was loaded). */
export function selectUnit(state: Pick<JournalState, 'byHost'>, hostId: string, sessionId: string): UnitState | undefined {
  const h = state.byHost[hostId]
  const key = h?.unitOfSession[sessionId]
  return key === undefined ? undefined : h?.units[key]
}

export const isStale = (u: UnitState, what: ChangedWhat): boolean => u.staleAt[what] > u.readAt[what]
