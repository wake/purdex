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
import { fetchFleetConfig, fetchSshAliases, putFleetRow, putProbeHosts, type FleetRow, type Marked, type ProbeRow, type Versioned } from './config-api'

export interface ProbeEntry {
  id: string
  name: string
  icon: string
  icon_weight: string
  colors?: ProbeRow['colors']
  /** Each daemon host's own SSH alias for this host, by App host id. */
  aliases: Record<string, string>
}

export interface DaemonFleetConfig {
  status: 'loading' | 'ready' | 'error'
  error?: string
  probe: Versioned<ProbeRow[]>
  fleet: Versioned<FleetRow>
  aliases: string[]
}

export interface DroppedNotice { hostId: string; count: number; reasons: string[]; invalid: boolean }
export type SaveResult =
  | { kind: 'saved' }
  | { kind: 'confirm'; notices: DroppedNotice[] }
  | { kind: 'conflict' }
  | { kind: 'error'; text: string }

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

export const useFleetConfigStore = create<State>((set, get) => ({
  byHost: {},
  focusProbe: null,

  load: async (hostId) => {
    set((s) => ({ byHost: { ...s.byHost, [hostId]: s.byHost[hostId] ?? EMPTY } }))
    try {
      const [cfg, aliases] = await Promise.all([fetchFleetConfig(hostId), fetchSshAliases(hostId).catch(() => [] as string[])])
      set((s) => ({ byHost: { ...s.byHost, [hostId]: { status: 'ready', probe: cfg.probe, fleet: cfg.fleet, aliases } } }))
    } catch (err) {
      const error = err instanceof Error ? err.message : String(err)
      set((s) => ({ byHost: { ...s.byHost, [hostId]: { ...(s.byHost[hostId] ?? EMPTY), status: 'error', error } } }))
    }
  },

  loadMany: async (hostIds) => { await Promise.all(hostIds.map((id) => get().load(id))) },

  saveProbeList: (hostIds, entries, opts) => queued(async (): Promise<SaveResult> => {
    const { byHost } = get()
    const notRead = hostIds.find((id) => byHost[id]?.status !== 'ready')
    if (notRead) return { kind: 'error', text: byHost[notRead]?.error ?? 'not loaded' }
    if (!opts?.confirmDropped) {
      const notices = noticesOf(hostIds, byHost, (c) => c.probe)
      if (notices.length) return { kind: 'confirm', notices }
    }
    const settled = await Promise.allSettled(hostIds.map((hostId) => {
      const rows: ProbeRow[] = entries.map((e) => {
        const row: ProbeRow = { id: e.id, name: e.name, ssh_alias: e.aliases[hostId] ?? '', icon: e.icon, icon_weight: e.icon_weight }
        if (e.colors) row.colors = e.colors
        return row
      })
      return putProbeHosts(hostId, rows, byHost[hostId].probe.revision)
    }))
    // Whatever happened, the revisions moved or may have: read both again (a half-written pair shows as it is).
    await get().loadMany(hostIds)
    const failed = settled.filter((r): r is PromiseRejectedResult => r.status === 'rejected').map((r) => r.reason)
    if (failed.some((e) => e instanceof HostConfigConflictError)) return { kind: 'conflict' }
    if (failed.length) {
      const e = failed[0]
      return { kind: 'error', text: e instanceof HostConfigApiError || e instanceof Error ? e.message : String(e) }
    }
    return { kind: 'saved' }
  }),

  saveFleetRow: (hostId, row, opts) => queued(async (): Promise<SaveResult> => {
    const cfg = get().byHost[hostId]
    if (cfg?.status !== 'ready') return { kind: 'error', text: cfg?.error ?? 'not loaded' }
    if (!opts?.confirmDropped) {
      const notices = noticesOf([hostId], get().byHost, (c) => c.fleet)
      if (notices.length) return { kind: 'confirm', notices }
    }
    try {
      await putFleetRow(hostId, row, cfg.fleet.revision)
    } catch (err) {
      await get().load(hostId)
      if (err instanceof HostConfigConflictError) return { kind: 'conflict' }
      return { kind: 'error', text: err instanceof Error ? err.message : String(err) }
    }
    await get().load(hostId)
    return { kind: 'saved' }
  }),

  requestProbeFocus: (id) => set({ focusProbe: id }),
  clearProbeFocus: () => set({ focusProbe: null }),
  reset: () => { chain = Promise.resolve(); set({ byHost: {}, focusProbe: null }) },
}))

/** The two daemons' copies as one list: ordered by the first daemon that has the row, each row's aliases per daemon. */
export function mergedProbeEntries(hostIds: string[], byHost: Record<string, DaemonFleetConfig>): ProbeEntry[] {
  const out = new Map<string, ProbeEntry>()
  for (const hostId of hostIds) {
    for (const r of byHost[hostId]?.probe.items ?? []) {
      const have = out.get(r.id)
      if (have) { have.aliases[hostId] = r.ssh_alias; continue }
      const e: ProbeEntry = { id: r.id, name: r.name, icon: r.icon, icon_weight: r.icon_weight, aliases: { [hostId]: r.ssh_alias } }
      if (r.colors) e.colors = r.colors
      out.set(r.id, e)
    }
  }
  return [...out.values()]
}
