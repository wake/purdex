import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, act, within, fireEvent } from '@testing-library/react'
import { FleetCells } from './FleetCells'
import { useFleetStore } from '../../lib/fleet/store'
import { fleetHistory } from '../../lib/fleet/history'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { usePoolStore } from '../../lib/pool/store'
import { fast, host, primaryAnswer } from '../../lib/fleet/__fixtures__/builders'
import { parseFacts, parseHostsAnswer } from '../../lib/pool/parse'
import darwinJson from '../../lib/pool/__fixtures__/facts_darwin.json'
import hostsJson from '../../lib/pool/__fixtures__/pool_hosts_response.json'
import type { PoolHost } from '../../lib/pool/types'

vi.mock('../../features/workspace/lib/open-watch-area', () => ({ openWatchArea: vi.fn() }))
const fetchPoolHosts = vi.fn()
vi.mock('../../lib/pool/api', () => ({ fetchPoolHosts: (...a: unknown[]) => fetchPoolHosts(...a) }))

const A = 'cfg-a'
const st = () => useFleetStore.getState()
const hostCfg = (id: string, daemonId: string) => ({ id, name: 'mlab', ip: '100.64.0.2', port: 7860, token: 't', order: 0, daemonId, colors: { console: { main: { color: '#336699', alpha: 100 } } } })
const darwin = parseFacts(darwinJson)!
const mlab = (over: Partial<PoolHost> = {}): PoolHost => ({ ...parseHostsAnswer(hostsJson)![0], alias: 'mlab', host_id: 'h-mlab', facts: { ...darwin, at: Date.now() }, reachable: true, ...over })

function answer() {
  const gen = st().begin(A)
  st().setSupport(A, gen, 'yes')
  st().applyFetched(A, gen, primaryAnswer(1, [{ ...host('d-a', { name: 'mlab', fast: fast({ load: 30 }) }), alias: 'mlab' }]))
}

beforeEach(() => {
  vi.useFakeTimers()
  useUISettingsStore.setState(useUISettingsStore.getInitialState())
  st().reset()
  fleetHistory.clear()
  usePoolStore.getState().stop()
  usePoolStore.getState().reset()
  fetchPoolHosts.mockReset()
  useHostStore.setState({ hosts: { [A]: hostCfg(A, 'd-a') }, hostOrder: [A], activeHostId: A, runtime: { [A]: { status: 'connected' } } } as never)
  useNexHostStore.setState({ byHost: {} } as never)
})
afterEach(() => { usePoolStore.getState().stop(); vi.useRealTimers() })

const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0) })

describe('FleetCells — the capability gate', () => {
  it('no daemon announces pool.v1: the cells are the fleet\'s, nothing is read, and the waiting-primary notice stays', async () => {
    answer()
    render(<FleetCells variant="wide" />)
    await flush()
    expect(fetchPoolHosts).not.toHaveBeenCalled()
    expect(screen.getByTestId('load-ring-d-a').dataset.zone).toBe('normal')
    expect(screen.queryByTestId('worker-running-d-a')).toBeNull()
  })

  it('the pool announced but has not answered yet: still the fleet\'s', async () => {
    useNexHostStore.setState({ byHost: { [A]: { phase: 'ready', daemonCapabilities: ['pool.v1'] } } } as never)
    fetchPoolHosts.mockReturnValue(new Promise(() => {}))
    answer()
    render(<FleetCells variant="wide" />)
    await flush()
    expect(fetchPoolHosts).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('worker-running-d-a')).toBeNull()
    expect(screen.getByTestId('load-ring-d-a').dataset.zone).toBe('normal')
  })
})

describe('FleetCells — on the pool', () => {
  beforeEach(() => { useNexHostStore.setState({ byHost: { [A]: { phase: 'ready', daemonCapabilities: ['pool.v1'] } } } as never) })

  it('a host the pool lists is drawn from its facts: the running dot appears, and the queued jobs add no number', async () => {
    fetchPoolHosts.mockResolvedValue([mlab({ facts: { ...darwin, at: Date.now(), queue: { queued: 2 }, worker: { ...darwin.worker, running: { heavy: 0, light: 1 } } } })])
    answer()
    render(<FleetCells variant="wide" />)
    await flush()
    expect(screen.getByTestId('worker-running-d-a')).toBeTruthy()
    expect(screen.queryByTestId('queue-badge-d-a')).toBeNull()
  })

  it('an empty list from the pool is an answer: the fleet host is drawn as a fallback, with the pool part, not as before', async () => {
    fetchPoolHosts.mockResolvedValue([])
    answer()
    render(<FleetCells variant="wide" />)
    await flush()
    expect(screen.getByTestId('load-ring-d-a').dataset.zone).toBe('normal')
    expect(usePoolStore.getState().sourceHostId).toBe(A)
    fireEvent.mouseEnter(screen.getByTestId('host-tile-d-a'))
    expect(screen.getByTestId('host-card-pool-fallback')).toBeTruthy()
  })

  it('every daemon failing: the host falls back to the fleet readings, not dimmed', async () => {
    fetchPoolHosts.mockImplementation(async () => { throw Object.assign(new Error('x'), { poolError: { code: 'wwd_unavailable', action: 'next_source' } }) })
    answer()
    render(<FleetCells variant="wide" />)
    await flush()
    expect(screen.getByTestId('load-ring-d-a').dataset.zone).toBe('normal')
    expect(screen.getByTestId('host-badge-dim-d-a').className).not.toContain('opacity-40')
    expect(screen.queryByTestId('worker-running-d-a')).toBeNull()
  })

  it('facts that go older than 30 s turn into the fleet readings without a new answer', async () => {
    fetchPoolHosts.mockResolvedValue([mlab({ facts: { ...darwin, at: Date.now(), worker: { ...darwin.worker, running: { heavy: 1, light: 0 } } } })])
    answer()
    render(<FleetCells variant="wide" />)
    await flush()
    expect(screen.getByTestId('worker-running-d-a')).toBeTruthy()
    fetchPoolHosts.mockImplementation(() => new Promise(() => {}))
    await act(async () => { await vi.advanceTimersByTimeAsync(40_000) })
    expect(screen.queryByTestId('worker-running-d-a')).toBeNull()
  })

  it('a host the pool lists but the fleet does not is not invented as a cell', async () => {
    fetchPoolHosts.mockResolvedValue([mlab(), mlab({ alias: 'ghost', host_id: 'h-ghost' })])
    answer()
    render(<FleetCells variant="wide" />)
    await flush()
    expect(within(screen.getByTestId('fleet-cells')).getAllByRole('button')).toHaveLength(1)
  })

  it('the pool being the source drops the waiting-primary notice from the cells', async () => {
    fetchPoolHosts.mockResolvedValue([mlab()])
    answer()
    render(<FleetCells variant="wide" />)
    await flush()
    expect(screen.getByTestId('host-tile-d-a').getAttribute('aria-label')).not.toContain('primary')
  })
})
