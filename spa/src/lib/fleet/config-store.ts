// spa/src/lib/fleet/config-store.ts — the fleet's host-config rows as the Hosts page edits them (fleet spec §8.2, plan F-2a2).
//
// The probe-host list lives on BOTH daemons: each daemon's row carries its own `ssh_alias`, everything else (id, name, look)
// is the same. `mergedProbeEntries` folds the two copies into one list keyed by id for display; a save is an EDIT (`ProbeEdit`:
// upsert one row, remove one, or rewrite one the daemons disagree on) applied to EACH daemon's OWN rows, so a row the reader did
// not touch keeps every daemon's own version (a mismatch is never silently overwritten). The writes go out in parallel, each
// with that daemon's revision; the store then reads both again and returns what each daemon took. A GET that carries `dropped`
// (rows the daemon left out of its answer, e.g. an ssh config that could not be read for a moment) or `invalid` must NOT be
// written back silently — that would delete the left-out rows — so a save then returns `confirm` with the reasons, and goes
// through only when called again with `confirmDropped`.
//
// An edit remembers what it started from (`ProbeBase`: each daemon's revision and copy of the row). When a daemon's list moved
// since, the edit goes through only if that daemon's copy of THE ROW is still what the edit started from (or already what the
// edit writes); otherwise the save returns `changed` and waits for `confirmChanged` — a retry after "A saved, B answered 409"
// never overwrites B's newer content unseen.
//
// F-2b (the sidebar cells) opens the probe section on a row with `requestProbeFocus(id)`: the section highlights the row once
// and clears the request (`focusProbe` back to null). The request waits in the store until a mounted section consumes it.
import { create } from 'zustand'
import { HostConfigApiError, HostConfigConflictError } from '../host-config-api'
import { useFleetDraftStore } from './config-draft'
import { fetchFleetConfig, fetchSshAliases, putFleetRow, putProbeHosts, type FleetRow, type Marked, type ProbeRow, type Versioned } from './config-api'

export interface ProbeEntry {
  id: string
  name: string
  icon: string
  icon_weight: string
  colors?: ProbeRow['colors']
  /** Each daemon host's own SSH alias for this host, by App host id. */
  aliases: Record<string, string>
  /** The daemons that hold this id disagree on its content (name, icon or colours); the first daemon's copy is shown. */
  mismatch?: boolean
}

export interface DaemonFleetConfig {
  status: 'loading' | 'ready' | 'error'
  error?: string
  probe: Versioned<ProbeRow[]>
  fleet: Versioned<FleetRow>
  aliases: string[]
  /** False when the daemon's alias list could not be read: its aliases cannot be verified here (the daemon still checks). */
  aliasesLoaded: boolean
}

export interface DroppedNotice { hostId: string; count: number; reasons: string[]; invalid: boolean }
/** What one daemon answered to a write: saved, or a 409 (`conflict`), or the reason text of any other failure. */
export interface DaemonOutcome { hostId: string; ok: boolean; conflict?: boolean; text?: string }
/** A probe row that cannot be written to every daemon: `missing` have no alias for it, `presentOn` have one. */
export interface MissingAlias { id: string; name: string; missing: string[]; presentOn: string[] }
/** What a save changes: one row written, one removed, or one row the daemons disagree on made equal (to the first daemon's copy). */
export type ProbeEdit =
  | { kind: 'upsert'; entry: ProbeEntry }
  | { kind: 'remove'; id: string }
  | { kind: 'rewrite'; id: string }
/** Each daemon's revision of the probe list and copy of the edited row when the edit began. */
export interface ProbeBase { revisions: Record<string, number>; rows: Record<string, ProbeRow | null> }
export type SaveResult =
  | { kind: 'saved' }
  /** A daemon's copy of the edited row is not what the edit started from: ask, then save with `confirmChanged`. */
  | { kind: 'changed'; hosts: string[] }
  | { kind: 'confirm'; notices: DroppedNotice[] }
  | { kind: 'missing_alias'; rows: MissingAlias[] }
  /** At least one daemon answered 409; `outcomes` says which daemons took the write and which did not. */
  | { kind: 'conflict'; outcomes: DaemonOutcome[] }
  | { kind: 'error'; text: string; outcomes: DaemonOutcome[] }

