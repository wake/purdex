// spa/src/lib/team/unattended-support.test.ts — does each host's daemon support 無人值守模式 (unattended spec D-U23-5;
// plan PU-2a): one /api/info per transition to connected (or re-point), `relay.unattended.v1` in its capabilities.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { HostInfo } from '../../stores/useHostStore'

const fetchHostInfo = vi.fn<(hostId: string) => Promise<HostInfo>>()
vi.mock('../host-api', () => ({ fetchHostInfo: (hostId: string) => fetchHostInfo(hostId) }))

import { startUnattendedSupport } from './unattended-support'
import { useHostStore } from '../../stores/useHostStore'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { useRelayQuotaStore } from './relay-quota'
import { teamKey, useMaxMembersStore } from './max-members'

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
  useRelayQuotaStore.getState().reset()
  useMaxMembersStore.getState().reset()
  fetchHostInfo.mockReset()
  fetchHostInfo.mockResolvedValue(WITH)
  useHostStore.setState({ hosts: { h1: host('h1'), h2: host('h2', '100.64.0.4') }, hostOrder: ['h1', 'h2'], runtime: {} })
})
afterEach(() => { stop(); vi.restoreAllMocks() })

describe('startUnattendedSupport', () => {
  it('a cap request still out to a host is forgotten when the host is re-pointed or removed', () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    stop = startUnattendedSupport()
    useMaxMembersStore.getState().begin(teamKey('h1', 't1'), { token: 1, identity: 'x' })
    useMaxMembersStore.getState().begin(teamKey('h2', 't1'), { token: 2, identity: 'y' })
    useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h1: host('h1', '100.64.0.9') } }) // re-point h1
    expect(Object.keys(useMaxMembersStore.getState().inflight)).toEqual([teamKey('h2', 't1')])
    const { h2: _gone, ...rest } = useHostStore.getState().hosts
    useHostStore.setState({ hosts: rest }) // remove h2
    expect(useMaxMembersStore.getState().inflight).toEqual({})
  })

  it('team.max_members.v1 in the capabilities → the cap stepper is supported; without it, not', async () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected' }, h2: { status: 'connected' } } })
    fetchHostInfo.mockImplementation(async (id) => info(id === 'h1' ? ['relay.unattended.v1', 'team.max_members.v1'] : ['relay.unattended.v1']))
    stop = startUnattendedSupport()
    await flush()
    expect(useUnattendedStore.getState().byHost.h1?.maxMembersSupport).toBe('yes')
    expect(useUnattendedStore.getState().byHost.h2?.maxMembersSupport).toBe('no')
  })

  it('team.edit.v1 in the capabilities → the appearance edit is supported; without it, not', async () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected' }, h2: { status: 'connected' } } })
    fetchHostInfo.mockImplementation(async (id) => info(id === 'h1' ? ['relay.unattended.v1', 'team.edit.v1'] : ['relay.unattended.v1']))
    stop = startUnattendedSupport()
    await flush()
    expect(useUnattendedStore.getState().byHost.h1?.editSupport).toBe('yes')
    expect(useUnattendedStore.getState().byHost.h2?.editSupport).toBe('no')
  })

  // #2309: every capability flag follows the connection by the one rule - unknown on a disconnect, unknown from the
  // reconnect until the probe answers, whatever the probe says once it does, and unknown after a failed probe.
  describe.each([
    ['support', 'relay.unattended.v1'],
    ['quotaSupport', 'team.relay_quota.v1'],
    ['maxMembersSupport', 'team.max_members.v1'],
    ['editSupport', 'team.edit.v1'],
  ] as const)('the %s flag follows the connection', (flag, cap) => {
    const ALL = ['relay.unattended.v1', 'team.relay_quota.v1', 'team.max_members.v1', 'team.edit.v1']
    const FULL = info(ALL)
    const WITHOUT_IT = info(ALL.filter((c) => c !== cap))
    const read = () => useUnattendedStore.getState().byHost.h1?.[flag]
    const setStatus = (status: 'connected' | 'disconnected') => useHostStore.setState({ runtime: { h1: { status } } })

    it('a disconnect makes it unknown; a reconnect probes before it is "yes" again', async () => {
      setStatus('connected')
      fetchHostInfo.mockResolvedValue(FULL)
      stop = startUnattendedSupport()
      await flush()
      expect(read()).toBe('yes')
      setStatus('disconnected')
      expect(read()).toBe('unknown')
      const d = deferred<HostInfo>()
      fetchHostInfo.mockReturnValueOnce(d.promise)
      setStatus('connected')
      expect(read()).toBe('unknown') // reconnected, not answered yet: the feature is not offered
      d.resolve(FULL)
      await flush()
      expect(read()).toBe('yes')
    })

    it('an answer still on its way when the host disconnects does not set it', async () => {
      const d = deferred<HostInfo>()
      fetchHostInfo.mockReturnValueOnce(d.promise)
      setStatus('connected')
      stop = startUnattendedSupport()
      setStatus('disconnected')
      d.resolve(FULL)
      await flush()
      expect(read()).not.toBe('yes')
    })

    it('a daemon that dropped the capability reads "no" after the reconnect', async () => {
      setStatus('connected')
      fetchHostInfo.mockResolvedValue(FULL)
      stop = startUnattendedSupport()
      await flush()
      setStatus('disconnected')
      fetchHostInfo.mockResolvedValue(WITHOUT_IT)
      setStatus('connected')
      await flush()
      expect(read()).toBe('no')
    })

    it('a failed probe leaves it unknown, never the old "yes"', async () => {
      setStatus('connected')
      fetchHostInfo.mockResolvedValue(FULL)
      stop = startUnattendedSupport()
      await flush()
      setStatus('disconnected')
      fetchHostInfo.mockRejectedValue(new Error('down'))
      setStatus('connected')
      await flush()
      expect(read()).toBe('unknown')
    })

    it('a removed host takes it with it', async () => {
      setStatus('connected')
      fetchHostInfo.mockResolvedValue(FULL)
      stop = startUnattendedSupport()
      await flush()
      const { h1: _gone, ...rest } = useHostStore.getState().hosts
      useHostStore.setState({ hosts: rest })
      expect(read()).toBeUndefined()
    })
  })

  it('a disconnect keeps the switch\'s last state (it is not a capability)', async () => {
    useHostStore.setState({ runtime: { h1: { status: 'connected' } } })
    fetchHostInfo.mockResolvedValue(WITH)
    stop = startUnattendedSupport()
    await flush()
    useUnattendedStore.getState().applyState('h1', { on: true } as never)
    useHostStore.setState({ runtime: { h1: { status: 'disconnected' } } })
    expect(useUnattendedStore.getState().byHost.h1?.state).toEqual({ on: true })
  })

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

    it('the quota numbers a host confirmed are forgotten when the host is removed or re-pointed', async () => {
      stop = startUnattendedSupport()
      useRelayQuotaStore.getState().applyAnswer('h1', 'r1', { self_left: 2, member_pool_left: 1 }, 3)
      useRelayQuotaStore.getState().applyAnswer('h2', 'r1', { self_left: 5, member_pool_left: 0 }, 1)
      useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h1: host('h1', '100.64.0.9') } }) // re-point h1
      expect(Object.keys(useRelayQuotaStore.getState().confirmed).some((k) => k.startsWith('h1'))).toBe(false)
      expect(Object.keys(useRelayQuotaStore.getState().confirmed).some((k) => k.startsWith('h2'))).toBe(true)
      useHostStore.setState({ hosts: { h1: host('h1', '100.64.0.9') }, hostOrder: ['h1'] }) // remove h2
      expect(useRelayQuotaStore.getState().confirmed).toEqual({})
    })

    it('a removed host with only a GET in flight (nothing confirmed yet) is forgotten too, and its late answer is ignored', async () => {
      stop = startUnattendedSupport()
      useRelayQuotaStore.getState().beginGet('h2') // the panel's first read, still out
      useRelayQuotaStore.getState().setWrite('h2', 'r1', 'self_left', { desired: 4 })
      useHostStore.setState({ hosts: { h1: host('h1') }, hostOrder: ['h1'] }) // h2 removed
      expect(useRelayQuotaStore.getState().gets).toEqual({})
      expect(useRelayQuotaStore.getState().writes).toEqual({})
      useRelayQuotaStore.getState().endGet('h2', [{ session_id: 's', root_session_id: 'r1', address: 'x/y', is_lead: false, self_left: 9, member_pool_left: 0, rev: 1 }], 0)
      expect(useRelayQuotaStore.getState().confirmed).toEqual({})
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
