// spa/src/lib/fleet/store.ts — the App's picture of the fleet (fleet spec rev 2 §6, plan T-2a1.2). The App connects to both
// daemons and takes the WHOLE fleet from the PRIMARY only:
//   - per daemon host: its `fleet.v1` support, its role and term from `GET /api/fleet`, and, for a primary, its hosts;
//   - the primary = the highest term among those that answered `primary` (equal terms → the earlier host in the App's host
//     order; the spec says "earlier in `order`" — the fleet `order` is aliases the App cannot map, see the PR notes);
//   - `fleet.changed` is merged from the current primary only. A push of a higher term from another daemon changes nothing
//     here: it returns 'reread' and the caller reads both daemons again, whose answers re-choose the primary;
//   - no push for 10 s, or the primary gone → `waitingPrimary`: the last values stay (never cleared), and the caller
//     re-reads every 5 s until a primary answers. Only a standby known → `primaryElsewhere`.
// Freshness: every host has a generation (kept even after the host is forgotten); `begin` / `fence` / `forgetHost` move it,
// and a reply or support answer written under an older generation is dropped. Nothing here is persisted: the history it
// feeds is in memory too.
import { create } from 'zustand'
import { useHostStore, type HostConfig } from '../../stores/useHostStore'
import { fleetHistory } from './history'
import type { FleetAnswer, FleetHost, FleetPush } from './types'

export const STALE_MS = 10_000

export type FleetSupport = 'unknown' | 'yes' | 'no'
export type FleetRole = 'primary' | 'standby' | 'off'

interface FleetData { term: number; hosts: Record<string, FleetHost>; order: string[] }
interface HostEntry {
  gen: number
  support: FleetSupport
  role?: FleetRole
  term: number
  /** A primary's fleet, as its last answer and pushes made it. */
  data?: FleetData
}
export interface FleetSnapshot extends FleetData { hostId: string }
export type PushResult = 'applied' | 'ignored' | 'reread'

interface FleetState {
  byHost: Record<string, HostEntry>
  /** The newest generation per host, never forgotten: a re-added host must not restart at a number an old reply holds. */
  gens: Record<string, number>
  primaryHostId: string | null
  /** The fleet of the current primary, or the last one seen while none answers (kept, never cleared by staleness). */
  snapshot: FleetSnapshot | null
  waitingPrimary: boolean
  primaryElsewhere: boolean
  lastPushAt: number
  stale: boolean

  begin: (hostId: string) => number
  genOf: (hostId: string) => number
  setSupport: (hostId: string, gen: number, support: 'yes' | 'no') => void
  applyFetched: (hostId: string, gen: number, answer: FleetAnswer) => void
  applyPush: (hostId: string, push: FleetPush) => PushResult
  checkStale: (now: number) => void
  fence: (hostId: string) => void
  forgetHost: (hostId: string) => void
  reset: () => void
}

const initial = { byHost: {}, gens: {}, primaryHostId: null, snapshot: null, waitingPrimary: false, primaryElsewhere: false, lastPushAt: 0, stale: false }

function chooseCurrent(byHost: Record<string, HostEntry>): string | null {
  const order = useHostStore.getState().hostOrder
  const rank = (id: string): number => { const i = order.indexOf(id); return i < 0 ? Number.MAX_SAFE_INTEGER : i }
  let best: string | null = null
  for (const [id, e] of Object.entries(byHost)) {
    if (e.role !== 'primary' || !e.data) continue
    if (best === null) { best = id; continue }
    const b = byHost[best]
    if (e.term > b.term || (e.term === b.term && rank(id) < rank(best))) best = id
  }
  return best
}

/** The derived fields after `byHost` changed. `fresh` = data arrived from the primary just now (the staleness clock restarts). */
function derive(s: FleetState, byHost: Record<string, HostEntry>, fresh: boolean): Partial<FleetState> {
  const primaryHostId = chooseCurrent(byHost)
  const chosen = primaryHostId ? byHost[primaryHostId] : undefined
  const snapshot: FleetSnapshot | null = primaryHostId && chosen?.data ? { hostId: primaryHostId, ...chosen.data } : s.snapshot
  const stale = fresh ? false : s.stale
  const standbySeen = Object.values(byHost).some((e) => e.role === 'standby')
  return {
    byHost, primaryHostId, snapshot, stale,
    lastPushAt: fresh ? Date.now() : s.lastPushAt,
    waitingPrimary: snapshot !== null && (primaryHostId === null || stale),
    primaryElsewhere: snapshot === null && primaryHostId === null && standbySeen,
  }
}

function recordHistory(hosts: Iterable<FleetHost>): void {
  for (const h of hosts) if (h.fast) fleetHistory.record(h.id, h.fast)
}

function toData(answer: Extract<FleetAnswer, { role: 'primary' }>): FleetData {
  const hosts: Record<string, FleetHost> = {}
  const order: string[] = []
  for (const h of answer.hosts) { if (!(h.id in hosts)) order.push(h.id); hosts[h.id] = h }
  return { term: answer.term, hosts, order }
}

