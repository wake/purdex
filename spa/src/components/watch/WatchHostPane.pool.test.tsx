// 7a-4a: the host tab on the resource pool — header 「wwd 版本・N 秒前」, the 可接工作 row, gauges from the facts, process owners, 常駐程式.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, within, act, cleanup } from '@testing-library/react'
import { WatchHostPane } from './WatchHostPane'
import { useFleetStore } from '../../lib/fleet/store'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { usePoolStore } from '../../lib/pool/store'
import { PoolReadError } from '../../lib/pool/api'
import { clearZoneMemory, readHistoryLoad, readZones } from '../../lib/pool/history'
import { fleetHistory } from '../../lib/fleet/history'
import { clearViewMemory, writeRange } from '../../lib/watch/view-memory'
import { fast, host, primaryAnswer } from '../../lib/fleet/__fixtures__/builders'
import { parseFacts, parseHostsAnswer } from '../../lib/pool/parse'
import darwinJson from '../../lib/pool/__fixtures__/facts_darwin.json'
import hostsJson from '../../lib/pool/__fixtures__/pool_hosts_response.json'
import type { PoolFacts, PoolHost } from '../../lib/pool/types'
import type { FleetQueue } from '../../lib/fleet/types'

const fetchPoolHosts = vi.fn()
const fetchFactsHistory = vi.fn()
vi.mock('../../lib/pool/api', async (orig) => ({
  ...(await orig<typeof import('../../lib/pool/api')>()),
  fetchPoolHosts: (...a: unknown[]) => fetchPoolHosts(...a),
  fetchFactsHistory: (...a: unknown[]) => fetchFactsHistory(...a),
}))

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const st = () => useFleetStore.getState()
const darwin = parseFacts(darwinJson)!
const baseHost = parseHostsAnswer(hostsJson)![0]
const ph = (over: Partial<PoolHost> = {}, facts: Partial<PoolFacts> = {}): PoolHost => ({
  ...baseHost, alias: 'air26', host_id: 'd-a', version: '1.0.0-alpha.755', reachable: true,
  facts: { ...darwin, alias: 'air26', at: Date.now() - 5000, ...facts }, ...over,
})

const queue: FleetQueue = {
  heavy_used: 1, heavy_total: 2, light_used: 0, light_total: 3, holders: [{ class: 'heavy', slot: 1, command: 'go test', project: 'purdex', session: '', since: 1, pid: 9 }],
  waiters: [], longest_wait_s: 0, admission_held: false, admission_reason: '', heavy_paused: false, light_paused: false,
}

function load(hosts: ReturnType<typeof host>[]) {
  const gen = st().begin('cfg-a')
  st().setSupport('cfg-a', gen, 'yes')
  st().applyFetched('cfg-a', gen, primaryAnswer(1, hosts))
}
const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0) })
async function renderPane(fleetHostId = 'd-a') {
  render(<WatchHostPane pane={{ id: `watch-host-${fleetHostId}.p`, content: { kind: 'watch-host', fleetHostId } }} isActive />)
  await flush()
}

