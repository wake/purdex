// spa/src/components/watch/host/MonitorTab.test.tsx — the host page's 監控 tab (monitoring tabs spec M41 1–3, M42, M22, #3785): a row of six gauges, six mini charts that share one hovered time, and one block of
// jobs | CPU top 5 | memory top 5. The samples are `useHostSeries`'s (mocked here to fixed ones, so the range it was asked for can be checked); the hosts are the pool's.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react'

const release = vi.fn()
vi.mock('../../../lib/pool/jobs-driver-live', () => ({ watchJobs: vi.fn(() => release), jobsDaemons: () => [] }))
vi.mock('../../../features/workspace/lib/open-watch-area', () => ({ openWatchArea: vi.fn() }))
const seriesFor = vi.fn()
vi.mock('../../../lib/pool/use-host-series', () => ({ useHostSeries: (...a: unknown[]) => seriesFor(...a) }))

import { MonitorTab } from './MonitorTab'
import { useJobsStore } from '../../../lib/pool/jobs-store'
import { usePoolStore } from '../../../lib/pool/store'
import { poolHostState } from '../../../lib/pool/host-state'
import { parseFacts, parseHostsAnswer } from '../../../lib/pool/parse'
import { GAUGES, gaugesOf, miniChartsOf } from '../../../lib/pool/host-charts'
import { RANGE_MS, type HostRange, type HostSample } from '../../../lib/pool/host-history'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import type { PoolFacts, PoolHost, PoolProc } from '../../../lib/pool/types'
import type { PoolJob } from '../../../lib/pool/jobs-types'
import darwinJson from '../../../lib/pool/__fixtures__/facts_darwin.json'
import hostsJson from '../../../lib/pool/__fixtures__/pool_hosts_response.json'
import { useI18nStore } from '../../../stores/useI18nStore'
import { openWatchArea } from '../../../features/workspace/lib/open-watch-area'
import { useAlertsStore } from '../../../lib/pool/alerts-store'
import type { PoolAlert } from '../../../lib/pool/alerts-api'
import { readEventFilters, clearViewMemory } from '../../../lib/watch/view-memory'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const darwin = parseFacts(darwinJson)!
const NOW = darwin.at + 5000
const base = parseHostsAnswer(hostsJson)![0]
const poolHost = (over: Partial<PoolHost> = {}): PoolHost => ({ ...base, alias: 'mlab', host_id: 'h-mlab', facts: darwin, reachable: true, ...over })
const proc = (pid: number, over: Partial<PoolProc> = {}): PoolProc => ({ pid, name: `p${pid}`, cpu_pct: 1, mem_mb: 100, mem_kind: 'footprint', owner: null, ...over })

function hostOf(ph: PoolHost, lastFactsAt: number | null = null): MonitorHost {
  return { hostId: ph.host_id, alias: ph.alias, daemonHostId: null, state: poolHostState(ph, lastFactsAt, NOW), look: {} as never, poolFailure: null, count: null, local: false }
}
const okHost = (facts: PoolFacts = darwin) => hostOf(poolHost({ facts }))
const downHost = (liveness: 'wwd_down' | 'host_down' | 'unknown', lastFactsAt: number | null = NOW - 90_000) =>
  hostOf(poolHost({ liveness, liveness_at: NOW - 3000, liveness_since: NOW - 600_000, facts: null }), lastFactsAt)

const sample = (at: number, over: Partial<HostSample> = {}): HostSample => ({
  at, cpuPct: 50, load1: 2, load5: 1, ncpu: 8, memUsedMb: 8192, memAvailMb: 8192, memTotalMb: 16384, pageOut: 5, swapOut: 1, swapUsedMb: 512, diskUsedPct: 50, diskFreeGb: 100,
  runHeavy: 1, runLight: 0, slotHeavy: 2, slotLight: 4, workerOn: true, zone: 'normal', ...over,
})
/** Five samples a minute apart, ending at NOW, cpu 10..50. */
const SAMPLES = Array.from({ length: 5 }, (_, i) => sample(NOW - (4 - i) * 60_000, { cpuPct: 10 * (i + 1) }))

function mount(host: MonitorHost, range: HostRange = '5m') {
  return render(<MonitorTab host={host} range={range} now={NOW} />)
}
const job = (id: string, over: Partial<PoolJob> = {}): PoolJob => ({
  id, origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: `task-${id}`, args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: '', ref: '', team: '', host: '' }, created_at: NOW - 1000, state: 'queued',
  claim: null, result: null, manual: '', manual_seq: 0, ...over,
})

beforeEach(() => {
  cleanup()
  seriesFor.mockReset()
  seriesFor.mockReturnValue({ samples: SAMPLES, load: { state: 'ok' }, rev: 0 })
  useJobsStore.getState().reset()
  usePoolStore.getState().reset()
  usePoolStore.setState({ hosts: [poolHost()] })
  clearViewMemory()
})
afterEach(() => cleanup())

