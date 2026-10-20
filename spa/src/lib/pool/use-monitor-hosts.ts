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
}

// W4: when no source answers (every one failed, or none is connected) the pool store has no hosts. The sidebar then keeps the last non-empty list of this run, every host unknown;
// from the start with nothing read it is empty. Not persisted.
// The list belongs to the App host that served it: once that host is gone (another world, a removed host) the list is not this App's to show.
let remembered: { hosts: PoolHost[]; source: string | null } = { hosts: usePoolStore.getState().hosts, source: usePoolStore.getState().sourceHostId }
usePoolStore.subscribe((s) => { if (s.hosts.length > 0) remembered = { hosts: s.hosts, source: s.sourceHostId } })
export function resetRememberedPoolHosts(): void { remembered = { hosts: [], source: null } }

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
  const sourceKnown = remembered.source !== null && Object.hasOwn(appHosts, remembered.source)
  const stale = hosts.length === 0 && !answered && remembered.hosts.length > 0 && sourceKnown
  const rows = stale ? remembered.hosts : hosts
  return useMemo(() => orderPoolHosts(rows, order).map((h): MonitorHost => ({
    hostId: h.host_id,
    alias: h.alias,
    daemonHostId: owners[h.host_id] ?? null,
    // A list kept from before the failure has no word on any host now: unknown, with the last reading it had.
    state: poolHostState(stale ? { ...h, reachable: false, facts: null, liveness: 'unknown' } : h, lastFactsAt[h.host_id] ?? null, now),
    look: lookOf(h.host_id),
    poolFailure: stale ? { code: poolError?.code ?? null, since: poolError?.since ?? null } : null,
  })), [rows, stale, poolError, order, owners, lookOf, lastFactsAt, now])
}
