// spa/src/components/watch/host-pane-seed.ts — test seed for the host page's tests (MON-6f-2): one pool host in the pool store, fresh, so the page finds it through `useMonitorHosts`.
import { usePoolStore } from '../../lib/pool/store'
import { parseFacts, parseHostsAnswer } from '../../lib/pool/parse'
import darwinJson from '../../lib/pool/__fixtures__/facts_darwin.json'
import hostsJson from '../../lib/pool/__fixtures__/pool_hosts_response.json'
import type { PoolHost } from '../../lib/pool/types'

export const SEED_HOST_ID = '82b11c521df16c12'

/** A reachable pool host with facts read now (state ok); `over` replaces fields, e.g. `{ facts: null, liveness: 'wwd_down' }` for one that is not. */
export function poolHostOf(over: Partial<PoolHost> = {}): PoolHost {
  const facts = { ...parseFacts(darwinJson)!, alias: 'air26', at: Date.now() }
  return { ...parseHostsAnswer(hostsJson)![0], host_id: SEED_HOST_ID, alias: 'air26', version: '1.0.0-alpha.768', reachable: true, liveness: 'ok', liveness_at: Date.now(), facts, ...over }
}

export function seedPool(hosts: PoolHost[], sourceHostId = 'cfg-a'): void {
  usePoolStore.getState().reset()
  usePoolStore.setState({ hosts, sourceHostId })
}
