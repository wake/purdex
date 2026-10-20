// 7b1-4: the host tab's 工作 section is between 資源 and 行程, for a host wwd reads; not for one it does not, and not on the settings page.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act, cleanup } from '@testing-library/react'

vi.mock('../../lib/pool/jobs-driver-live', () => ({ watchJobs: vi.fn(() => () => {}), jobsDaemons: () => [] }))

import { WatchHostPane } from './WatchHostPane'
import { useFleetStore } from '../../lib/fleet/store'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { usePoolStore } from '../../lib/pool/store'
import { clearZoneMemory } from '../../lib/pool/history'
import { fleetHistory } from '../../lib/fleet/history'
import { clearViewMemory, writeHostTab } from '../../lib/watch/view-memory'
import { fast, host, primaryAnswer } from '../../lib/fleet/__fixtures__/builders'
import { parseFacts, parseHostsAnswer } from '../../lib/pool/parse'
import darwinJson from '../../lib/pool/__fixtures__/facts_darwin.json'
import hostsJson from '../../lib/pool/__fixtures__/pool_hosts_response.json'
import type { PoolHost } from '../../lib/pool/types'

const fetchPoolHosts = vi.fn()
const fetchFactsHistory = vi.fn()
vi.mock('../../lib/pool/api', async (orig) => ({
  ...(await orig<typeof import('../../lib/pool/api')>()),
  fetchPoolHosts: (...a: unknown[]) => fetchPoolHosts(...a),
  fetchFactsHistory: (...a: unknown[]) => fetchFactsHistory(...a),
}))

const st = () => useFleetStore.getState()
const darwin = parseFacts(darwinJson)!
const baseHost = parseHostsAnswer(hostsJson)![0]
const ph = (): PoolHost => ({ ...baseHost, alias: 'air26', host_id: 'd-a', version: '1.0.0-alpha.755', reachable: true, facts: { ...darwin, alias: 'air26', at: Date.now() - 5000 } })

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

describe('WatchHostPane — the 工作 section', () => {
  it('a host wwd reads has it, after 資源 and before 行程', async () => {
    load([host('d-a', { alias: 'air26', fast: fast({ cpu: 1, zone: 'normal' }) })]); fetchPoolHosts.mockResolvedValue([ph()])
    await renderPane()
    const jobs = screen.getByTestId('host-jobs')
    const procs = screen.getByTestId('procs-cpu')
    expect(jobs.compareDocumentPosition(procs) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    // nothing runs here, so the running list is not drawn (MON-6e-2)
    expect(screen.queryByTestId('host-jobs-running')).toBeNull()
    expect(screen.getByTestId('host-jobs-queued')).toBeInTheDocument()
    expect(screen.getByTestId('host-jobs-ended')).toBeInTheDocument()
  })

  it('a host wwd does not read has none', async () => {
    load([host('d-a', { alias: 'air26', fast: fast({ cpu: 1, zone: 'normal' }) })]); fetchPoolHosts.mockResolvedValue([])
    await renderPane()
    expect(screen.queryByTestId('host-jobs')).toBeNull()
  })

  it('the settings page has none', async () => {
    load([host('d-a', { alias: 'air26', fast: fast({ cpu: 1, zone: 'normal' }) })]); fetchPoolHosts.mockResolvedValue([ph()])
    writeHostTab('d-a', 'settings')
    await renderPane()
    expect(screen.queryByTestId('host-jobs')).toBeNull()
  })
})
