// spa/src/lib/fleet/config-store.ts — the fleet's host-config rows as the Hosts page edits them (fleet spec §8.2, plan F-2a2).
//
// The probe-host list lives on BOTH daemons: each daemon's row carries its own `ssh_alias`, everything else (id, name, look)
// is the same. `mergedProbeEntries` folds the two copies into one list keyed by id; `saveProbeList` writes it back to every
// listed daemon with that daemon's own revision. A 409 from either reloads both and says the list changed elsewhere; a 400's
// text is returned for the dialog. A GET that carries `dropped` (rows the daemon left out of its answer, e.g. an ssh config
// that could not be read for a moment) or `invalid` must NOT be written back silently — that would delete the left-out rows —
// so a save then returns `confirm` with the reasons, and goes through only when called again with `confirmDropped`.
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
}

export interface DroppedNotice { hostId: string; count: number; reasons: string[]; invalid: boolean }
/** What one daemon answered to a write: saved, or a 409 (`conflict`), or the reason text of any other failure. */
export interface DaemonOutcome { hostId: string; ok: boolean; conflict?: boolean; text?: string }
/** A probe row that cannot be written to every daemon: `missing` have no alias for it, `presentOn` have one. */
export interface MissingAlias { id: string; name: string; missing: string[]; presentOn: string[] }
export type SaveResult =
  | { kind: 'saved' }
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
}

interface State {
  byHost: Record<string, DaemonFleetConfig>
  /** A probe row F-2b wants shown: set by `requestProbeFocus`, cleared by the section that highlighted it. */
  focusProbe: string | null
  load: (hostId: string) => Promise<void>
  loadMany: (hostIds: string[]) => Promise<void>
  saveProbeList: (hostIds: string[], entries: ProbeEntry[], opts?: { confirmDropped?: boolean }) => Promise<SaveResult>
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

/** Per daemon host: the newest read in flight. An older read that answers later is dropped, so it cannot put an old revision back. */
const loadSeq = new Map<string, number>()

export const useFleetConfigStore = create<State>((set, get) => ({
  byHost: {},
  focusProbe: null,

  load: async (hostId) => {
    const seq = (loadSeq.get(hostId) ?? 0) + 1
    loadSeq.set(hostId, seq)
    const current = () => loadSeq.get(hostId) === seq
    set((s) => ({ byHost: { ...s.byHost, [hostId]: s.byHost[hostId] ?? EMPTY } }))
    try {
      const [cfg, aliases] = await Promise.all([fetchFleetConfig(hostId), fetchSshAliases(hostId).catch(() => [] as string[])])
      if (!current()) return
      set((s) => ({ byHost: { ...s.byHost, [hostId]: { status: 'ready', probe: cfg.probe, fleet: cfg.fleet, aliases } } }))
    } catch (err) {
      if (!current()) return
      const error = err instanceof Error ? err.message : String(err)
      set((s) => ({ byHost: { ...s.byHost, [hostId]: { ...(s.byHost[hostId] ?? EMPTY), status: 'error', error } } }))
    }
  },

  loadMany: async (hostIds) => { await Promise.all(hostIds.map((id) => get().load(id))) },

  saveProbeList: (hostIds, entries, opts) => queued(async (): Promise<SaveResult> => {
    const { byHost } = get()
    const notRead = hostIds.find((id) => byHost[id]?.status !== 'ready')
    if (notRead) return { kind: 'error', text: byHost[notRead]?.error ?? 'not loaded', outcomes: [] }
    // A daemon refuses a row whose alias it does not know (`unknown_ssh_alias`), and an empty alias is unknown: sending one
    // would half-succeed on every save. A row without an alias for a target daemon is refused here, never sent.
    const missing = missingAliases(hostIds, entries)
    if (missing.length) return { kind: 'missing_alias', rows: missing }
    if (!opts?.confirmDropped) {
      const notices = noticesOf(hostIds, byHost, (c) => c.probe)
      if (notices.length) return { kind: 'confirm', notices }
    }
    const settled = await Promise.allSettled(hostIds.map((hostId) => {
      const rows: ProbeRow[] = entries.map((e) => {
        const row: ProbeRow = { id: e.id, name: e.name, ssh_alias: e.aliases[hostId], icon: e.icon, icon_weight: e.icon_weight }
        if (e.colors) row.colors = e.colors
        return row
      })
      return putProbeHosts(hostId, rows, byHost[hostId].probe.revision)
    }))
    // Whatever happened, the revisions moved or may have: read both again (a half-written pair shows as it is).
    await get().loadMany(hostIds)
    const outcomes: DaemonOutcome[] = settled.map((r, i) => {
      const hostId = hostIds[i]
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

const sameLook = (a: ProbeRow, b: ProbeRow): boolean =>
  a.name === b.name && a.icon === b.icon && a.icon_weight === b.icon_weight && JSON.stringify(a.colors ?? null) === JSON.stringify(b.colors ?? null)

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
        if (!sameLook(firstRow.get(r.id) as ProbeRow, r)) have.mismatch = true
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
