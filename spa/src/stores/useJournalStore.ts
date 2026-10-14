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
import { getJournal, getRoadmap, getTasks, type Outcome } from '../lib/journal/api'
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

/** Where a session's journal is read from. `follow`: a member whose lead lives on another daemon (409). */
export type JournalRoute =
  | { kind: 'direct' }
  | { kind: 'none' }
  | { kind: 'follow'; hostId: string; sessionId: string }
  | { kind: 'unreachable'; leadHostId: string }

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

export const useJournalStore = create<JournalState>()((set, get) => {
  /** The unit a session names on a host, if its answer was loaded. */
  const unitOf = (hostId: string, sessionId: string): UnitState | undefined => {
    const h = get().byHost[hostId]
    const key = h?.unitOfSession[sessionId]
    return key === undefined ? undefined : h?.units[key]
  }

  /** Run one read; `apply` runs only when the host was neither forgotten nor reconnected meanwhile. */
  async function read<T>(hostId: string, call: () => Promise<Outcome<T>>, apply: (value: T, startedAt: number) => void): Promise<LoadResult> {
    const epoch = get().epoch[hostId] ?? 0
    const gen = get().gens[hostId] ?? 0
    const startedAt = nextStamp()
    let out: Outcome<T>
    const current = (): boolean => (get().epoch[hostId] ?? 0) === epoch && (get().gens[hostId] ?? 0) === gen
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
      return read(hostId, () => getJournal(hostId, sessionId, before), (page, startedAt) => set((s) => {
        const key = unitKeyOf(page.unit)
        const h = s.byHost[hostId] ?? { units: {}, unitOfSession: {} }
        const held = h.units[key]
        const entries = mergeEntries(held?.entries ?? [], page.entries)
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
        const units = { ...h.units, [key]: next }
        return { byHost: { ...s.byHost, [hostId]: trim(units, { ...h.unitOfSession, [sessionId]: key }) } }
      }))
    },

    loadTasks: async (hostId, sessionId) => {
      if (!unitOf(hostId, sessionId)) return { kind: 'no_unit' }
      return read(hostId, () => getTasks(hostId, sessionId), (tasks, startedAt) => set((s) => {
        const h = s.byHost[hostId]
        const key = h?.unitOfSession[sessionId]
        const held = key === undefined ? undefined : h?.units[key]
        if (!h || key === undefined || !held) return {}
        return { byHost: { ...s.byHost, [hostId]: { ...h, units: { ...h.units, [key]: { ...held, tasks, readAt: { ...held.readAt, tasks: startedAt }, touched: nextStamp() } } } } }
      }))
    },

    loadRoadmap: async (hostId, sessionId) => {
      if (!unitOf(hostId, sessionId)) return { kind: 'no_unit' }
      return read(hostId, () => getRoadmap(hostId, sessionId), (roadmap, startedAt) => set((s) => {
        const h = s.byHost[hostId]
        const key = h?.unitOfSession[sessionId]
        const held = key === undefined ? undefined : h?.units[key]
        if (!h || key === undefined || !held) return {}
        return { byHost: { ...s.byHost, [hostId]: { ...h, units: { ...h.units, [key]: { ...held, roadmap, readAt: { ...held.readAt, roadmap: startedAt }, touched: nextStamp() } } } } }
      }))
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
      set((s) => ({
        support: drop(s.support, hostId), sticky: drop(s.sticky, hostId), gens: drop(s.gens, hostId), byHost: drop(s.byHost, hostId),
        routes: Object.fromEntries(Object.entries(s.routes).filter(([k, r]) => !k.startsWith(`${hostId}\u0000`) && !(r.kind === 'follow' && r.hostId === hostId))),
        epoch: { ...s.epoch, [hostId]: (s.epoch[hostId] ?? 0) + 1 },
      }))
    },

    reset: () => { clock = 0; set({ support: {}, sticky: {}, gens: {}, byHost: {}, routes: {}, epoch: {}, staleRev: 0 }) },
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
