// A host re-pointed at another daemon stays `connected` in the runtime, so the connected check alone does not retire what the old
// endpoint still has in flight. A read remembers the connection identity (ip, port, token) it was sent on and writes back only
// while the host still has it; the identity changing moves the generation, forgets what the old daemon said, and reads the new
// daemon again. The status path.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

const fetchWatchStatus = vi.fn()
vi.mock('./api', () => ({
  fetchWatchStatus: (...a: unknown[]) => fetchWatchStatus(...a),
}))
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { startWatchStatus, useWatchStatusStore } from './status-store'

const status = (id: string) => ({ self: { host_id: id, state: 'none' }, enabled: true, auto_wake: true, mode: '', targets: [] })
const infoOf = (...caps: string[]) => ({ info: null, capabilities: null, phase: 'unavailable', error: null, fetchedAt: 1, generation: 1, fingerprint: 'f', daemonCapabilities: caps })
const flush = () => new Promise((r) => setTimeout(r, 0))
const at = (ip: string, token = 't') => useHostStore.setState({
  hosts: { h1: { id: 'h1', name: 'mlab', ip, port: 1, token, order: 0 } }, hostOrder: ['h1'], runtime: { h1: { status: 'connected' } },
} as never) // the runtime stays connected: that is the case

let stops: Array<() => void> = []
beforeEach(() => {
  fetchWatchStatus.mockReset()
  useWatchStatusStore.getState().reset()
  useNexHostStore.setState({ byHost: { h1: infoOf('watch.v1') }, ensure: async () => {} } as never)
  at('1.1.1.1')
})
afterEach(() => { for (const s of stops) s(); stops = [] })

const deferred = <T,>() => { let resolve!: (v: T) => void; const promise = new Promise<T>((r) => { resolve = r }); return { promise, resolve } }

describe('re-point with the runtime still connected', () => {
  it('a status answer of the old endpoint is not written, and the new endpoint is read', async () => {
    const old = deferred<unknown>()
    fetchWatchStatus.mockReturnValueOnce(old.promise)
    fetchWatchStatus.mockResolvedValue({ kind: 'ok', status: status('new') })
    stops.push(startWatchStatus())
    await flush()
    at('2.2.2.2')
    await flush()
    old.resolve({ kind: 'ok', status: status('old') })
    await flush()
    expect(fetchWatchStatus).toHaveBeenCalledTimes(2)
    const e = useWatchStatusStore.getState().byHost.h1
    expect(e.kind === 'ok' && e.status.self.host_id).toBe('new')
  })

  it('a re-point forgets what the old daemon said until the new one answers', async () => {
    fetchWatchStatus.mockResolvedValueOnce({ kind: 'ok', status: status('old') })
    const next = deferred<unknown>()
    fetchWatchStatus.mockReturnValueOnce(next.promise)
    stops.push(startWatchStatus())
    await flush()
    expect(useWatchStatusStore.getState().byHost.h1.kind).toBe('ok')
    at('2.2.2.2')
    expect(useWatchStatusStore.getState().byHost.h1).toBeUndefined()
    next.resolve({ kind: 'ok', status: status('new') })
    await flush()
    expect(useWatchStatusStore.getState().byHost.h1.kind).toBe('ok')
  })

  it('a token change is a re-point too', async () => {
    const old = deferred<unknown>()
    fetchWatchStatus.mockReturnValueOnce(old.promise)
    fetchWatchStatus.mockResolvedValue({ kind: 'ok', status: status('new') })
    stops.push(startWatchStatus())
    await flush()
    at('1.1.1.1', 'other-token')
    await flush()
    old.resolve({ kind: 'ok', status: status('old') })
    await flush()
    const e = useWatchStatusStore.getState().byHost.h1
    expect(e.kind === 'ok' && e.status.self.host_id).toBe('new')
  })
})