export const useFleetStore = create<FleetState>((set, get) => {
  /** Start a new generation for the host with a clean entry (`begin`, `fence`). */
  const restart = (hostId: string): number => {
    const gen = (get().gens[hostId] ?? 0) + 1
    set((s) => ({ ...derive(s, { ...s.byHost, [hostId]: { gen, support: 'unknown', term: 0 } }, false), gens: { ...s.gens, [hostId]: gen } }))
    return gen
  }
  const live = (hostId: string, gen: number): HostEntry | null => {
    const e = get().byHost[hostId]
    return e && e.gen === gen && get().gens[hostId] === gen ? e : null
  }

  return {
    ...initial,

    begin: restart,
    genOf: (hostId) => get().gens[hostId] ?? 0,

    setSupport: (hostId, gen, support) => {
      const e = live(hostId, gen)
      if (e) set((s) => ({ byHost: { ...s.byHost, [hostId]: { ...e, support } } }))
    },

    applyFetched: (hostId, gen, answer) => {
      const e = live(hostId, gen)
      if (!e || e.support !== 'yes') return
      const next: HostEntry = { gen, support: 'yes', role: answer.role, term: 'term' in answer ? answer.term : 0 }
      if (answer.role === 'primary') {
        next.data = toData(answer)
        recordHistory(next.data.order.map((id) => next.data!.hosts[id]))
      }
      set((s) => {
        const byHost = { ...s.byHost, [hostId]: next }
        return derive(s, byHost, answer.role === 'primary' && chooseCurrent(byHost) === hostId)
      })
    },

    applyPush: (hostId, push) => {
      const s = get()
      const e = s.byHost[hostId]
      if (!e || e.support !== 'yes') return 'ignored'
      if (hostId !== s.primaryHostId || !e.data) {
        // Not the primary: a standby never pushes, so this is news of a newer term — read both daemons again to re-choose.
        return push.term > (s.snapshot?.term ?? -1) ? 'reread' : 'ignored'
      }
      if (push.term < e.data.term) return 'ignored'
      const hosts = { ...e.data.hosts }
      const order = [...e.data.order]
      let unknown = false
      const touched: FleetHost[] = []
      for (const row of push.hosts) {
        let cur = hosts[row.id]
        if (!cur) {
          if (!row.full) { unknown = true; continue }
          cur = row.full
          order.push(row.id)
        } else if (row.full) {
          cur = { ...row.full, fast: cur.fast, slow: cur.slow }
        }
        const merged: FleetHost = { ...cur, fast: row.fast === undefined ? cur.fast : row.fast, slow: row.slow ?? cur.slow }
        if (row.last_ok_at !== undefined) merged.last_ok_at = row.last_ok_at
        if (row.status !== undefined) merged.status = row.status
        if (row.error !== undefined) merged.error = row.error
        hosts[row.id] = merged
        touched.push(merged)
      }
      recordHistory(touched)
      const data: FleetData = { term: Math.max(push.term, e.data.term), hosts, order }
      set((st) => derive(st, { ...st.byHost, [hostId]: { ...e, term: data.term, data } }, true))
      return unknown ? 'reread' : 'applied'
    },

    checkStale: (now) => {
      const s = get()
      if (s.primaryHostId === null || s.stale || now - s.lastPushAt < STALE_MS) return
      set((st) => ({ stale: true, waitingPrimary: st.snapshot !== null }))
    },

    // The connection the answers came on is gone: unknown until the next probe, and an answer still on its way must not
    // land. The primary's last values stay (shown as waiting); nothing else of this host is believed.
    fence: (hostId) => {
      if (get().byHost[hostId]) restart(hostId)
    },

    // A removed host, or one re-pointed at another daemon: what the old daemon said is not the new one's.
    forgetHost: (hostId) => {
      if (!get().byHost[hostId]) return
      const byHost = { ...get().byHost }
      delete byHost[hostId]
      set((s) => {
        const d = derive(s, byHost, false)
        const gens = { ...s.gens, [hostId]: (s.gens[hostId] ?? 0) + 1 }
        if (s.snapshot?.hostId !== hostId || d.primaryHostId) return { ...d, gens }
        return { ...d, gens, snapshot: null, waitingPrimary: false }
      })
    },

    reset: () => set({ ...initial, byHost: {}, gens: {} }),
  }
})

export interface FleetHostView extends FleetHost {
  /** The App's HostConfig id for a daemon host (matched by daemon id); the look then comes from there. */
  hostConfigId?: string
}

/**
 * The snapshot's hosts for the UI (spec §8.1 order): daemon hosts mapped to a `HostConfig` through `HostConfig.daemonId`
 * (the id `lib/host-daemon-id.ts` verifies) in the App's host order, the unmapped ones after them in wire order, then the
 * probe hosts in wire order.
 */
export function fleetHostsView(snapshot: FleetSnapshot | null, hostState: { hosts: Record<string, HostConfig>; hostOrder: string[] }): FleetHostView[] {
  if (!snapshot) return []
  const byDaemonId = new Map<string, string>()
  for (const id of hostState.hostOrder) { const d = hostState.hosts[id]?.daemonId; if (d && !byDaemonId.has(d)) byDaemonId.set(d, id) }
  const mapped: FleetHostView[] = []
  const loose: FleetHostView[] = []
  const probes: FleetHostView[] = []
  for (const h of snapshot.order.map((id) => snapshot.hosts[id])) {
    if (h.kind === 'probe') { probes.push(h); continue }
    const cfg = byDaemonId.get(h.id)
    if (cfg) mapped.push({ ...h, hostConfigId: cfg })
    else loose.push(h)
  }
  mapped.sort((a, b) => hostState.hostOrder.indexOf(a.hostConfigId!) - hostState.hostOrder.indexOf(b.hostConfigId!))
  return [...mapped, ...loose, ...probes]
}