describe('the gauges (M41-1)', () => {
  it('six, in order, each drawing what gaugesOf says of the latest facts', () => {
    mount(okHost())
    const want = gaugesOf(darwin, t)
    GAUGES.forEach((g, i) => {
      const el = screen.getByTestId(`monitor-gauge-${g.id}`)
      expect(el).toHaveTextContent(t(g.labelKey))
      expect(el).toHaveAttribute('data-tone', want[i] ? want[i]!.tone : 'nodata')
      if (want[i]) expect(el).toHaveTextContent(want[i]!.value)
    })
    expect(screen.getAllByTestId(/^monitor-gauge-/)).toHaveLength(6)
  })
})

describe('the six charts (M41-2)', () => {
  it('in the order of miniChartsOf, three to a row', () => {
    mount(okHost())
    const ids = within(screen.getByTestId('monitor-charts')).getAllByTestId('mini-chart').map((e) => e.getAttribute('data-chart'))
    expect(ids).toEqual(miniChartsOf(SAMPLES, darwin, '5m').map((d) => d.id))
    expect(ids).toHaveLength(6)
    expect(screen.getByTestId('monitor-charts').className).toContain('grid-cols-3')
  })

  it('the samples are asked for with the host, the range and the live facts, and drawn with that same range', () => {
    mount(okHost(), '1h')
    expect(seriesFor).toHaveBeenCalledWith('h-mlab', 'mlab', '1h', darwin)
    // the time axis of a card is the range's: the first tick is one range before now
    const first = within(screen.getAllByTestId('mini-chart')[0]).getByTestId('mini-xtick-0').textContent
    const d = new Date(NOW - RANGE_MS['1h'])
    expect(first).toBe(`${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`)
  })

  it('one hovered time for all six (M42): pointing at one card puts the line on every card', () => {
    mount(okHost())
    expect(screen.queryAllByTestId('mini-hover-line')).toHaveLength(0)
    const plots = screen.getAllByTestId('mini-plot')
    plots.forEach((p) => { p.getBoundingClientRect = () => ({ left: 0, top: 0, width: 600, height: 90, right: 600, bottom: 90, x: 0, y: 0, toJSON: () => ({}) }) })
    fireEvent.mouseMove(plots[2], { clientX: 600 })
    expect(screen.getAllByTestId('mini-hover-line')).toHaveLength(6)
    fireEvent.mouseLeave(plots[2])
    expect(screen.queryAllByTestId('mini-hover-line')).toHaveLength(0)
  })

  it('a new range drops the hovered time', () => {
    const { rerender } = mount(okHost())
    const plots = screen.getAllByTestId('mini-plot')
    plots.forEach((p) => { p.getBoundingClientRect = () => ({ left: 0, top: 0, width: 600, height: 90, right: 600, bottom: 90, x: 0, y: 0, toJSON: () => ({}) }) })
    fireEvent.mouseMove(plots[0], { clientX: 600 })
    expect(screen.getAllByTestId('mini-hover-line')).toHaveLength(6)
    rerender(<MonitorTab host={okHost()} range="15m" now={NOW} />)
    expect(screen.queryAllByTestId('mini-hover-line')).toHaveLength(0)
  })

  const stub = () => screen.getAllByTestId('mini-plot').forEach((p) => { p.getBoundingClientRect = () => ({ left: 0, top: 0, width: 600, height: 90, right: 600, bottom: 90, x: 0, y: 0, toJSON: () => ({}) }) })

  it('going to another range and back does not bring the old hovered time back', () => {
    const { rerender } = mount(okHost())
    stub()
    fireEvent.mouseMove(screen.getAllByTestId('mini-plot')[0], { clientX: 600 })
    expect(screen.getAllByTestId('mini-hover-line')).toHaveLength(6)
    rerender(<MonitorTab host={okHost()} range="15m" now={NOW} />)
    rerender(<MonitorTab host={okHost()} range="5m" now={NOW} />)
    expect(screen.queryAllByTestId('mini-hover-line')).toHaveLength(0)
  })

  it('another host on the same range does not inherit the hovered time', () => {
    const { rerender } = mount(okHost())
    stub()
    fireEvent.mouseMove(screen.getAllByTestId('mini-plot')[0], { clientX: 600 })
    expect(screen.getAllByTestId('mini-hover-line')).toHaveLength(6)
    rerender(<MonitorTab host={hostOf(poolHost({ host_id: 'h-other', alias: 'other' }))} range="5m" now={NOW} />)
    expect(screen.queryAllByTestId('mini-hover-line')).toHaveLength(0)
  })

  it('the hovered time is a time, not a place in the list: when the oldest sample leaves the window it stays on the same time', () => {
    const { rerender } = mount(okHost())
    stub()
    fireEvent.mouseMove(screen.getAllByTestId('mini-plot')[0], { clientX: 600 })
    const label = () => screen.getAllByTestId('mini-hover-time')[0].textContent
    const before = label()
    seriesFor.mockReturnValue({ samples: SAMPLES.slice(1), load: { state: 'ok' }, rev: 1 })
    rerender(<MonitorTab host={okHost()} range="5m" now={NOW} />)
    expect(label()).toBe(before)
  })

  it('when the hovered sample is gone from the window, nothing is hovered', () => {
    const { rerender } = mount(okHost())
    stub()
    fireEvent.mouseMove(screen.getAllByTestId('mini-plot')[0], { clientX: 600 })
    seriesFor.mockReturnValue({ samples: SAMPLES.slice(0, 3), load: { state: 'ok' }, rev: 1 })
    rerender(<MonitorTab host={okHost()} range="5m" now={NOW} />)
    expect(screen.queryAllByTestId('mini-hover-line')).toHaveLength(0)
  })

  it('a host that is ok draws no slanted area', () => {
    mount(okHost())
    expect(screen.queryAllByTestId('mini-dead')).toHaveLength(0)
  })
})

