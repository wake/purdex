import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { startFleetSupport } from './fleet-support'
import { handleFleetEvent } from './fleet-ws'
import { useFleetStore } from './store'
import { useHostStore } from '../../stores/useHostStore'
import { fast, host, primaryAnswer, standbyAnswer } from './fixtures'
import type { FleetAnswer } from './types'

vi.mock('../host-api', async (orig) => ({ ...(await orig<typeof import('../host-api')>()), fetchHostInfo: vi.fn() }))
vi.mock('./api', () => ({ fetchFleet: vi.fn() }))

import { fetchHostInfo } from '../host-api'
import { fetchFleet } from './api'

const A = 'cfg-a'
const B = 'cfg-b'
const cfg = (id: string, over: Record<string, unknown> = {}) => ({ id, name: id, ip: '100.64.0.2', port: 7860, token: 't', order: 0, ...over })
const st = () => useFleetStore.getState()
const setStatus = (id: string, status: 'connected' | 'disconnected') => useHostStore.setState((s) => ({ runtime: { ...s.runtime, [id]: { status } } }))
const flush = async () => { await vi.advanceTimersByTimeAsync(0); await vi.advanceTimersByTimeAsync(0) }

function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((a) => { resolve = a })
  return { promise, resolve }
}
const caps = (list: string[]) => ({ capabilities: list }) as never

let stop: () => void
beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(1_000_000)
  vi.mocked(fetchHostInfo).mockReset()
  vi.mocked(fetchFleet).mockReset()
  st().reset()
  useHostStore.setState({ hosts: { [A]: cfg(A), [B]: cfg(B) }, hostOrder: [A, B], activeHostId: A, runtime: { [A]: { status: 'disconnected' }, [B]: { status: 'disconnected' } } } as never)
})
afterEach(() => { stop?.(); vi.useRealTimers() })

describe('startFleetSupport: the fleet.v1 probe', () => {
  it('a connected host with fleet.v1 gets the one GET, and its answer is the fleet', async () => {
    vi.mocked(fetchHostInfo).mockResolvedValue(caps(['fleet.v1']))
    vi.mocked(fetchFleet).mockResolvedValue(primaryAnswer(2, [host('d-a')]))
    stop = startFleetSupport()
    setStatus(A, 'connected')
    await flush()
    expect(st().byHost[A].support).toBe('yes')
    expect(st().primaryHostId).toBe(A)
    expect(fetchFleet).toHaveBeenCalledTimes(1)
  })

  it('a host without the capability gets support no and no GET', async () => {
    vi.mocked(fetchHostInfo).mockResolvedValue(caps(['journal.v1']))
    stop = startFleetSupport()
    setStatus(A, 'connected')
    await flush()
    expect(st().byHost[A].support).toBe('no')
    expect(fetchFleet).not.toHaveBeenCalled()
  })

  it('a failed /api/info leaves support unknown', async () => {
    vi.mocked(fetchHostInfo).mockRejectedValue(new Error('boom'))
    stop = startFleetSupport()
    setStatus(A, 'connected')
    await flush()
    expect(st().byHost[A].support).toBe('unknown')
  })

  it('hosts already connected when it starts are probed', async () => {
    useHostStore.setState({ runtime: { [A]: { status: 'connected' } } } as never)
    vi.mocked(fetchHostInfo).mockResolvedValue(caps(['fleet.v1']))
    vi.mocked(fetchFleet).mockResolvedValue(primaryAnswer(2, [host('d-a')]))
    stop = startFleetSupport()
    await flush()
    expect(st().primaryHostId).toBe(A)
  })

  it('a GET reply after a disconnect does not write', async () => {
    const get = deferred<FleetAnswer>()
    vi.mocked(fetchHostInfo).mockResolvedValue(caps(['fleet.v1']))
    vi.mocked(fetchFleet).mockReturnValue(get.promise)
    stop = startFleetSupport()
    setStatus(A, 'connected')
    await flush()
    setStatus(A, 'disconnected')
    get.resolve(primaryAnswer(2, [host('d-a')]))
    await flush()
    expect(st().snapshot).toBeNull()
    expect(st().byHost[A].support).toBe('unknown')
  })

  it('an /api/info answer of the old connection after a reconnect does not set support', async () => {
    const first = deferred<never>()
    vi.mocked(fetchHostInfo).mockReturnValueOnce(first.promise as never).mockResolvedValue(caps([]))
    stop = startFleetSupport()
    setStatus(A, 'connected')
    await flush()
    setStatus(A, 'disconnected')
    setStatus(A, 'connected')
    await flush()
    first.resolve(caps(['fleet.v1']))
    await flush()
    expect(st().byHost[A].support).toBe('no')
    expect(fetchFleet).not.toHaveBeenCalled()
  })

  it('a re-point forgets the old daemon: its fleet is gone and a late GET reply does not write', async () => {
    const get = deferred<FleetAnswer>()
    vi.mocked(fetchHostInfo).mockResolvedValueOnce(caps(['fleet.v1'])).mockResolvedValue(caps([]))
    vi.mocked(fetchFleet).mockReturnValue(get.promise)
    stop = startFleetSupport()
    setStatus(A, 'connected')
    await flush()
    useHostStore.setState({ hosts: { [A]: cfg(A, { ip: '100.64.0.9' }), [B]: cfg(B) } } as never)
    get.resolve(primaryAnswer(2, [host('d-a')]))
    await flush()
    expect(st().snapshot).toBeNull()
    expect(st().byHost[A].support).toBe('no')
  })

  it('removing the host forgets its entry', async () => {
    vi.mocked(fetchHostInfo).mockResolvedValue(caps(['fleet.v1']))
    vi.mocked(fetchFleet).mockResolvedValue(primaryAnswer(2, [host('d-a')]))
    stop = startFleetSupport()
    setStatus(A, 'connected')
    await flush()
    useHostStore.setState({ hosts: { [B]: cfg(B) }, hostOrder: [B], runtime: {} } as never)
    expect(st().byHost[A]).toBeUndefined()
    expect(st().snapshot).toBeNull()
  })
})

