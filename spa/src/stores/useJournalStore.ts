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
// the 待確認 band (`confirms`) is the server's list of every open 待確認 of the unit, newest first (an answer without `confirms[]` has no
// band). EVENTS NEVER FETCH: `markStale` only raises a flag, the loader decides
// whether to read (journal-loader.ts). A flag is cleared by a read that STARTED after the event (`staleAt` > `readAt`
// compare store-clock stamps), so an event landing during a read keeps the unit stale.
// A read that lands after its host was forgotten (`epoch`) or its connection ended (`gens`) is dropped.
import { create } from 'zustand'
import { getJournal, getJournalPage, getJournalSessions, getRoadmap, getTasks, setConfirmItem, type Outcome } from '../lib/journal/api'
import { useI18nStore } from './useI18nStore'
import { useUndoToast } from './useUndoToast'
import { cursorOf, mergeEntries, sortBand } from '../lib/journal/merge'
import { forgetViewMemosOfHost } from '../lib/journal/view-memory'
import { unitKeyOf, type ChangedWhat, type JournalEntry, type JournalProject, type JournalTask, type JournalUnit, type RemoteLead, type RoadmapFeature, type SessionLink } from '../lib/journal/types'

/** Units kept per host: the least recently touched past this are dropped (events can name any unit, the store must not grow with them). */
export const MAX_UNITS = 30
export const MAX_ROUTES = 200
/** Other sessions' pages kept per unit (session pages, JV-4): the least recently read past this are dropped, a page is read again when shown. */
export const MAX_PAGES = 10

type Stamps = Record<ChangedWhat, number>
const zero = (): Stamps => ({ entries: 0, tasks: 0, roadmap: 0 })

/** A session page that is not the current one (journal.sessions.v1): its rows, whether the daemon cut it, and when its read STARTED (store clock). */
export interface PageState { entries: JournalEntry[]; truncated: boolean; readAt: number }

