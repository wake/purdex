import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { startFleetSupport } from './fleet-support'
import { useFleetStore } from './store'
import { useHostStore } from '../../stores/useHostStore'
import type { FleetPayload } from './types'

vi.mock('../host-api', async (orig) => ({ ...(await orig<typeof import('../host-api')>()), fetchHostInfo: vi.fn() }))
vi.mock('./api', async (orig) => ({ ...(await orig<typeof import('./api')>()), fetchFleet: vi.fn() }))

import { fetchHostInfo } from '../host-api'
import { fetchFleet } from './api'

const H = 'h1'
const host = (over: Record<string, unknown> = {}) => ({ id: H, name: 'mlab', ip: '100.64.0.2', port: 7860, token: 't', order: 0, ...over })
const nums = { cpu: 1, mem: 2, disk: 3, load1: 0, ncpu: 4, zone: 'normal', trend: 'flat', sampled_at: 1 } as const
const payload = (cpu: number): FleetPayload => ({ self: { ...nums, cpu }, probes: [] })

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((a, b) => { resolve = a; reject = b })
  return { promise, resolve, reject }
}
const tick = () => new Promise((r) => setTimeout(r, 0))
const entry = () => useFleetStore.getState().byHost[H]
const setStatus = (status: 'connected' | 'disconnected') => useHostStore.setState((s) => ({ runtime: { ...s.runtime, [H]: { status } } }))

let stop: () => void
beforeEach(() => {
  vi.mocked(fetchHostInfo).mockReset()
  vi.mocked(fetchFleet).mockReset()
  useFleetStore.getState().reset()
  useHostStore.setState({ hosts: { [H]: host() }, hostOrder: [H], activeHostId: H, runtime: { [H]: { status: 'disconnected' } } } as never)
})
afterEach(() => { stop?.() })

describe('startFleetSupport', () => {
  it('a connected host with fleet.v1 gets the one GET and its numbers', async () => {
    vi.mocked(fetchHostInfo).mockResolvedValue({ capabilities: ['fleet.v1'] } as never)
    vi.mocked(fetchFleet).mockResolvedValue(payload(42))
    stop = startFleetSupport()
    setStatus('connected')
    await tick(); await tick()
    expect(entry().support).toBe('yes')
    expect(entry().self?.cpu).toBe(42)
    expect(fetchFleet).toHaveBeenCalledTimes(1)
  })

  it('a host without the capability gets no GET and support no', async () => {
    vi.mocked(fetchHostInfo).mockResolvedValue({ capabilities: ['other'] } as never)
    stop = startFleetSupport()
    setStatus('connected')
    await tick()
    expect(entry().support).toBe('no')
    expect(fetchFleet).not.toHaveBeenCalled()
  })

  it('hosts already connected when it starts are probed', async () => {
    useHostStore.setState({ runtime: { [H]: { status: 'connected' } } } as never)
    vi.mocked(fetchHostInfo).mockResolvedValue({ capabilities: ['fleet.v1'] } as never)
    vi.mocked(fetchFleet).mockResolvedValue(payload(7))
    stop = startFleetSupport()
    await tick(); await tick()
    expect(entry().self?.cpu).toBe(7)
  })

  it('a capability answer that arrives long after the connection still starts the GET', async () => {
    const info = deferred<unknown>()
    vi.mocked(fetchHostInfo).mockReturnValue(info.promise as never)
    vi.mocked(fetchFleet).mockResolvedValue(payload(9))
    stop = startFleetSupport()
    setStatus('connected')
    await tick()
    expect(entry().support).toBe('unknown')
    info.resolve({ capabilities: ['fleet.v1'] })
    await tick(); await tick()
    expect(entry().self?.cpu).toBe(9)
  })

  it('a failed /api/info leaves support unknown', async () => {
    vi.mocked(fetchHostInfo).mockRejectedValue(new Error('boom'))
    stop = startFleetSupport()
    setStatus('connected')
    await tick()
    expect(entry().support).toBe('unknown')
  })

  it('a GET reply after a disconnect does not write', async () => {
    const get = deferred<FleetPayload>()
    vi.mocked(fetchHostInfo).mockResolvedValue({ capabilities: ['fleet.v1'] } as never)
    vi.mocked(fetchFleet).mockReturnValue(get.promise)
    stop = startFleetSupport()
    setStatus('connected')
    await tick(); await tick()
    setStatus('disconnected')
    get.resolve(payload(99))
    await tick()
    expect(entry().self).toBeNull()
    expect(entry().offlineSince).not.toBeNull()
  })

  it('a GET reply after a re-point does not write', async () => {
    const get = deferred<FleetPayload>()
    vi.mocked(fetchHostInfo).mockResolvedValueOnce({ capabilities: ['fleet.v1'] } as never).mockResolvedValue({ capabilities: [] } as never)
    vi.mocked(fetchFleet).mockReturnValue(get.promise)
    stop = startFleetSupport()
    setStatus('connected')
    await tick(); await tick()
    useHostStore.setState({ hosts: { [H]: host({ ip: '100.64.0.9' }) } } as never)
    get.resolve(payload(99))
    await tick(); await tick()
    expect(entry().self).toBeNull()
    expect(entry().support).toBe('no')
  })

  it('a re-point to a host that is not connected leaves nothing of the old daemon', async () => {
    vi.mocked(fetchHostInfo).mockResolvedValue({ capabilities: ['fleet.v1'] } as never)
    vi.mocked(fetchFleet).mockResolvedValue(payload(5))
    stop = startFleetSupport()
    setStatus('connected')
    await tick(); await tick()
    expect(entry().self?.cpu).toBe(5)
    useHostStore.setState({ hosts: { [H]: host({ ip: '100.64.0.9' }) }, runtime: { [H]: { status: 'disconnected' } } } as never)
    expect(entry()).toBeUndefined()
  })

  it('a GET reply after the host was removed does not write, and the entry is gone', async () => {
    const get = deferred<FleetPayload>()
    vi.mocked(fetchHostInfo).mockResolvedValue({ capabilities: ['fleet.v1'] } as never)
    vi.mocked(fetchFleet).mockReturnValue(get.promise)
    stop = startFleetSupport()
    setStatus('connected')
    await tick(); await tick()
    useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {} } as never)
    get.resolve(payload(99))
    await tick()
    expect(entry()).toBeUndefined()
  })

  it('an /api/info answer of the old connection after a reconnect does not set support', async () => {
    const first = deferred<unknown>()
    vi.mocked(fetchHostInfo).mockReturnValueOnce(first.promise as never).mockResolvedValue({ capabilities: [] } as never)
    stop = startFleetSupport()
    setStatus('connected')
    await tick()
    setStatus('disconnected')
    setStatus('connected')
    await tick()
    first.resolve({ capabilities: ['fleet.v1'] })
    await tick(); await tick()
    expect(entry().support).toBe('no')
    expect(fetchFleet).not.toHaveBeenCalled()
  })
})
