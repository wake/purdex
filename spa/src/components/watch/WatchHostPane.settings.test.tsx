// MON-6f-1: what the host page hands the 設定 page — the pool host's own alias (not the fleet view's, which a daemon mapping can make differ), and the key of the page's memory for the drafts.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act, cleanup } from '@testing-library/react'

vi.mock('../../lib/pool/jobs-driver-live', () => ({ watchJobs: vi.fn(() => () => {}), jobsDaemons: () => [] }))
const settingsProps = vi.fn()
vi.mock('./WatchSettings', () => ({ WatchSettings: (p: unknown) => { settingsProps(p); return <div data-testid="mock-settings" /> } }))

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
const WWD_ID = '82b11c521df16c12'
// The fleet calls this host 'fleet-name'; the pool (wwd) calls it 'pool-name'. The daemon mapping (cfg-a is d-a's App host, and wwd's host is fronted by cfg-a) joins them.
const ph = (): PoolHost => ({ ...baseHost, alias: 'pool-name', host_id: WWD_ID, version: '1.0.0-alpha.755', reachable: true, facts: { ...darwin, alias: 'pool-name', at: Date.now() - 5000 } })

const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0) })

beforeEach(() => {
  cleanup()
  vi.useFakeTimers()
  settingsProps.mockReset()
  fetchPoolHosts.mockReset()
  fetchFactsHistory.mockReset()
  fetchFactsHistory.mockResolvedValue({ step: '5s', records: [] })
  clearZoneMemory(); fleetHistory.clear(); clearViewMemory()
  usePoolStore.getState().stop()
  usePoolStore.getState().reset()
  st().reset()
  const gen = st().begin('cfg-a')
  st().setSupport('cfg-a', gen, 'yes')
  st().applyFetched('cfg-a', gen, primaryAnswer(1, [host('d-a', { alias: 'fleet-name', fast: fast({ cpu: 1, zone: 'normal' }) })]))
  fetchPoolHosts.mockResolvedValue([ph()])
  useHostStore.setState({
    hosts: { 'cfg-a': { id: 'cfg-a', name: 'air26', ip: '1', port: 1, token: 't', order: 0, daemonId: 'd-a' } },
    hostOrder: ['cfg-a'], activeHostId: 'cfg-a', runtime: { 'cfg-a': { status: 'connected' } },
  } as never)
  useNexHostStore.setState({ byHost: { 'cfg-a': { phase: 'ready', fetchedAt: 1, daemonCapabilities: ['pool.v1'], wwd: { hostId: WWD_ID } } } } as never)
})
afterEach(() => { usePoolStore.getState().stop(); vi.useRealTimers() })

describe('the host page hands the 設定 page the pool\'s identity', () => {
  it('a pool tab: the pool host\'s alias (not the fleet view\'s) and the pool host_id as the key of the drafts', async () => {
    writeHostTab(WWD_ID, 'settings')
    render(<WatchHostPane pane={{ id: `watch-host-${WWD_ID}.p`, content: { kind: 'watch-host', poolHostId: WWD_ID } }} isActive />)
    await flush()
    expect(settingsProps).toHaveBeenCalled()
    const p = settingsProps.mock.calls.at(-1)![0] as { poolAlias?: string; hostId: string; hostConfigId?: string }
    expect(p.poolAlias).toBe('pool-name')
    expect(p.hostId).toBe(WWD_ID)
    expect(p.hostConfigId).toBe('cfg-a')
  })

  it('an older tab (a fleet id): keyed by that id, the alias is whatever the pool lists for the matched host', async () => {
    writeHostTab('d-a', 'settings')
    render(<WatchHostPane pane={{ id: 'watch-host-d-a.p', content: { kind: 'watch-host', fleetHostId: 'd-a' } }} isActive />)
    await flush()
    const p = settingsProps.mock.calls.at(-1)![0] as { hostId: string }
    expect(p.hostId).toBe('d-a')
  })
})