beforeEach(() => {
  cleanup()
  vi.useFakeTimers()
  fetchPoolHosts.mockReset()
  fetchFactsHistory.mockReset()
  fetchFactsHistory.mockResolvedValue({ step: '5s', records: [] })
  clearZoneMemory(); fleetHistory.clear(); clearViewMemory()
  usePoolStore.getState().stop()
  usePoolStore.getState().reset()
  st().reset()
  useHostStore.setState({
    hosts: { 'cfg-a': { id: 'cfg-a', name: 'air26', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd-a' } },
    hostOrder: ['cfg-a'], activeHostId: 'cfg-a', runtime: { 'cfg-a': { status: 'connected' } },
  } as never)
  useNexHostStore.setState({ byHost: { 'cfg-a': { phase: 'ready', daemonCapabilities: ['pool.v1'] } } } as never)
})
afterEach(() => { usePoolStore.getState().stop(); vi.useRealTimers() })

describe('WatchHostPane on the pool — a host wwd reads', () => {
  beforeEach(() => { load([host('d-a', { alias: 'air26', fast: fast({ cpu: 1, zone: 'normal' }) })]); fetchPoolHosts.mockResolvedValue([ph()]) })

  it('the header names wwd\'s version and how old the facts are, not the measuring host', async () => {
    await renderPane()
    const src = screen.getByTestId('measure-source').textContent ?? ''
    expect(src).toBe(t('watch.host.wwd_age', { version: '1.0.0-alpha.755', s: 5 }))
    expect(src).not.toContain(t('watch.host.measured', { alias: 'air26' }))
  })

  it('shows the 可接工作 row from the facts', async () => {
    await renderPane()
    const row = screen.getByTestId('avail-row')
    expect(within(row).getByTestId('avail-worker').textContent).toContain('heavy 0/1')
    // the fixture's two exceptions: one without an end is in force, the one that ended on 10-18 is over by the facts' time
    expect(within(row).getByTestId('avail-exception-dev-server')).toBeTruthy()
    expect(within(row).queryByTestId('avail-exception-app-dev-package')).toBeNull()
  })

  it('the gauges read the facts, not the fleet\'s older number', async () => {
    await renderPane()
    const cpu = Math.round(100 - darwin.cpu.idle_pct)
    expect(within(screen.getByTestId('gauge-cpu')).getByText(String(cpu))).toBeTruthy()
  })

  it('the process tables come from the facts, each with its owner', async () => {
    await renderPane()
    const cpu = screen.getByTestId('procs-cpu')
    expect(within(cpu).getByText('node')).toBeTruthy()
    expect(within(cpu).getByTestId('proc-owner-123').textContent).toContain('Job')
    expect(within(cpu).getByTestId('proc-owner-123').textContent).toContain('j_01JC')
    expect(within(cpu).getByTestId('proc-owner-1').textContent).toContain('System')
    expect(within(cpu).queryByTestId('proc-owner-999')).toBeNull()
  })

  it('codex and resident processes name their session, so several sessions can be told apart', async () => {
    const procs = [
      { pid: 41, name: 'codex', cpu_pct: 9, mem_mb: 300, mem_kind: 'rss', owner: { kind: 'codex', session: 's7' } },
      { pid: 42, name: 'tail', cpu_pct: 8, mem_mb: 20, mem_kind: 'rss', owner: { kind: 'resident', session: 's9' } },
      { pid: 43, name: 'cron', cpu_pct: 7, mem_mb: 10, mem_kind: 'rss', owner: { kind: 'system' } },
    ]
    fetchPoolHosts.mockResolvedValue([ph({}, { procs })])
    await renderPane()
    const cpu = screen.getByTestId('procs-cpu')
    expect(within(cpu).getByTestId('proc-owner-41').textContent).toBe('codex s7')
    expect(within(cpu).getByTestId('proc-owner-42').textContent).toBe('Resident s9')
    expect(within(cpu).getByTestId('proc-owner-43').textContent).toBe('System')
  })

  it('wwd reads the host: the fleet\'s older gate queue is not drawn next to the facts', async () => {
    load([host('d-a', { alias: 'air26', slow: { at: 1, top_cpu: [], top_mem: [], queue } })])
    await renderPane()
    expect(screen.queryByTestId('q-holder')).toBeNull()
    expect(screen.queryByText(t('watch.queue.title'))).toBeNull()
  })

  it('a memory kind this App does not know is not labelled RSS', async () => {
    const procs = [
      { pid: 51, name: 'a', cpu_pct: 9, mem_mb: 300, mem_kind: 'rss', owner: null },
      { pid: 52, name: 'b', cpu_pct: 8, mem_mb: 200, mem_kind: 'swap', owner: null },
      { pid: 53, name: 'c', cpu_pct: 7, mem_mb: 100, mem_kind: 'footprint', owner: null },
    ]
    fetchPoolHosts.mockResolvedValue([ph({}, { procs })])
    await renderPane()
    const rows = within(screen.getByTestId('procs-mem')).getAllByRole('row')
    const rss = (name: string) => within(rows.find((r) => r.textContent?.includes(name))!).queryByText('RSS')
    expect(rss('a')).not.toBeNull()
    expect(rss('b')).toBeNull()
    expect(rss('c')).toBeNull()
  })

  it('residents: shown, with no way to end them', async () => {
    await renderPane()
    const r = screen.getByTestId('residents')
    expect(within(r).getByTestId('resident-s1-codex-broker').textContent).toContain('codex-broker')
    expect(within(r).getByTestId('resident-s1-codex-broker').textContent).toContain('640')
    expect(within(r).queryByRole('button')).toBeNull()
  })

  it('a zone the fleet has no word for is shown as unknown', async () => {
    fetchPoolHosts.mockResolvedValue([ph({}, { derived: { ...darwin.derived, zone: 'unknown' } })])
    await renderPane()
    expect(screen.getByTestId('zone-text').textContent).toBe(t('fleet.zone.unknown'))
  })
})

describe('WatchHostPane on the pool — the 5 minute history', () => {
  const sample = (secAgo: number, zone: 'normal' | 'warning' | 'danger', idle: number): PoolFacts => ({ ...ph().facts!, at: Date.now() - secAgo * 1000, cpu: { ...darwin.cpu, idle_pct: idle }, derived: { ...darwin.derived, zone } })
  beforeEach(() => { load([host('d-a', { alias: 'air26' })]); fetchPoolHosts.mockResolvedValue([ph()]) })

  it('opening the tab reads the daemon\'s last 5 minutes once, and the zone band shows them', async () => {
    fetchFactsHistory.mockResolvedValue({ step: '5s', records: [sample(30, 'normal', 90), sample(25, 'warning', 20), sample(20, 'danger', 5)] })
    await renderPane()
    expect(fetchFactsHistory).toHaveBeenCalledTimes(1)
    expect(fetchFactsHistory.mock.calls[0].slice(0, 2)).toEqual(['cfg-a', 'air26'])
    expect(fetchFactsHistory.mock.calls[0][3]).toBe('5s')
    const zones = screen.getAllByTestId('zone-seg').map((z) => z.getAttribute('data-zone'))
    // the three read from the daemon, then the sample the page itself is showing
    expect(zones).toEqual(['normal', 'warning', 'danger', 'normal'])
    expect(screen.getByTestId('metric-cpu')).toBeTruthy()
  })

  it('leaving the tab while the history is still being read: what comes back is not written', async () => {
    let ok!: (v: unknown) => void
    fetchFactsHistory.mockReturnValue(new Promise((a) => { ok = a }))
    const view = render(<WatchHostPane pane={{ id: 'watch-host-d-a.p', content: { kind: 'watch-host', fleetHostId: 'd-a' } }} isActive />)
    await flush()
    expect(fetchFactsHistory).toHaveBeenCalledTimes(1)
    const before = fleetHistory.rawCount('d-a') // the fleet snapshot the test loaded is already in
    view.unmount()
    await act(async () => { ok({ step: '5s', records: [sample(30, 'danger', 5)] }); await vi.advanceTimersByTimeAsync(0) })
    expect(readHistoryLoad('d-a')).toBe('idle')
    expect(readZones('d-a').some((z) => z.zone === 'danger')).toBe(false)
    expect(fleetHistory.rawCount('d-a')).toBe(before)
  })

  it('host_not_local: the gauges stay, the chart says it cannot be read through this daemon', async () => {
    fetchFactsHistory.mockRejectedValue(new PoolReadError('/x', 501, { code: 'host_not_local', action: 'none' } as never))
    await renderPane()
    expect(screen.getByTestId('history-not-local').textContent).toBe(t('watch.host.history_not_local'))
    expect(screen.getByTestId('gauge-cpu')).toBeTruthy()
    expect(screen.queryByTestId('metric-cpu')).toBeNull()
    expect(screen.queryByTestId('zone-band')).toBeNull()
  })

  it('any other failure leaves the charts as the App collected them, with no note', async () => {
    fetchFactsHistory.mockRejectedValue(new Error('boom'))
    await renderPane()
    expect(screen.queryByTestId('history-not-local')).toBeNull()
    expect(screen.getByTestId('metric-cpu')).toBeTruthy()
  })

  it('the longer ranges keep the App\'s own history: nothing is read, no band, no note', async () => {
    writeRange('d-a', '1h')
    await renderPane()
    expect(fetchFactsHistory).not.toHaveBeenCalled()
    expect(screen.queryByTestId('zone-band')).toBeNull()
    expect(screen.getByTestId('metric-cpu')).toBeTruthy()
  })

  it('a host wwd does not read, or no pool at all: nothing is read', async () => {
    fetchPoolHosts.mockResolvedValue([])
    await renderPane()
    expect(fetchFactsHistory).not.toHaveBeenCalled()
  })
})

describe('WatchHostPane on the pool — a host wwd does not read', () => {
  it('keeps "measured by", says the state is unknown (the pool does not list it; no fleet fallback, MON-1d), and keeps the fleet\'s process tables', async () => {
    load([host('d-a', { alias: 'air26' })])
    fetchPoolHosts.mockResolvedValue([])
    await renderPane()
    expect(screen.getByTestId('measure-source').textContent).toContain(t('watch.host.measured', { alias: 'air26' }))
    const row = screen.getByTestId('avail-row').textContent!
    expect(row).toContain('State unknown')
    expect(row).not.toContain('unreachable')
    expect(screen.queryByTestId('residents')).toBeNull()
  })
})

describe('WatchHostPane without the pool', () => {
  it('no daemon announces pool.v1: the tab as it was', async () => {
    useNexHostStore.setState({ byHost: { 'cfg-a': { phase: 'ready', daemonCapabilities: [] } } } as never)
    load([host('d-a', { alias: 'air26' })])
    await renderPane()
    expect(screen.queryByTestId('avail-row')).toBeNull()
    expect(screen.queryByTestId('residents')).toBeNull()
    expect(screen.getByTestId('measure-source').textContent).toContain(t('watch.host.measured', { alias: 'air26' }))
    expect(fetchFactsHistory).not.toHaveBeenCalled()
  })
})
