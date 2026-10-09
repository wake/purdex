// spa/src/lib/team/unattended-support.test.ts — does each host's daemon support 無人值守模式 (unattended spec D-U23-5;
// plan PU-2a): one /api/info per transition to connected (or re-point), `relay.unattended.v1` in its capabilities.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { HostInfo } from '../../stores/useHostStore'

const fetchHostInfo = vi.fn<(hostId: string) => Promise<HostInfo>>()
vi.mock('../host-api', () => ({ fetchHostInfo: (hostId: string) => fetchHostInfo(hostId) }))

import { startUnattendedSupport } from './unattended-support'
import { useHostStore } from '../../stores/useHostStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'

const host = (id: string, ip = '100.64.0.2') => ({ id, name: id, ip, port: 7860, token: 't', order: 0 })
const info = (capabilities?: unknown): HostInfo =>
  ({ host_id: 'mini-lab:abc', tmux_instance: '', purdex_version: '', tmux_version: '', os: '', arch: '', ...(capabilities === undefined ? {} : { capabilities }) })
const WITH = info(['conversations.scope.v1', 'relay.unattended.v1'])
const WITHOUT = info(['conversations.scope.v1'])

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
const flush = () => new Promise((r) => setTimeout(r, 0))
const support = (id: string) => useUnattendedStore.getState().byHost[id]?.support

let stop: () => void = () => {}

beforeEach(() => {
  useHostStore.getState().reset()
  useUnattendedStore.getState().reset()
  fetchHostInfo.mockReset()
  fetchHostInfo.mockResolvedValue(WITH)
  useHostStore.setState({ hosts: { h1: host('h1'), h2: host('h2', '100.64.0.4') }, hostOrder: ['h1', 'h2'], runtime: {} })
})
afterEach(() => { stop(); vi.restoreAllMocks() })