describe('a host that is not ok (M22, #3785)', () => {
  it.each([['wwd_down', 'warning'], ['host_down', 'error'], ['unknown', 'muted']] as const)('%s: six dashes, and the charts stop at the last reading and run slanted in %s', (liveness, tone) => {
    mount(downHost(liveness))
    for (const g of GAUGES) {
      expect(screen.getByTestId(`monitor-gauge-${g.id}`)).toHaveAttribute('data-tone', 'nodata')
      expect(screen.getByTestId(`monitor-gauge-${g.id}`)).toHaveTextContent('—')
    }
    const dead = screen.getAllByTestId('mini-dead')
    expect(dead).toHaveLength(6)
    dead.forEach((d) => expect(d).toHaveAttribute('data-tone', tone))
    expect(seriesFor).toHaveBeenCalledWith('h-mlab', 'mlab', '5m', null)
  })

  it('the slanted area starts where the last facts were read', () => {
    mount(downHost('wwd_down', NOW - 90_000))
    const pct = Number(screen.getAllByTestId('mini-dead')[0].getAttribute('data-from-pct'))
    expect(pct).toBeCloseTo(((RANGE_MS['5m'] - 90_000) / RANGE_MS['5m']) * 100, 1)
  })

  it('a host never read: the whole plot is slanted', () => {
    mount(downHost('unknown', null))
    expect(Number(screen.getAllByTestId('mini-dead')[0].getAttribute('data-from-pct'))).toBe(0)
  })

  it('both process tables say it is offline and list nothing, whatever the fleet may still hold', () => {
    mount(downHost('host_down'))
    for (const id of ['procs-cpu', 'procs-mem']) {
      expect(screen.getByTestId(id)).toHaveTextContent(t('watch.host.offline_no_data'))
      expect(within(screen.getByTestId(id)).queryAllByRole('row')).toHaveLength(2) // the header and the one message row
    }
  })
})

describe('jobs | CPU top 5 | memory top 5 (M41-3)', () => {
  const procs = [
    proc(1, { cpu_pct: 5, mem_mb: 900 }), proc(2, { cpu_pct: 90, mem_mb: 100 }), proc(3, { cpu_pct: 50, mem_mb: 300 }), proc(4, { cpu_pct: 10, mem_mb: 800 }),
    proc(5, { cpu_pct: 40, mem_mb: 700 }), proc(6, { cpu_pct: 30, mem_mb: 600 }), proc(7, { cpu_pct: 20, mem_mb: 500, owner: { kind: 'job', id: 'j_9', session: '' } }),
  ]
  const withProcs = () => okHost({ ...darwin, procs })
  const pids = (id: string) => within(screen.getByTestId(id)).getAllByRole('row').slice(1).map((r) => r.querySelector('td')?.textContent)

  it('the top five of each, by CPU and by memory', () => {
    mount(withProcs())
    expect(pids('procs-cpu')).toEqual(['2', '3', '5', '6', '7'])
    expect(pids('procs-mem')).toEqual(['1', '4', '5', '6', '7'])
  })

  it('a process shows who owns it', () => {
    mount(withProcs())
    expect(within(screen.getByTestId('procs-cpu')).getByTestId('proc-owner-7')).toHaveTextContent(t('watch.host.owner.job'))
  })

  it('left, middle, right: the jobs column, then CPU, then memory', () => {
    mount(withProcs())
    const cols = within(screen.getByTestId('monitor-work')).getByTestId('monitor-work-cols')
    const order = Array.from(cols.children).map((c) => (c.getAttribute('data-testid') ?? c.firstElementChild?.getAttribute('data-testid')))
    expect(order).toEqual(['host-jobs', 'procs-cpu', 'procs-mem'])
  })

  it('the jobs column is this host\'s', () => {
    const j = job('q1')
    useJobsStore.setState({ byOrigin: { mlab: { jobs: { q1: j }, cursor: '1', epoch: 'e', via: 'v' } } })
    mount(withProcs())
    expect(within(screen.getByTestId('host-jobs')).getByTestId('host-jobs-queue-row')).toHaveAttribute('data-job-id', 'q1')
  })

  it('says how often the processes are read', () => {
    mount(withProcs())
    expect(screen.getByTestId('monitor-work')).toHaveTextContent(t('watch.host.monitor.work'))
    expect(screen.getByTestId('monitor-work')).toHaveTextContent(t('watch.host.monitor.procs_every'))
  })
})

