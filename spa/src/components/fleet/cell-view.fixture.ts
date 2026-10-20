// A `CellView` for the sidebar cell and card tests: an ok host with a reading unless told otherwise.
import type { CellFast, CellView } from '../../lib/pool/cell-model'
import { poolHostState } from '../../lib/pool/host-state'
import { parseFacts, parseHostsAnswer } from '../../lib/pool/parse'
import darwinJson from '../../lib/pool/__fixtures__/facts_darwin.json'
import hostsJson from '../../lib/pool/__fixtures__/pool_hosts_response.json'

export const FACTS = parseFacts(darwinJson)!
export const NOW = FACTS.at + 5000
const BASE = parseHostsAnswer(hostsJson)![0]

export const fast = (over: Partial<CellFast> = {}): CellFast => ({
  at: FACTS.at, cpu: 20, mem: 40, level: 1, page_rate: 0, disk: 50, disk_free_gb: 100, load1: 1, ncpu: 8, zone: 'normal', load: 40, ...over,
})

const DEFAULT_LOOK = { icon: 'Desktop', colors: { console: { main: { color: '#336699', alpha: 100 } } }, indicator: { shape: 'ring', data: ['load'] } } as CellView['look']

/** An ok host. `liveness` makes it a host in another state (no readings). */
export function cellView(over: Partial<CellView> & { liveness?: 'wwd_down' | 'host_down' | 'unknown'; lastFactsAt?: number | null } = {}): CellView {
  const { liveness, lastFactsAt = null, ...rest } = over
  const pool = { ...BASE, host_id: '82b11c521df16c12', alias: 'mlab', reachable: liveness === undefined, facts: liveness === undefined ? FACTS : null, ...(liveness ? { liveness } : {}) }
  const state = poolHostState(pool, lastFactsAt, NOW)
  const ok = state.state === 'ok'
  return {
    hostId: '82b11c521df16c12', name: 'mlab', state, look: DEFAULT_LOOK, poolFailure: null,
    fast: ok ? fast() : null, zone: ok ? 'normal' : null,
    load: null, worker: null, headroom: null, reasons: null, sessionsRoom: null, owner: null, psi: null,
    ...rest,
  }
}