describe('waiting for a primary', () => {
  async function withPrimary() {
    vi.mocked(fetchHostInfo).mockResolvedValue(caps(['fleet.v1']))
    vi.mocked(fetchFleet).mockImplementation(async (id: string) => (id === A ? primaryAnswer(2, [host('d-a', { fast: fast({ cpu: 61 }) })]) : standbyAnswer(2, 'air26')))
    stop = startFleetSupport()
    setStatus(A, 'connected')
    setStatus(B, 'connected')
    await flush()
    vi.mocked(fetchFleet).mockClear()
  }

  it('10 s without a push: the cells wait with their last values, and both daemons are read again every 5 s', async () => {
    await withPrimary()
    await vi.advanceTimersByTimeAsync(9_000)
    expect(st().waitingPrimary).toBe(false)
    expect(fetchFleet).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1_000)
    expect(st().waitingPrimary).toBe(true)
    expect(st().snapshot?.hosts['d-a'].fast?.cpu).toBe(61)
    vi.mocked(fetchFleet).mockClear()
    await vi.advanceTimersByTimeAsync(5_000)
    expect(vi.mocked(fetchFleet).mock.calls.map((c) => c[0]).sort()).toEqual([A, B])
  })

  it('a primary that answers again ends the waiting and the re-reads', async () => {
    await withPrimary()
    await vi.advanceTimersByTimeAsync(12_000)
    expect(st().waitingPrimary).toBe(true)
    await vi.advanceTimersByTimeAsync(4_000) // the re-read round (5 s after waiting began): A answers primary again
    expect(st().waitingPrimary).toBe(false)
    vi.mocked(fetchFleet).mockClear()
    await vi.advanceTimersByTimeAsync(4_000)
    expect(fetchFleet).not.toHaveBeenCalled()
  })

  it('only a standby known: it keeps asking every 5 s until a primary answers', async () => {
    vi.mocked(fetchHostInfo).mockResolvedValue(caps(['fleet.v1']))
    vi.mocked(fetchFleet).mockResolvedValue(standbyAnswer(2, 'air26'))
    stop = startFleetSupport()
    setStatus(A, 'connected')
    await flush()
    expect(st().primaryElsewhere).toBe(true)
    vi.mocked(fetchFleet).mockClear()
    await vi.advanceTimersByTimeAsync(5_000)
    expect(fetchFleet).toHaveBeenCalledTimes(1)
    vi.mocked(fetchFleet).mockResolvedValue(primaryAnswer(3, [host('d-a')]))
    await vi.advanceTimersByTimeAsync(5_000)
    expect(st().primaryHostId).toBe(A)
    expect(st().primaryElsewhere).toBe(false)
  })
})

describe('handleFleetEvent', () => {
  const wire = (term: number, cpu: number) => JSON.stringify({ term, primary: 'air26', hosts: [{ id: 'd-a', fast: fast({ at: 2_000_000, cpu }) }] })

  beforeEach(async () => {
    vi.mocked(fetchHostInfo).mockResolvedValue(caps(['fleet.v1']))
    vi.mocked(fetchFleet).mockImplementation(async (id: string) => (id === A ? primaryAnswer(2, [host('d-a')]) : standbyAnswer(2, 'air26')))
    stop = startFleetSupport()
    setStatus(A, 'connected')
    setStatus(B, 'connected')
    await flush()
    vi.mocked(fetchFleet).mockClear()
  })

  it('merges a push from the primary', () => {
    handleFleetEvent(A, wire(2, 93))
    expect(st().snapshot?.hosts['d-a'].fast?.cpu).toBe(93)
  })

  it('a push from the standby is ignored', () => {
    handleFleetEvent(B, wire(2, 93))
    expect(st().snapshot?.hosts['d-a'].fast?.cpu).toBe(20)
    expect(fetchFleet).not.toHaveBeenCalled()
  })

  it('a push of a higher term from the other daemon reads both again and switches to it when it answers primary', async () => {
    vi.mocked(fetchFleet).mockImplementation(async (id: string) => (id === B ? primaryAnswer(3, [host('d-a', { fast: fast({ cpu: 4 }) })]) : standbyAnswer(3, 'mlab')))
    handleFleetEvent(B, wire(3, 93))
    await flush()
    expect(vi.mocked(fetchFleet).mock.calls.map((c) => c[0]).sort()).toEqual([A, B])
    expect(st().primaryHostId).toBe(B)
    expect(st().snapshot?.hosts['d-a'].fast?.cpu).toBe(4)
  })

  it('a malformed frame changes nothing', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    handleFleetEvent(A, 'not json')
    handleFleetEvent(A, JSON.stringify({ hosts: 3 }))
    expect(st().snapshot?.hosts['d-a'].fast?.cpu).toBe(20)
  })
})