describe('the alert badge (M41-1, MON-10c)', () => {
  const alertOf = (over: Partial<PoolAlert> & { key: string }): PoolAlert => ({ kind: 'orphans', level: 'digest', since: NOW - 60_000, last: NOW, clearedAt: null, detail: over.key, ...over })
  const put = (alias: string, alerts: PoolAlert[]) => useAlertsStore.setState((s) => ({ byHost: { ...s.byHost, [alias]: { kind: 'ok', known: true, alerts, at: NOW } } }))
  beforeEach(() => { useAlertsStore.setState({ byHost: {}, source: null }); vi.mocked(openWatchArea).mockClear() })

  it('is not there when the host has no alert in progress', () => {
    put('mlab', [alertOf({ key: 'old', clearedAt: NOW - 1000 })])
    mount(okHost())
    expect(screen.queryByTestId('monitor-alerts')).toBeNull()
  })

  it('says how many are in progress, at the right end of the gauge row, and lists their titles', () => {
    put('mlab', [alertOf({ key: 'a', detail: '孤兒行程' }), alertOf({ key: 'b', detail: '磁碟快滿' }), alertOf({ key: 'c', clearedAt: NOW - 1000 })])
    mount(okHost())
    const badge = within(screen.getByTestId('monitor-gauges')).getByTestId('monitor-alerts')
    expect(badge).toHaveTextContent(t('watch.host.monitor.alerts', { n: 2 }))
    expect(badge.getAttribute('title')).toContain('孤兒行程')
    expect(badge.getAttribute('title')).toContain('磁碟快滿')
    expect(screen.getByTestId('monitor-gauges').lastElementChild).toBe(badge)
    expect(badge).toHaveClass('text-status-warning-text')
  })

  it('counts a watch-level alert in progress, and stays yellow without a push one', () => {
    put('mlab', [alertOf({ key: 'a', level: 'watch', kind: 'pressure_watch' })])
    mount(okHost())
    expect(screen.getByTestId('monitor-alerts')).toHaveTextContent(t('watch.host.monitor.alerts', { n: 1 }))
    expect(screen.getByTestId('monitor-alerts')).toHaveClass('text-status-warning-text')
  })

  it('is red when one of them is push level', () => {
    put('mlab', [alertOf({ key: 'a' }), alertOf({ key: 'b', level: 'push' })])
    mount(okHost())
    expect(screen.getByTestId('monitor-alerts')).toHaveClass('text-status-error-text')
  })

  it('counts a silence about this host that other hosts\' lists hold, once (the events tab\'s attribution)', () => {
    usePoolStore.setState({ hosts: [poolHost(), poolHost({ alias: 'mserv', host_id: 'h-mserv' }), poolHost({ alias: 'air26', host_id: 'h-air26' })] })
    const s = alertOf({ key: 'silence:mlab', kind: 'silence', silence: 'host_down', level: 'push' })
    put('mserv', [s])
    put('air26', [s])
    mount(okHost())
    expect(screen.getByTestId('monitor-alerts')).toHaveTextContent(t('watch.host.monitor.alerts', { n: 1 }))
  })

  it('does not count an alert that belongs to another host', () => {
    usePoolStore.setState({ hosts: [poolHost(), poolHost({ alias: 'mserv', host_id: 'h-mserv' })] })
    put('mserv', [alertOf({ key: 'orphans' })])
    mount(okHost())
    expect(screen.queryByTestId('monitor-alerts')).toBeNull()
  })

  it('opens the events tab filtered to this host when pressed', () => {
    put('mlab', [alertOf({ key: 'a' })])
    mount(okHost())
    fireEvent.click(screen.getByTestId('monitor-alerts'))
    expect(readEventFilters()).toEqual({ host: 'h-mlab', source: 'all', status: 'all' })
    expect(openWatchArea).toHaveBeenCalledWith({ tab: 'events' })
  })
})
