import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { HostInfo } from '../stores/useHostStore'

const fetchHostInfo = vi.fn<(hostId: string) => Promise<HostInfo>>()
vi.mock('./host-api', () => ({ fetchHostInfo: (hostId: string) => fetchHostInfo(hostId) }))

import { startHostDaemonIdVerification } from './host-daemon-id'
import { useHostStore, selectDaemonIdMismatch } from '../stores/useHostStore'

const ID = 'mini-lab:abc123'
const OTHER = 'mini-lab:zzz999'
const host = (id: string, ip = '100.64.0.2', extra: Record<string, unknown> = {}) =>
  ({ id, name: id, ip, port: 7860, token: 't', order: 0, ...extra })
const info = (host_id: string): HostInfo =>
  ({ host_id, tmux_instance: '', purdex_version: '', tmux_version: '', os: '', arch: '' })

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
const flush = () => new Promise((r) => setTimeout(r, 0))

let stop: () => void = () => {}

beforeEach(() => {
  useHostStore.getState().reset()
  fetchHostInfo.mockReset()
  fetchHostInfo.mockResolvedValue(info(ID))
  useHostStore.setState({ hosts: { h1: host('h1'), h2: host('h2', '100.64.0.4') }, hostOrder: ['h1', 'h2'], runtime: {} })
})
afterEach(() => { stop(); vi.restoreAllMocks() })

describe('startHostDaemonIdVerification (spec 2026-09-23 D4.2)', () => {
  it('verifies hosts already connected at start', async () => {
    useHostStore.setState({ runtime: { h2: { status: 'connected' } } })
    stop = startHostDaemonIdVerification()
    expect(fetchHostInfo).toHaveBeenCalledTimes(1)
    expect(fetchHostInfo).toHaveBeenCalledWith('h2')
    await flush()
    expect(useHostStore.getState().hosts.h2.daemonId).toBe(ID)
  })

  it('requests once on the transition to connected, and learns the id', async () => {
    stop = startHostDaemonIdVerification()
    useHostStore.getState().setRuntime('h1', { status: 'connected' })
    useHostStore.getState().setRuntime('h1', { latency: 3 })
    expect(fetchHostInfo).toHaveBeenCalledTimes(1)
    await flush()
    expect(useHostStore.getState().hosts.h1.daemonId).toBe(ID)
    // Learning the id is itself a stored-daemonId change: it must not trigger a second request.
    expect(fetchHostInfo).toHaveBeenCalledTimes(1)
  })

  it('never requests while disconnected', () => {
    stop = startHostDaemonIdVerification()
    useHostStore.getState().setRuntime('h1', { status: 'reconnecting' })
    useHostStore.getState().updateHost('h1', { ip: '10.0.0.1' })
    useHostStore.getState().updateHost('h1', { token: 'u' })
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, h1: { ...s.hosts.h1, daemonId: OTHER } } }))
    expect(fetchHostInfo).not.toHaveBeenCalled()
  })

  it('requests on an endpoint change while connected', () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    stop = startHostDaemonIdVerification()
    fetchHostInfo.mockClear()
    useHostStore.getState().updateHost('h1', { port: 7861 })
    expect(fetchHostInfo).toHaveBeenCalledTimes(1)
  })

  it('requests on a token change while connected', () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    stop = startHostDaemonIdVerification()
    fetchHostInfo.mockClear()
    useHostStore.getState().updateHost('h1', { token: 'rotated' })
    expect(fetchHostInfo).toHaveBeenCalledTimes(1)
  })

  it('requests when the stored daemonId changes while connected (e.g. by sync) and flags a mismatch', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    stop = startHostDaemonIdVerification()
    await flush()
    fetchHostInfo.mockClear()
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, h1: { ...s.hosts.h1, daemonId: OTHER } } }))
    expect(fetchHostInfo).toHaveBeenCalledTimes(1)
    await flush()
    expect(useHostStore.getState().hosts.h1.daemonId).toBe(OTHER)
    expect(selectDaemonIdMismatch(useHostStore.getState(), 'h1')).toEqual({ stored: OTHER, observed: ID, endpoint: '100.64.0.2:7860' })
  })

  it('a failed request is not retried until the next trigger', async () => {
    fetchHostInfo.mockRejectedValue(new Error('401'))
    stop = startHostDaemonIdVerification()
    useHostStore.getState().setRuntime('h1', { status: 'connected' })
    await flush()
    useHostStore.getState().setRuntime('h1', { latency: 5 })
    await flush()
    expect(fetchHostInfo).toHaveBeenCalledTimes(1)
    expect('daemonId' in useHostStore.getState().hosts.h1).toBe(false)
    useHostStore.getState().setRuntime('h1', { status: 'disconnected' })
    useHostStore.getState().setRuntime('h1', { status: 'connected' })
    expect(fetchHostInfo).toHaveBeenCalledTimes(2)
  })

  it('an answer for an old endpoint is dropped', async () => {
    const first = deferred<HostInfo>()
    fetchHostInfo.mockReturnValueOnce(first.promise).mockReturnValue(new Promise(() => {}))
    stop = startHostDaemonIdVerification()
    useHostStore.getState().setRuntime('h1', { status: 'connected' })
    useHostStore.getState().updateHost('h1', { ip: '10.0.0.1' })
    first.resolve(info(OTHER))
    await flush()
    expect('daemonId' in useHostStore.getState().hosts.h1).toBe(false)
  })

  it('an answer for a host deleted before it lands is dropped', async () => {
    const first = deferred<HostInfo>()
    fetchHostInfo.mockReturnValueOnce(first.promise)
    stop = startHostDaemonIdVerification()
    useHostStore.getState().setRuntime('h1', { status: 'connected' })
    useHostStore.getState().removeHost('h1')
    first.resolve(info(ID))
    await flush()
    expect(useHostStore.getState().hosts.h1).toBeUndefined()
    expect(useHostStore.getState().runtime.h1).toBeUndefined()
  })

  it('stops listening once disposed', () => {
    stop = startHostDaemonIdVerification()
    stop()
    useHostStore.getState().setRuntime('h1', { status: 'connected' })
    expect(fetchHostInfo).not.toHaveBeenCalled()
  })
})
