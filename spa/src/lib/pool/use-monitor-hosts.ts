// spa/src/lib/pool/use-monitor-hosts.ts — the sidebar's list of hosts, from the pool only (monitoring tabs spec M10, M13, M21, W4). The pool store's hosts in the order of the pool hosts store (the unlisted after
// them, M13), each with its state, its look and the App host that fronts it. The fleet is not read: a host the pool does not list is not here. The clock moves a host's facts from fresh to stalled between answers.
import { useMemo } from 'react'
import { usePoolStore } from './store'
import { poolHostState, type PoolHostState } from './host-state'
import { orderPoolHosts, usePoolHostLookResolver, type PoolHostResolvedLook } from './host-looks'
import { usePoolHostOwners } from './daemons'
import { useClock, useRunPool } from './use-cell-models'
import { usePoolHostsStore } from '../../stores/usePoolHostsStore'
import { useHostStore } from '../../stores/useHostStore'
import type { PoolErrorCode } from './errors'
import { hostCountsOf, type HostCount } from './host-counts'
import { queueEntries } from './jobs-lanes'
import { usePoolJobs, usePoolReadable } from './pool-jobs'
import type { PoolHost } from './types'

export interface MonitorHost {
  /** The wwd `host_id`. */
  hostId: string
  alias: string
  /** The App host that fronts this pool host (its daemon), when one is known. */
  daemonHostId: string | null
  state: PoolHostState
  look: PoolHostResolvedLook
  /** Set while the list is the kept one because the whole pool cannot be read (M25): why, as the pool store knows it. */
  poolFailure: { code: PoolErrorCode | null; since: number | null } | null
  /** The number at the cell's corner and its colour (M6, M7); null when there is none to show: a host that is not ok, a pool nobody can read (M25), or nothing to count. */
  count: HostCount | null
}

// W4: when no source answers (every one failed, or none is connected) the pool store has no hosts. The sidebar then keeps the last non-empty list of this run, every host unknown;
// from the start with nothing read it is empty. Not persisted.
// The list belongs to the App host that served it: once that host is gone (another world, a removed host) the list is not this App's to show.
let remembered: { hosts: PoolHost[]; source: string | null } = usePoolStore.getState().sourceHostId !== null ? { hosts: usePoolStore.getState().hosts, source: usePoolStore.getState().sourceHostId } : { hosts: [], source: null }
// Every successful answer is remembered, an empty one too: the pool then really is empty, and a failure after it must not bring back an older list.
usePoolStore.subscribe((s) => { if (s.sourceHostId !== null) remembered = { hosts: s.hosts, source: s.sourceHostId } })
export function resetRememberedPoolHosts(): void { remembered = { hosts: [], source: null } }

/** The name for a pool host outside a hook (a tab's label): the pool store's alias, else the one the kept list had (W4), else the short id. */
export function poolHostAliasOf(hostId: string): string {
  const live = usePoolStore.getState().hosts.find((h) => h.host_id === hostId)
  const kept = remembered.hosts.find((h) => h.host_id === hostId)
  return live?.alias || kept?.alias || hostId.slice(0, 6)
}

export function useMonitorHosts(): MonitorHost[] {
  useRunPool()
  const now = useClock()
  const hosts = usePoolStore((s) => s.hosts)
  const answered = usePoolStore((s) => s.sourceHostId !== null)
  const lastFactsAt = usePoolStore((s) => s.lastFactsAt)
  const order = usePoolHostsStore((s) => s.order)
  const owners = usePoolHostOwners()
  const lookOf = usePoolHostLookResolver()
  const appHosts = useHostStore((s) => s.hosts)
  const poolError = usePoolStore((s) => s.error)
  const jobs = usePoolJobs()
  const readable = usePoolReadable()
  const sourceKnown = remembered.source !== null && Object.hasOwn(appHosts, remembered.source)
  const stale = hosts.length === 0 && !answered && remembered.hosts.length > 0 && sourceKnown
  const rows = stale ? remembered.hosts : hosts
  const counts = useMemo(() => (readable ? hostCountsOf(queueEntries(jobs, now, hosts), jobs) : {}), [readable, jobs, now, hosts])
  return useMemo(() => orderPoolHosts(rows, order).map((h): MonitorHost => {
    const state = poolHostState(stale ? { ...h, reachable: false, facts: null, liveness: 'unknown' } : h, lastFactsAt[h.host_id] ?? null, now)
    return {
      hostId: h.host_id,
      alias: h.alias,
      daemonHostId: owners[h.host_id] ?? null,
      // A list kept from before the failure has no word on any host now: unknown, with the last reading it had.
      state,
      look: lookOf(h.host_id),
      poolFailure: stale ? { code: poolError?.code ?? null, since: poolError?.since ?? null } : null,
      count: state.state === 'ok' ? (counts[h.alias] ?? null) : null,
    }
  }), [rows, stale, poolError, order, owners, lookOf, lastFactsAt, now, counts])
}