const emptyMarks: Marked = { dropped: null, invalid: false }
const EMPTY: DaemonFleetConfig = {
  status: 'loading',
  probe: { items: [], revision: 0, ...emptyMarks },
  fleet: { items: { order: [], peer_ssh_alias: '', enabled: true }, revision: 0, ...emptyMarks },
  aliases: [],
  aliasesLoaded: false,
}

interface State {
  byHost: Record<string, DaemonFleetConfig>
  /** A probe row F-2b wants shown: set by `requestProbeFocus`, cleared by the section that highlighted it. */
  focusProbe: string | null
  load: (hostId: string) => Promise<void>
  loadMany: (hostIds: string[]) => Promise<void>
  saveProbe: (hostIds: string[], edit: ProbeEdit, opts?: { confirmDropped?: boolean; confirmChanged?: boolean; base?: ProbeBase }) => Promise<SaveResult>
  saveFleetRow: (hostId: string, row: FleetRow, opts?: { confirmDropped?: boolean }) => Promise<SaveResult>
  requestProbeFocus: (id: string) => void
  clearProbeFocus: () => void
  reset: () => void
}

function noticesOf(hostIds: string[], byHost: Record<string, DaemonFleetConfig>, pick: (c: DaemonFleetConfig) => Versioned<unknown>): DroppedNotice[] {
  const out: DroppedNotice[] = []
  for (const hostId of hostIds) {
    const v = byHost[hostId] && pick(byHost[hostId])
    if (v && (v.dropped || v.invalid)) out.push({ hostId, count: v.dropped?.count ?? 0, reasons: v.dropped?.reasons ?? [], invalid: v.invalid })
  }
  return out
}

/** Saves one at a time: a second click waits for the first, so it never writes from a revision the first just spent. */
let chain: Promise<unknown> = Promise.resolve()
const queued = <T>(fn: () => Promise<T>): Promise<T> => {
  const next = chain.then(fn, fn)
  chain = next.catch(() => undefined)
  return next
}

/**
 * Per daemon host: the newest read in flight. An older read that answers later is dropped, so it cannot put an old revision
 * back. The counter is global and never reset (a read started before `reset()` can never match a number handed out after it).
 */
let loadCounter = 0
const loadSeq = new Map<string, number>()