describe('startUnattendedSupport', () => {
  it('probes the hosts already connected at start', async () => {
    useHostStore.setState({ runtime: { h2: { status: 'connected' } } })
    stop = startUnattendedSupport()
    expect(fetchHostInfo).toHaveBeenCalledTimes(1)
    expect(fetchHostInfo).toHaveBeenCalledWith('h2')
    await flush()
    expect(support('h2')).toBe('yes')
  })

  it('connected → one /api/info; the capability present → yes; no second call without a new transition', async () => {
    stop = startUnattendedSupport()
    useHostStore.getState().setRuntime('h1', { status: 'connected' })
    useHostStore.getState().setRuntime('h1', { latency: 3 })
    expect(fetchHostInfo).toHaveBeenCalledTimes(1)
    expect(fetchHostInfo).toHaveBeenCalledWith('h1')
    await flush()
    expect(support('h1')).toBe('yes')
    useHostStore.getState().setRuntime('h1', { latency: 4 })
    await flush()
    expect(fetchHostInfo).toHaveBeenCalledTimes(1)
  })

  it('the capability absent → no (a daemon too old for the switch)', async () => {
    fetchHostInfo.mockResolvedValue(WITHOUT)
    stop = startUnattendedSupport()
    useHostStore.getState().setRuntime('h1', { status: 'connected' })
    await flush()
    expect(support('h1')).toBe('no')
  })

  it.each([
    ['missing', info()],
    ['an object', info({ 'relay.unattended.v1': true })],
    ['a string', info('relay.unattended.v1')],
  ])('capabilities %s (not an array) → no', async (_what, answer) => {
    fetchHostInfo.mockResolvedValue(answer)
    stop = startUnattendedSupport()
    useHostStore.getState().setRuntime('h1', { status: 'connected' })
    await flush()
    expect(support('h1')).toBe('no')
  })

  it('a failed request leaves unknown until the next transition, then asks again', async () => {
    fetchHostInfo.mockRejectedValueOnce(new Error('refused'))
    stop = startUnattendedSupport()
    useHostStore.getState().setRuntime('h1', { status: 'connected' })
    await flush()
    expect(support('h1') ?? 'unknown').toBe('unknown')
    useHostStore.getState().setRuntime('h1', { status: 'reconnecting' })
    useHostStore.getState().setRuntime('h1', { status: 'connected' })
    expect(fetchHostInfo).toHaveBeenCalledTimes(2)
    await flush()
    expect(support('h1')).toBe('yes')
  })

  it('never probes while not connected', () => {
    stop = startUnattendedSupport()
    useHostStore.getState().setRuntime('h1', { status: 'reconnecting' })
    useHostStore.getState().updateHost('h1', { ip: '10.0.0.1' })
    expect(fetchHostInfo).not.toHaveBeenCalled()
  })

  it('a re-point while connected probes again and drops the stale answer of the old endpoint', async () => {
    const old = deferred<HostInfo>()
    fetchHostInfo.mockReturnValueOnce(old.promise).mockResolvedValueOnce(WITHOUT)
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    stop = startUnattendedSupport()
    useHostStore.getState().updateHost('h1', { ip: '10.0.0.9' })
    expect(fetchHostInfo).toHaveBeenCalledTimes(2)
    await flush()
    expect(support('h1')).toBe('no')
    old.resolve(WITH) // the old daemon's answer arrives late
    await flush()
    expect(support('h1')).toBe('no')
  })

  it('a token change while connected probes again', () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    stop = startUnattendedSupport()
    fetchHostInfo.mockClear()
    useHostStore.getState().updateHost('h1', { token: 'rotated' })
    expect(fetchHostInfo).toHaveBeenCalledTimes(1)
  })

  it("a re-point forgets what the old daemon said: its state and support are not the new one's", () => {
    stop = startUnattendedSupport()
    useUnattendedStore.getState().applyState('h1', { on: true, since: 1, changed_at: 1 })
    useUnattendedStore.getState().setSupport('h1', 'yes')
    useHostStore.getState().updateHost('h1', { port: 7861 })
    expect(useUnattendedStore.getState().byHost.h1).toBeUndefined()
  })

  it('a removed host is forgotten, and its late answer applies nothing', async () => {
    const late = deferred<HostInfo>()
    fetchHostInfo.mockReturnValueOnce(late.promise)
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    stop = startUnattendedSupport()
    useUnattendedStore.getState().applyState('h1', { on: true, since: 1, changed_at: 1 })
    useHostStore.getState().removeHost('h1')
    expect(useUnattendedStore.getState().byHost.h1).toBeUndefined()
    late.resolve(WITH)
    await flush()
    expect(useUnattendedStore.getState().byHost.h1).toBeUndefined()
  })

  it('stop unsubscribes', () => {
    stop = startUnattendedSupport()
    stop()
    useHostStore.getState().setRuntime('h1', { status: 'connected' })
    expect(fetchHostInfo).not.toHaveBeenCalled()
  })

  describe('team.relay_quota.v1 (relay quota, plan RQ-A Task 2)', () => {
    const quotaSupport = (id: string) => useUnattendedStore.getState().byHost[id]?.quotaSupport

    it('listed → yes; the same probe sets the switch\'s support', async () => {
      fetchHostInfo.mockResolvedValue(info(['relay.unattended.v1', 'team.relay_quota.v1']))
      stop = startUnattendedSupport()
      useHostStore.getState().setRuntime('h1', { status: 'connected' })
      await flush()
      expect(quotaSupport('h1')).toBe('yes')
      expect(support('h1')).toBe('yes')
    })

    it('not listed → no (a daemon with the switch but without quotas keeps its switch)', async () => {
      stop = startUnattendedSupport() // WITH has only the unattended capability
      useHostStore.getState().setRuntime('h1', { status: 'connected' })
      await flush()
      expect(quotaSupport('h1')).toBe('no')
      expect(support('h1')).toBe('yes')
    })

    it('a failed probe leaves it unknown', async () => {
      fetchHostInfo.mockRejectedValue(new Error('down'))
      stop = startUnattendedSupport()
      useHostStore.getState().setRuntime('h1', { status: 'connected' })
      await flush()
      expect(quotaSupport('h1')).toBeUndefined()
    })

    it('a re-point forgets it with the rest of the entry', async () => {
      fetchHostInfo.mockResolvedValue(info(['relay.unattended.v1', 'team.relay_quota.v1']))
      stop = startUnattendedSupport()
      useHostStore.getState().setRuntime('h1', { status: 'connected' })
      await flush()
      expect(quotaSupport('h1')).toBe('yes')
      fetchHostInfo.mockReset()
      fetchHostInfo.mockReturnValue(new Promise(() => {}))
      useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h1: host('h1', '100.64.0.9') } })
      expect(quotaSupport('h1')).toBeUndefined()
    })
  })
})