export interface UnitState {
  unit: JournalUnit
  project: JournalProject
  personName: string
  /** Oldest first, unique by id. */
  entries: JournalEntry[]
  /** The open 待確認 band, newest first: every entry with an unchecked item, across the whole unit. */
  confirms: JournalEntry[]
  /** The oldest loaded entry is not the unit's first: an older month can be loaded. */
  more: boolean
  openConfirms: number
  /** Session pages (journal.sessions.v1). `entries` above is the CURRENT session's page; '' = the host does not declare it / not read yet. */
  currentPage: string
  /** The current page held more than the daemon's cap and the newest are kept. */
  truncated: boolean
  /** The leader's chain, oldest first; null = not read yet. */
  sessions: SessionLink[] | null
  /** A predecessor reader is not registered: sessions older than the first listed may be missing. */
  sessionsIncomplete: boolean
  /** The last read of the chain failed (the daemon's 503: a loop, a chain too long, a read error, no lead); ends only when a chain arrives. The current page is unaffected. */
  sessionsFailed: boolean
  /** The other sessions' pages that were read, by leader session id (least recently read first). */
  pages: Record<string, PageState>
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
  /** Live journal.confirm.v1 of the current connection (cleared by `fence`): hand checks can be written and the band reads `confirms[]`. */
  confirmSupport: Record<string, boolean>
  /** Live journal.sessions.v1 of the current connection (cleared by `fence`): the stream reads one leader session at a time. */
  sessionsSupport: Record<string, boolean>
  /** `sessionKey` → the newest session-page read of that session failed (the daemon's 503: no lead, a chain error): show it, not an empty page. */
  readFailed: Record<string, boolean>
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
  setSupport: (hostId: string, yes: boolean, confirm?: boolean, sessions?: boolean) => void
  /** The latest page (no `older`) or the next older page of the unit this session names. */
  loadJournal: (hostId: string, sessionId: string, opts?: { older?: boolean }) => Promise<LoadResult>
  /** The leader's chain of sessions (journal.sessions.v1). */
  loadSessions: (hostId: string, sessionId: string) => Promise<LoadResult>
  /** One OTHER session's page: it lands in `pages` only and leaves the current page, the band and the open count alone. */
  loadPage: (hostId: string, sessionId: string, page: string) => Promise<LoadResult>
  loadTasks: (hostId: string, sessionId: string) => Promise<LoadResult>
  loadRoadmap: (hostId: string, sessionId: string) => Promise<LoadResult>
  /** Check / uncheck item `idx` of a 待確認 of the unit this session names: the screen follows at once, the PUT follows; see `checks` below for how a failure or a later read settles it. */
  setConfirm: (hostId: string, sessionId: string, entryId: string, idx: number, done: boolean, fallback?: JournalEntry) => Promise<void>
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
 * Pending optimistic hand checks, per (host, unit, entry, item). `gen` is the item's newest mutation; `base` is the value the server is
 * known to hold (the item's value before the first unconfirmed click, or that of the newest confirmed mutation, `baseGen`). A failed
 * PUT reverts to `base` only when its generation is still the newest (a later click owns the display), and only while the record
 * exists: a server read of the entry deletes it, because what the server says wins from then on. `at` is the store-clock stamp of the
 * newest click and `value` what that click shows: only a read that STARTED after `at` speaks for the item; one sent before the
 * click and landing after it carries the old state, so the click keeps the display.
 */
interface CheckRec { gen: number; base: boolean; baseGen: number; at: number; value: boolean }
const checks = new Map<string, CheckRec>()
let checkGen = 0
/** PUTs out per item (`overlapped`: more than one was out at the same time, so the server may hold them in a different order). */
const flights = new Map<string, { n: number; overlapped: boolean }>()
const checkKey = (hostId: string, unitKey: string, entryId: string, idx: number): string => `${hostId}\u0000${unitKey}\u0000${entryId}\u0000${idx}`
/**
 * The newest copy of an entry only the band holds (an older session / month: not in `entries`), kept while it has pending checks.
 * Checking its last item takes it out of `confirms[]`, so a failed write would find it nowhere to roll back into: the rollback
 * restores it from here. Dropped with the pending checks of the entry (a server read, forgetHost, reset).
 */
const orphans = new Map<string, JournalEntry>()
const entryKey = (hostId: string, unitKey: string, entryId: string): string => `${hostId}\u0000${unitKey}\u0000${entryId}`
/** A read that started at `startedAt` ends the pending checks it speaks for (those clicked before it started, and of an entry named by `only` when given). */
const dropChecks = (hostId: string, unitKey: string, startedAt: number, only?: ReadonlySet<string>): void => {
  const prefix = `${hostId}\u0000${unitKey}\u0000`
  for (const [k, rec] of [...checks]) if (k.startsWith(prefix) && rec.at < startedAt && (!only || only.has(k.slice(prefix.length).split('\u0000')[0]))) checks.delete(k)
}
/** Checks still pending after a read: the click keeps the display over what the (older) read carried. */
function keepChecks(hostId: string, unitKey: string, u: UnitState): UnitState {
  const prefix = `${hostId}\u0000${unitKey}\u0000`
  let out = u
  for (const [k, rec] of checks) {
    if (!k.startsWith(prefix)) continue
    const [entryId, idx] = k.slice(prefix.length).split('\u0000')
    const entry = findEntry(out, entryId)
    if (entry) out = withItem(out, entry, Number(idx), rec.value)
  }
  return out
}
/** An entry of the unit by id: in the current page, the band, or another session's page. */
export const findEntry = (u: UnitState, id: string): JournalEntry | undefined =>
  u.entries.find((e) => e.id === id) ?? u.confirms.find((e) => e.id === id) ?? Object.values(u.pages).flatMap((p) => p.entries).find((e) => e.id === id)

/**
 * `entry` with item `idx` set to `value`, in the stream and in the band. An entry with an unchecked item is in the band (newest first),
 * one with every item checked leaves it; the open count moves by the one item. No-op when the item is not there or already has the value.
 */
function withItem(u: UnitState, entry: JournalEntry, idx: number, value: boolean): UnitState {
  const cur = entry.items[idx]
  if (!cur || cur.done === value) return u
  const next = { ...entry, items: entry.items.map((it, i) => (i === idx ? { ...it, done: value } : it)) }
  const rest = u.confirms.filter((e) => e.id !== entry.id)
  const pages = Object.fromEntries(Object.entries(u.pages).map(([k, p]) => [k, p.entries.some((e) => e.id === entry.id) ? { ...p, entries: p.entries.map((e) => (e.id === entry.id ? next : e)) } : p]))
  return {
    ...u,
    pages,
    entries: u.entries.map((e) => (e.id === entry.id ? next : e)),
    confirms: next.items.some((it) => !it.done) ? sortBand([...rest, next]) : rest,
    openConfirms: Math.max(0, u.openConfirms + (value ? -1 : 1)),
  }
}

const hasSessionState = (u: UnitState): boolean => u.currentPage !== '' || u.truncated || u.sessions !== null || u.sessionsFailed || Object.keys(u.pages).length > 0
const forgetSessionState = (u: UnitState): UnitState => ({ ...u, entries: [], more: false, currentPage: '', truncated: false, sessions: null, sessionsIncomplete: false, sessionsFailed: false, pages: {} })

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
    support: {}, confirmSupport: {}, sessionsSupport: {}, readFailed: {}, sticky: {}, gens: {}, byHost: {}, routes: {}, epoch: {}, staleRev: 0,

    fence: (hostId) => set((s) => ({ support: drop(s.support, hostId), confirmSupport: drop(s.confirmSupport, hostId), sessionsSupport: drop(s.sessionsSupport, hostId), gens: { ...s.gens, [hostId]: (s.gens[hostId] ?? 0) + 1 } })),
    setSupport: (hostId, yes, confirm = false, sessions = false) => set((s) => {
      const next = { support: { ...s.support, [hostId]: yes }, confirmSupport: { ...s.confirmSupport, [hostId]: yes && confirm }, sessionsSupport: { ...s.sessionsSupport, [hostId]: yes && sessions }, sticky: { ...s.sticky, [hostId]: yes } }
      if (yes && sessions) return next
      // The same endpoint can come back as an older daemon (no `fence`-time forget: the host was not re-pointed): what session mode left
      // on the host is not this daemon's. The session fields go, and so do the rows (the first legacy read must not merge into a session page).
      const h = s.byHost[hostId]
      const units = h && Object.fromEntries(Object.entries(h.units).map(([k, u]) => [k, hasSessionState(u) ? forgetSessionState(u) : u]))
      return { ...next, readFailed: Object.fromEntries(Object.entries(s.readFailed).filter(([k]) => !k.startsWith(`${hostId}\u0000`))), ...(h ? { byHost: { ...s.byHost, [hostId]: { ...h, units: units! } } } : {}) }
    }),

    loadJournal: async (hostId, sessionId, opts) => {
      const older = opts?.older === true
      // journal.sessions.v1: the stream shows one leader session; there are no older months to load, and the newest read is a session page.
      const sessionMode = get().sessionsSupport[hostId] === true
      if (older && sessionMode) return { kind: 'ok' }
      let before: string | undefined
      if (older) {
        const u = unitOf(hostId, sessionId)
        const oldest = u?.entries[0]
        if (!u || !oldest) return { kind: 'no_unit' }
        if (!u.more) return { kind: 'ok' }
        before = cursorOf(oldest)
      }
      const res = await read(hostId, slotOf(hostId, sessionId, older ? 'older' : 'newest'), () => (sessionMode ? getJournalPage(hostId, sessionId) : getJournal(hostId, sessionId, before)), (page, startedAt) => set((s) => {
        const key = unitKeyOf(page.unit)
        const h = s.byHost[hostId] ?? { units: {}, unitOfSession: {} }
        const held = h.units[key]
        // What the server says about an item wins over a pending optimistic check of it (the newest page speaks for the whole band).
        dropChecks(hostId, key, startedAt, older ? new Set(page.entries.map((e) => e.id)) : undefined)
        // The band is `confirms[]`; an answer without it has no open 待確認.
        const confirmBand = page.confirms !== null
        // A session page of ANOTHER leader session than the held current one (a relay or /clear happened) is a different list, not new rows.
        const paged = sessionMode && page.page !== ''
        const sameSession = !paged || held?.currentPage === page.page
        let entries = mergeEntries(sameSession ? (held?.entries ?? []) : [], page.entries)
        if (!older && confirmBand) {
          // Held rows the page does not carry (older months) follow the band the server just sent: an open one takes its items, one missing from it is answered.
          const band = new Map(page.confirms!.map((e) => [e.id, e]))
          const inPage = new Set(page.entries.map((e) => e.id))
          entries = entries.map((e) => {
            if (inPage.has(e.id) || e.kind !== 'confirm') return e
            const open = band.get(e.id)
            return { ...e, items: open ? open.items : e.items.map((it) => (it.done ? it : { ...it, done: true })) }
          })
        }
        const readAt = { ...(held?.readAt ?? zero()) }
        // Only a read of the newest page speaks for "entries are fresh"; an older page says nothing about new events.
        if (!older) readAt.entries = startedAt
        const next: UnitState = {
          unit: page.unit, project: page.project, personName: page.personName, entries,
          // The band and the open count are the unit's live state: only the newest page carries them.
          confirms: older ? (held?.confirms ?? []) : sortBand(page.confirms ?? []),
          openConfirms: older ? (held?.openConfirms ?? 0) : page.openConfirms,
          // `more` describes the OLDEST loaded entry: an older page sets it; a newest-page re-read keeps it once older months are held.
          more: paged ? false : older || !held || held.entries.length === 0 ? page.more : held.more,
          currentPage: paged ? page.page : (held?.currentPage ?? ''),
          truncated: paged ? page.truncated : (held?.truncated ?? false),
          sessions: held?.sessions ?? null, sessionsIncomplete: held?.sessionsIncomplete ?? false, sessionsFailed: held?.sessionsFailed ?? false, pages: held?.pages ?? {},
          tasks: held?.tasks ?? null, roadmap: held?.roadmap ?? null,
          staleAt: held?.staleAt ?? zero(), readAt, touched: nextStamp(),
        }
        const units = { ...h.units, [key]: keepChecks(hostId, key, next) }
        return { byHost: { ...s.byHost, [hostId]: trim(units, { ...h.unitOfSession, [sessionId]: key }) } }
      }))
      if (sessionMode && !older) {
        // A 503 (no lead, a chain error) is an error to show, not an empty page; a good read ends it.
        const k = sessionKey(hostId, sessionId)
        if (res.kind === 'failed') set((s) => ({ readFailed: { ...s.readFailed, [k]: true } }))
        else if (res.kind === 'ok') {
          set((s) => (s.readFailed[k] ? { readFailed: drop(s.readFailed, k) } : {}))
          // Its own failure leaves the page as read (and marks the chain failed); a 409 on the chain goes to the caller like one on the page: the loader re-routes.
          const chain = await get().loadSessions(hostId, sessionId)
          if (chain.kind === 'remote') return chain
        }
      }
      return res
    },

    loadSessions: async (hostId, sessionId) => {
      if (!unitOf(hostId, sessionId)) return { kind: 'no_unit' }
      const startedKey = get().byHost[hostId]?.unitOfSession[sessionId]
      const res = await read(hostId, slotOf(hostId, sessionId, 'sessions'), () => getJournalSessions(hostId, sessionId), (chain) => set((s) => {
        const h = s.byHost[hostId]
        const key = h?.unitOfSession[sessionId]
        const held = key === undefined ? undefined : h?.units[key]
        if (!h || key === undefined || key !== startedKey || !held) return {}
        return { byHost: { ...s.byHost, [hostId]: { ...h, units: { ...h.units, [key]: { ...held, sessions: chain.sessions, sessionsIncomplete: chain.incomplete, sessionsFailed: false } } } } }
      }))
      // A chain that could not be read is an error to show (the pager is unusable), not "no earlier sessions"; the rows of the current page stay.
      if (res.kind === 'failed' || res.kind === 'none' || res.kind === 'remote') {
        set((s) => {
          const h = s.byHost[hostId]
          const held = startedKey === undefined ? undefined : h?.units[startedKey]
          return h && held && h.unitOfSession[sessionId] === startedKey ? { byHost: { ...s.byHost, [hostId]: { ...h, units: { ...h.units, [startedKey!]: { ...held, sessionsFailed: true } } } } } : {}
        })
      }
      return res
    },

    loadPage: async (hostId, sessionId, pageId) => {
      if (!unitOf(hostId, sessionId)) return { kind: 'no_unit' }
      const startedKey = get().byHost[hostId]?.unitOfSession[sessionId]
      return read(hostId, slotOf(hostId, sessionId, `page:${pageId}`), () => getJournalPage(hostId, sessionId, pageId), (page, startedAt) => set((s) => {
        const h = s.byHost[hostId]
        const key = h?.unitOfSession[sessionId]
        const held = key === undefined ? undefined : h?.units[key]
        if (!h || key === undefined || key !== startedKey || !held) return {}
        // Only the rows: the band and the open count are the whole unit's and come with the newest read of the current page.
        const { [pageId]: _old, ...others } = held.pages
        const pages = { ...others, [pageId]: { entries: mergeEntries([], page.entries), truncated: page.truncated, readAt: startedAt } }
        for (const old of Object.keys(pages).slice(0, Math.max(0, Object.keys(pages).length - MAX_PAGES))) delete pages[old]
        return { byHost: { ...s.byHost, [hostId]: { ...h, units: { ...h.units, [key]: keepChecks(hostId, key, { ...held, pages, touched: nextStamp() }) } } } }
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

    setConfirm: async (hostId, sessionId, entryId, idx, done, fallback) => {
      if (get().confirmSupport[hostId] !== true) return
      const u = unitOf(hostId, sessionId)
      if (!u) return
      const unitKey = unitKeyOf(u.unit)
      // Last resorts when neither the stream nor the band holds the entry (every item answered, it left the band): the copy kept for a
      // rollback, then the caller's own (the detail keeps showing the last copy it saw).
      // Held = the current page, the band or another session's page (a detail opened on an older page): found there, never an orphan.
      const held = findEntry(u, entryId)
      const entry = held ?? orphans.get(entryKey(hostId, unitKey, entryId)) ?? fallback
      if (!entry || entry.kind !== 'confirm' || entry.id !== entryId) return
      if (!held) orphans.set(entryKey(hostId, unitKey, entryId), entry)
      const k = checkKey(hostId, unitKey, entryId, idx)
      const shown = entry.items[idx]?.done
      if (shown === undefined) return
      const pending = checks.get(k)
      if (shown === done && !pending) return
      const gen = ++checkGen
      checks.set(k, { gen, base: pending?.base ?? shown, baseGen: pending?.baseGen ?? 0, at: nextStamp(), value: done })
      const apply = (value: boolean): void => set((s) => {
        const h = s.byHost[hostId]
        const cur = h?.units[unitKey]
        // The entry as the store holds it NOW (a read may have changed its other fields since the click), not the object clicked.
        const ek = entryKey(hostId, unitKey, entryId)
        const now = (cur ? findEntry(cur, entryId) : undefined) ?? orphans.get(ek)
        if (!h || !cur || !now) return {}
        const nextUnit = withItem(cur, now, idx, value)
        // An entry the stream does not hold lives only in the band: remember its newest copy for a rollback after it left.
        if (!cur.entries.some((e) => e.id === entryId) && !Object.values(cur.pages).some((p) => p.entries.some((e) => e.id === entryId))) orphans.set(ek, nextUnit.confirms.find((e) => e.id === entryId) ?? { ...now, items: now.items.map((it, i) => (i === idx ? { ...it, done: value } : it)) })
        return { byHost: { ...s.byHost, [hostId]: { ...h, units: { ...h.units, [unitKey]: nextUnit } } } }
      })
      apply(done)
      const epoch = get().epoch[hostId] ?? 0
      const flight = flights.get(k) ?? { n: 0, overlapped: false }
      flight.n++
      if (flight.n > 1) flight.overlapped = true
      flights.set(k, flight)
      let ok = false
      try { ok = (await setConfirmItem(hostId, sessionId, entryId, idx, done)).kind === 'ok' } catch { /* ok stays false */ }
      // PUTs of one item can reach the server in the opposite order of the clicks: when the last one ends, read the unit once more.
      const reread = --flight.n === 0 && flight.overlapped
      if (flight.n === 0 && flights.get(k) === flight) flights.delete(k)
      try {
        const rec = checks.get(k)
        if ((get().epoch[hostId] ?? 0) !== epoch || !rec) return // the host was forgotten, or a server read has the last word
        if (ok) {
          if (rec.gen === gen) checks.delete(k)
          else if (gen > rec.baseGen) checks.set(k, { ...rec, base: done, baseGen: gen })
          return
        }
        if (rec.gen !== gen) return // a later click owns the display now
        checks.delete(k)
        apply(rec.base)
        useUndoToast.getState().show(useI18nStore.getState().t('journal.confirm.failed'))
      } finally {
        const ek = entryKey(hostId, unitKey, entryId)
        if (![...checks.keys()].some((c) => c.startsWith(`${ek}\u0000`))) orphans.delete(ek) // nothing pending on the entry: no rollback needs its copy
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
      for (const k of [...checks.keys()]) if (k.startsWith(`${hostId}\u0000`)) checks.delete(k)
      for (const k of [...flights.keys()]) if (k.startsWith(`${hostId}\u0000`)) flights.delete(k)
      for (const k of [...orphans.keys()]) if (k.startsWith(`${hostId}\u0000`)) orphans.delete(k)
      set((s) => ({
        readFailed: Object.fromEntries(Object.entries(s.readFailed).filter(([k]) => !k.startsWith(`${hostId}\u0000`))),
        support: drop(s.support, hostId), confirmSupport: drop(s.confirmSupport, hostId), sessionsSupport: drop(s.sessionsSupport, hostId), sticky: drop(s.sticky, hostId), gens: drop(s.gens, hostId), byHost: drop(s.byHost, hostId),
        routes: Object.fromEntries(Object.entries(s.routes).filter(([k, r]) => !k.startsWith(`${hostId}\u0000`) && !(r.kind === 'follow' && r.hostId === hostId))),
        epoch: { ...s.epoch, [hostId]: (s.epoch[hostId] ?? 0) + 1 },
      }))
    },

    reset: () => { clock = 0; latest.clear(); checks.clear(); flights.clear(); orphans.clear(); set({ support: {}, confirmSupport: {}, sessionsSupport: {}, readFailed: {}, sticky: {}, gens: {}, byHost: {}, routes: {}, epoch: {}, staleRev: 0 }) },
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