export const useFleetConfigStore = create<State>((set, get) => ({
  byHost: {},
  focusProbe: null,

  load: async (hostId) => {
    const seq = ++loadCounter
    loadSeq.set(hostId, seq)
    const current = () => loadSeq.get(hostId) === seq
    set((s) => ({ byHost: { ...s.byHost, [hostId]: s.byHost[hostId] ?? EMPTY } }))
    try {
      let aliasesLoaded = true
      const [cfg, aliases] = await Promise.all([fetchFleetConfig(hostId), fetchSshAliases(hostId).catch(() => { aliasesLoaded = false; return [] as string[] })])
      if (!current()) return
      set((s) => ({ byHost: { ...s.byHost, [hostId]: { status: 'ready', probe: cfg.probe, fleet: cfg.fleet, aliases, aliasesLoaded } } }))
    } catch (err) {
      if (!current()) return
      const error = err instanceof Error ? err.message : String(err)
      set((s) => ({ byHost: { ...s.byHost, [hostId]: { ...(s.byHost[hostId] ?? EMPTY), status: 'error', error } } }))
    }
  },

  loadMany: async (hostIds) => { await Promise.all(hostIds.map((id) => get().load(id))) },

  saveProbe: (hostIds, edit, opts) => queued(async (): Promise<SaveResult> => {
    const { byHost } = get()
    const notRead = hostIds.find((id) => byHost[id]?.status !== 'ready')
    if (notRead) return { kind: 'error', text: byHost[notRead]?.error ?? 'not loaded', outcomes: [] }
    const id = edit.kind === 'upsert' ? edit.entry.id : edit.id
    const rowOf = (h: string): ProbeRow | null => byHost[h].probe.items.find((r) => r.id === id) ?? null

    // Which daemons are written, and the list each one gets: ITS OWN rows with the edit applied.
    let targets = hostIds
    let build: (h: string) => ProbeRow[]
    let intended: (h: string) => ProbeRow | null = () => null
    if (edit.kind === 'upsert') {
      // A daemon refuses a row whose alias it does not know (`unknown_ssh_alias`), and an empty alias is unknown: sending one
      // would half-succeed on every save. A row without a usable alias for a target daemon is refused here, never sent.
      const missing = aliasProblems(hostIds, edit.entry, byHost)
      if (missing) return { kind: 'missing_alias', rows: [missing] }
      intended = (h) => rowFromEntry(edit.entry, h)
      build = (h) => {
        const mine = byHost[h].probe.items
        const row = rowFromEntry(edit.entry, h)
        return mine.some((r) => r.id === id) ? mine.map((r) => (r.id === id ? row : r)) : [...mine, row]
      }
    } else if (edit.kind === 'remove') {
      build = (h) => byHost[h].probe.items.filter((r) => r.id !== id)
    } else {
      const canonical = hostIds.map(rowOf).find((r): r is ProbeRow => r !== null)
      targets = canonical ? hostIds.filter((h) => { const r = rowOf(h); return r !== null && lookKey(r) !== lookKey(canonical) }) : []
      if (!canonical || targets.length === 0) return { kind: 'saved' }
      build = (h) => byHost[h].probe.items.map((r) => (r.id === id ? { ...canonical, ssh_alias: r.ssh_alias } : r))
    }

    if (!opts?.confirmDropped) {
      const notices = noticesOf(targets, byHost, (c) => c.probe)
      if (notices.length) return { kind: 'confirm', notices }
    }

    // The revision each daemon is written at: the current one (the list we build from is the current one). If the daemon's list
    // moved since the edit began, its copy of the edited row must still be what the edit started from, or already what it writes.
    const revisions: Record<string, number> = {}
    const changed: string[] = []
    for (const h of targets) {
      const cur = byHost[h].probe
      const baseRev = opts?.base?.revisions[h]
      revisions[h] = cur.revision
      if (baseRev === undefined || baseRev === cur.revision || opts?.confirmChanged) continue
      const now = rowOf(h)
      if (!sameRow(now, opts?.base?.rows[h] ?? null) && !sameRow(now, intended(h))) changed.push(h)
    }
    if (changed.length) return { kind: 'changed', hosts: changed }

    const settled = await Promise.allSettled(targets.map((h) => putProbeHosts(h, build(h), revisions[h])))
    // Whatever happened, the revisions moved or may have: read both again (a half-written pair shows as it is).
    await get().loadMany(hostIds)
    const outcomes: DaemonOutcome[] = settled.map((r, i) => {
      const hostId = targets[i]
      if (r.status === 'fulfilled') return { hostId, ok: true }
      const e: unknown = r.reason
      if (e instanceof HostConfigConflictError) return { hostId, ok: false, conflict: true }
      return { hostId, ok: false, text: e instanceof HostConfigApiError || e instanceof Error ? e.message : String(e) }
    })
    if (outcomes.every((o) => o.ok)) return { kind: 'saved' }
    if (outcomes.some((o) => o.conflict)) return { kind: 'conflict', outcomes }
    return { kind: 'error', text: outcomes.filter((o) => !o.ok).map((o) => o.text).join('\n'), outcomes }
  }),

  saveFleetRow: (hostId, row, opts) => queued(async (): Promise<SaveResult> => {
    const cfg = get().byHost[hostId]
    if (cfg?.status !== 'ready') return { kind: 'error', text: cfg?.error ?? 'not loaded', outcomes: [] }
    if (!opts?.confirmDropped) {
      const notices = noticesOf([hostId], get().byHost, (c) => c.fleet)
      if (notices.length) return { kind: 'confirm', notices }
    }
    try {
      await putFleetRow(hostId, row, cfg.fleet.revision)
    } catch (err) {
      await get().load(hostId)
      if (err instanceof HostConfigConflictError) return { kind: 'conflict', outcomes: [{ hostId, ok: false, conflict: true }] }
      const text = err instanceof Error ? err.message : String(err)
      return { kind: 'error', text, outcomes: [{ hostId, ok: false, text }] }
    }
    await get().load(hostId)
    return { kind: 'saved' }
  }),

  requestProbeFocus: (id) => set({ focusProbe: id }),
  clearProbeFocus: () => set({ focusProbe: null }),
  reset: () => { chain = Promise.resolve(); loadSeq.clear(); useFleetDraftStore.getState().reset(); set({ byHost: {}, focusProbe: null }) },
}))

