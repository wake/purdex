// A host re-pointed at another daemon stays `connected` in the runtime, so the connected check alone does not retire what the old
// endpoint still has in flight. A read remembers the connection identity (ip, port, token) it was sent on and writes back only
// while the host still has it; the identity changing moves the generation, forgets what the old daemon said, and reads the new
// daemon again. The status, events and unhandled paths each.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

const fetchWatchStatus = vi.fn()
const fetchWatchEvents = vi.fn()
const fetchWatchUnhandled = vi.fn()
vi.mock('./api', () => ({
  fetchWatchStatus: (...a: unknown[]) => fetchWatchStatus(...a),
  fetchWatchEvents: (...a: unknown[]) => fetchWatchEvents(...a),
  fetchWatchUnhandled: (...a: unknown[]) => fetchWatchUnhandled(...a),
}))
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { startWatchStatus, useWatchStatusStore } from './status-store'
import { startWatchEvents, useWatchEventsStore } from './events-store'
import type { WatchEvent } from './events-types'

const status = (id: string) => ({ self: { host_id: id, state: 'none' }, enabled: true, auto_wake: true, mode: '', targets: [] })
const ev = (id: string): WatchEvent => ({ id, host: 'm', kind: 'pressure_high', at: '2026-10-17T01:00:00Z', status: 'pending' })
const infoOf = (...caps: string[]) => ({ info: null, capabilities: null, phase: 'unavailable', error: null, fetchedAt: 1, generation: 1, fingerprint: 'f', daemonCapabilities: caps })
const flush = () => new Promise((r) => setTimeout(r, 0))
const at = (ip: string, token = 't') => useHostStore.setState({
  hosts: { h1: { id: 'h1', name: 'mlab', ip, port: 1, token, order: 0 } }, hostOrder: ['h1'], runtime: { h1: { status: 'connected' } },
} as never) // the runtime stays connected: that is the case

let stops: Array<() => void> = []
beforeEach(() => {
  for (const f of [fetchWatchStatus, fetchWatchEvents, fetchWatchUnhandled]) f.mockReset()
  useWatchStatusStore.getState().reset(); useWatchEventsStore.getState().reset()
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

  const okStatus = { kind: 'ok', at: 1, status: status('d') } as never

  it('events and unhandled answers of the old endpoint are not written', async () => {
    const oldEvents = deferred<unknown>()
    const oldCount = deferred<unknown>()
    fetchWatchEvents.mockReturnValueOnce(oldEvents.promise)
    fetchWatchUnhandled.mockReturnValueOnce(oldCount.promise)
    stops.push(startWatchEvents())
    useWatchStatusStore.getState().set('h1', okStatus)
    await flush()
    at('2.2.2.2') // the status store is not running here: the status entry stays, as it would between its forget and its answer
    await flush()
    oldEvents.resolve({ kind: 'ok', events: [ev('old')] })
    oldCount.resolve({ kind: 'ok', count: 99 })
    await flush()
    expect(useWatchEventsStore.getState().byHost.h1).toBeUndefined()
    expect(useWatchEventsStore.getState().unhandled.h1).toBeUndefined()
  })

  it('the new identity is read again once its status answers, and what it says is kept', async () => {
    fetchWatchEvents.mockResolvedValue({ kind: 'ok', events: [ev('old')] })
    fetchWatchUnhandled.mockResolvedValue({ kind: 'ok', count: 1 })
    stops.push(startWatchEvents())
    useWatchStatusStore.getState().set('h1', okStatus)
    await flush()
    at('2.2.2.2')
    expect(useWatchEventsStore.getState().byHost.h1).toBeUndefined()
    expect(useWatchEventsStore.getState().unhandled.h1).toBeUndefined()
    fetchWatchEvents.mockResolvedValue({ kind: 'ok', events: [ev('new')] })
    fetchWatchUnhandled.mockResolvedValue({ kind: 'ok', count: 7 })
    useWatchStatusStore.getState().forget('h1')
    useWatchStatusStore.getState().set('h1', okStatus)
    await flush()
    expect(useWatchEventsStore.getState().byHost.h1).toMatchObject({ events: [{ id: 'new' }] })
    expect(useWatchEventsStore.getState().unhandled.h1).toBe(7)
  })
})
