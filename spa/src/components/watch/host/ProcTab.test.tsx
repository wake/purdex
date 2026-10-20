// spa/src/components/watch/host/ProcTab.test.tsx — the host page's 行程 tab (monitoring tabs spec M45; plan MON-8a): two process tables (CPU top 12, memory top 12) and the resident programs, from the facts of a host that is ok; a host that is not ok says it is offline.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, cleanup, fireEvent, act } from '@testing-library/react'

const fetchFactsHistory = vi.fn()
vi.mock('../../../lib/pool/api', async (orig) => ({ ...(await orig<typeof import('../../../lib/pool/api')>()), fetchFactsHistory: (...a: unknown[]) => fetchFactsHistory(...a) }))
import { ProcTab } from './ProcTab'
import { clearProcHistory } from '../../../lib/pool/proc-history'
import { clearViewMemory, readProcAt, writeProcAt } from '../../../lib/watch/view-memory'
import { usePoolStore } from '../../../lib/pool/store'
import { PoolReadError } from '../../../lib/pool/api'
import { clock } from '../../../lib/pool/words'
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

beforeEach(() => {
  cleanup(); fetchFactsHistory.mockReset(); clearProcHistory(); clearViewMemory()
  usePoolStore.setState({ sourceHostId: 'cfg-a' })
})

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

describe('the timeline (MON-8b)', () => {
  /** A record 10 minutes back and the one before it, so the nearest one is clear. */
  const past = [{ ...darwin, at: NOW - 615_000, procs: [proc(76, { name: 'older' })] }, { ...darwin, at: NOW - 600_000, procs: [proc(77, { name: 'tenago', cpu_pct: 55 })] }]
  const move = async (to: number) => { await act(async () => { fireEvent.change(screen.getByTestId('proc-timeline'), { target: { value: String(to) } }) }) }

  it('reads nothing until the timeline is moved, then reads the hour and shows that record in both tables', async () => {
    fetchFactsHistory.mockResolvedValue({ step: '5s', records: past })
    render(<ProcTab host={okHost({ procs: [proc(1, { name: 'livenow' })] })} now={NOW} />)
    expect(fetchFactsHistory).not.toHaveBeenCalled()
    expect((screen.getByTestId('proc-timeline') as HTMLInputElement).value).toBe('240')
    await move(200) // 40 steps of 15 s back = 10 minutes
    expect(fetchFactsHistory).toHaveBeenCalledTimes(1)
    expect(within(screen.getByTestId('procs-cpu')).getByText('tenago')).toBeInTheDocument()
    expect(within(screen.getByTestId('procs-mem')).getByText('tenago')).toBeInTheDocument()
    expect(screen.queryByText('livenow')).toBeNull()
    expect(screen.getByTestId('proc-note')).toHaveTextContent(t('watch.host.proc.at', { time: clock(NOW - 600_000) }))
    expect(readProcAt('h-mlab')).toBe(NOW - 600_000)
  })

  it('back to now: the live facts again, and the pick is forgotten', async () => {
    fetchFactsHistory.mockResolvedValue({ step: '5s', records: past })
    render(<ProcTab host={okHost({ procs: [proc(1, { name: 'livenow' })] })} now={NOW} />)
    await move(200)
    fireEvent.click(screen.getByTestId('proc-now'))
    expect(within(screen.getByTestId('procs-cpu')).getByText('livenow')).toBeInTheDocument()
    expect((screen.getByTestId('proc-timeline') as HTMLInputElement).value).toBe('240')
    expect(readProcAt('h-mlab')).toBeNull()
    expect(screen.getByTestId('proc-now')).toBeDisabled()
  })

  it('says it is reading, and says so when the daemon cannot read the host\'s history or the read fails', async () => {
    let answer: (v: unknown) => void = () => {}
    fetchFactsHistory.mockReturnValueOnce(new Promise((r) => { answer = r }))
    render(<ProcTab host={okHost()} now={NOW} />)
    await move(200)
    expect(screen.getByTestId('proc-note')).toHaveTextContent(t('watch.host.proc.loading'))
    await act(async () => { answer({ step: '5s', records: [] }) })
    cleanup(); clearProcHistory(); clearViewMemory()
    fetchFactsHistory.mockRejectedValueOnce(new PoolReadError('/api/pool/facts/history', 409, { code: 'host_not_local', message: '' } as never))
    render(<ProcTab host={okHost()} now={NOW} />)
    await move(200)
    expect(screen.getByTestId('proc-note')).toHaveTextContent(t('watch.host.proc.not_local'))
    cleanup(); clearProcHistory(); clearViewMemory()
    fetchFactsHistory.mockRejectedValueOnce(new Error('boom'))
    render(<ProcTab host={okHost()} now={NOW} />)
    await move(200)
    expect(screen.getByTestId('proc-note')).toHaveTextContent(t('watch.host.proc.error'))
  })

  it('a host that is not ok has the timeline off', () => {
    render(<ProcTab host={downHost('host_down')} now={NOW} />)
    expect(screen.getByTestId('proc-timeline')).toBeDisabled()
  })

  it('a pick is dropped while the host is not ok and does not come back when it is (codex R1)', async () => {
    fetchFactsHistory.mockResolvedValue({ step: '5s', records: past })
    const live = okHost({ procs: [proc(1, { name: 'livenow' })] })
    const { rerender } = render(<ProcTab host={live} now={NOW} />)
    await move(200)
    expect(readProcAt('h-mlab')).toBe(NOW - 600_000)
    rerender(<ProcTab host={downHost('host_down')} now={NOW} />)
    expect(readProcAt('h-mlab')).toBeNull()
    rerender(<ProcTab host={live} now={NOW} />)
    expect(within(screen.getByTestId('procs-cpu')).getByText('livenow')).toBeInTheDocument()
    expect((screen.getByTestId('proc-timeline') as HTMLInputElement).value).toBe('240')
  })

  it('a gap in the hour: a pick far from every record says there is none, it does not show the far one (codex attack)', async () => {
    fetchFactsHistory.mockResolvedValue({ step: '5s', records: [{ ...darwin, at: NOW - 3000_000, procs: [proc(5, { name: 'faraway' })] }] })
    render(<ProcTab host={okHost({ procs: [proc(1, { name: 'livenow' })] })} now={NOW} />)
    await move(200) // 10 minutes back; the only record is 50 minutes back
    expect(screen.getByTestId('proc-note')).toHaveTextContent(t('watch.host.proc.no_record'))
    expect(screen.queryByText('faraway')).toBeNull()
    expect(screen.queryByText('livenow')).toBeNull()
  })

  it('a pick that has slid out of the hour is the live record again', () => {
    writeProcAt('h-mlab', NOW - 600_000)
    render(<ProcTab host={okHost({ procs: [proc(1, { name: 'livenow' })] })} now={NOW + 3 * 3600_000} />)
    expect(within(screen.getByTestId('procs-cpu')).getByText('livenow')).toBeInTheDocument()
  })
})