/** Rows that lack an alias for some target daemon (a half row: it exists on one daemon only, or was dropped on the other). */
export function missingAliases(hostIds: string[], entries: ProbeEntry[]): MissingAlias[] {
  const out: MissingAlias[] = []
  for (const e of entries) {
    const missing = hostIds.filter((id) => !e.aliases[id])
    if (missing.length) out.push({ id: e.id, name: e.name, missing, presentOn: hostIds.filter((id) => !!e.aliases[id]) })
  }
  return out
}

/** The daemons where `entry` has no usable alias: none given, or one the daemon's loaded alias list does not carry. */
function aliasProblems(hostIds: string[], entry: ProbeEntry, byHost: Record<string, DaemonFleetConfig>): MissingAlias | null {
  const bad = (h: string) => {
    const alias = entry.aliases[h]
    return !alias || (byHost[h].aliasesLoaded && !byHost[h].aliases.includes(alias))
  }
  const missing = hostIds.filter(bad)
  return missing.length ? { id: entry.id, name: entry.name, missing, presentOn: hostIds.filter((h) => !bad(h)) } : null
}

function rowFromEntry(e: ProbeEntry, hostId: string): ProbeRow {
  const row: ProbeRow = { id: e.id, name: e.name.trim(), ssh_alias: e.aliases[hostId], icon: e.icon, icon_weight: e.icon_weight }
  if (e.colors) row.colors = e.colors
  return row
}

const DEFAULT_WEIGHT = 'regular'

/** Key order and hex case do not matter: the same colours compare equal however the daemon wrote them. */
function canon(v: unknown): unknown {
  if (typeof v === 'string') return /^#[0-9a-f]{6}$/i.test(v) ? v.toLowerCase() : v
  if (Array.isArray(v)) return v.map(canon)
  if (v && typeof v === 'object') {
    return Object.fromEntries(Object.entries(v as Record<string, unknown>).filter(([, x]) => x !== undefined).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)).map(([k, x]) => [k, canon(x)]))
  }
  return v
}

/** What two daemons must agree on: the name and the look, with an empty icon weight meaning the default weight. */
const lookKey = (r: ProbeRow): string => JSON.stringify(canon({ name: r.name, icon: r.icon, weight: r.icon_weight || DEFAULT_WEIGHT, colors: r.colors ?? null }))
const sameRow = (a: ProbeRow | null, b: ProbeRow | null): boolean => (a === null || b === null ? a === b : a.ssh_alias === b.ssh_alias && lookKey(a) === lookKey(b))

/** The revisions and the edited row's copies to remember when an edit begins (`id` null: no row yet). */
export function probeBaseOf(hostIds: string[], byHost: Record<string, DaemonFleetConfig>, id: string | null): ProbeBase {
  const revisions: Record<string, number> = {}
  const rows: Record<string, ProbeRow | null> = {}
  for (const h of hostIds) {
    revisions[h] = byHost[h]?.probe.revision ?? 0
    rows[h] = id === null ? null : byHost[h]?.probe.items.find((r) => r.id === id) ?? null
  }
  return { revisions, rows }
}

/**
 * The two daemons' copies as one list: ordered by the first daemon that has the row, each row's aliases per daemon. The
 * content shown is the first daemon's; a later daemon that disagrees on it marks the entry `mismatch`.
 */
export function mergedProbeEntries(hostIds: string[], byHost: Record<string, DaemonFleetConfig>): ProbeEntry[] {
  const out = new Map<string, ProbeEntry>()
  const firstRow = new Map<string, ProbeRow>()
  for (const hostId of hostIds) {
    for (const r of byHost[hostId]?.probe.items ?? []) {
      const have = out.get(r.id)
      if (have) {
        have.aliases[hostId] = r.ssh_alias
        if (lookKey(firstRow.get(r.id) as ProbeRow) !== lookKey(r)) have.mismatch = true
        continue
      }
      const e: ProbeEntry = { id: r.id, name: r.name, icon: r.icon, icon_weight: r.icon_weight, aliases: { [hostId]: r.ssh_alias } }
      if (r.colors) e.colors = r.colors
      out.set(r.id, e)
      firstRow.set(r.id, r)
    }
  }
  return [...out.values()]
}
