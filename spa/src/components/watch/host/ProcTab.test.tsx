// spa/src/components/watch/host/ProcTab.test.tsx — the host page's 行程 tab (monitoring tabs spec M45; plan MON-8a): two process tables (CPU top 12, memory top 12) and the resident programs, from the facts of a host that is ok; a host that is not ok says it is offline.
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, within, cleanup } from '@testing-library/react'
import { ProcTab } from './ProcTab'
import { poolHostState } from '../../../lib/pool/host-state'
import { parseFacts, parseHostsAnswer } from '../../../lib/pool/parse'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import type { PoolFacts, PoolHost, PoolProc } from '../../../lib/pool/types'
import darwinJson from '../../../lib/pool/__fixtures__/facts_darwin.json'
import hostsJson from '../../../lib/pool/__fixtures__/pool_hosts_response.json'
import { useI18nStore } from '../../../stores/useI18nStore'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const darwin = parseFacts(darwinJson)!
const NOW = darwin.at + 5000
const base = parseHostsAnswer(hostsJson)![0]
const poolHost = (over: Partial<PoolHost> = {}): PoolHost => ({ ...base, alias: 'mlab', host_id: 'h-mlab', facts: darwin, reachable: true, ...over })
const proc = (pid: number, over: Partial<PoolProc> = {}): PoolProc => ({ pid, name: `p${pid}`, cpu_pct: 1, mem_mb: 100, mem_kind: 'footprint', owner: null, ...over })
const hostOf = (ph: PoolHost): MonitorHost => ({ hostId: ph.host_id, alias: ph.alias, daemonHostId: null, state: poolHostState(ph, null, NOW), look: {} as never, poolFailure: null, count: null, local: false })
const okHost = (over: Partial<PoolFacts> = {}, id = 'h-mlab') => hostOf(poolHost({ host_id: id, facts: { ...darwin, ...over } }))
const downHost = (liveness: 'wwd_down' | 'host_down' | 'unknown') => hostOf(poolHost({ liveness, liveness_at: NOW - 3000, liveness_since: NOW - 600_000, facts: null }))

beforeEach(() => cleanup())

describe('ProcTab', () => {
  it('an ok host: the CPU table and the memory table, each its own top, and the resident programs', () => {
    const procs = [proc(1, { cpu_pct: 90, mem_mb: 10 }), proc(2, { cpu_pct: 5, mem_mb: 9000 })]
    render(<ProcTab host={okHost({ procs, residents: [{ session: 'ab12', kind: 'tsserver', count: 2, mem_mb: 300 }] })} now={NOW} />)
    expect(within(screen.getByTestId('procs-cpu')).getAllByRole('row')[1]).toHaveTextContent('p1')
    expect(within(screen.getByTestId('procs-mem')).getAllByRole('row')[1]).toHaveTextContent('p2')
    expect(screen.getByTestId('residents')).toHaveTextContent('tsserver')
    expect(screen.getByTestId('proc-tab')).toHaveTextContent(t('watch.host.proc.every'))
  })

  it('each table lists at most 12 rows', () => {
    const procs = Array.from({ length: 30 }, (_, i) => proc(i + 1, { cpu_pct: i, mem_mb: i }))
    render(<ProcTab host={okHost({ procs })} now={NOW} />)
    expect(screen.getByTestId('procs-cpu').querySelectorAll('tbody tr')).toHaveLength(12)
    expect(screen.getByTestId('procs-mem').querySelectorAll('tbody tr')).toHaveLength(12)
  })

  it.each(['wwd_down', 'host_down', 'unknown'] as const)('%s: both tables and the resident programs say offline', (state) => {
    render(<ProcTab host={downHost(state)} now={NOW} />)
    for (const id of ['procs-cpu', 'procs-mem', 'residents']) expect(screen.getByTestId(id)).toHaveTextContent(t('watch.host.offline_no_data'))
  })

  it('another host is another content', () => {
    const a = okHost({ procs: [proc(1, { name: 'alpha' })] }, 'h-a')
    const b = okHost({ procs: [proc(2, { name: 'beta' })] }, 'h-b')
    const { rerender } = render(<ProcTab host={a} now={NOW} />)
    expect(screen.getAllByText('alpha').length).toBeGreaterThan(0)
    rerender(<ProcTab host={b} now={NOW} />)
    expect(screen.queryByText('alpha')).toBeNull()
    expect(screen.getAllByText('beta').length).toBeGreaterThan(0)
  })
})
