import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as devicesApi from './devices-api'
import { resolvePendingTarget, retryPendingRevocations, startPendingRevocationRetry } from './pending-revocation-retry'
import { useHostStore } from '../stores/useHostStore'
import { usePendingRevocationsStore } from '../stores/usePendingRevocationsStore'

let revoke: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  localStorage.clear()
  usePendingRevocationsStore.setState({ items: [] })
  useHostStore.setState({
    hosts: {
      a: { id: 'a', name: 'a', ip: '1.1.1.1', port: 1, order: 0, token: 'ta', daemonId: 'Da' },
      b: { id: 'b', name: 'b', ip: '1.1.1.2', port: 1, order: 1, token: 'tb', daemonId: 'Db' },
    },
    hostOrder: ['a', 'b'],
    runtime: { a: { status: 'disconnected' }, b: { status: 'disconnected' } },
  })
  revoke = vi.spyOn(devicesApi, 'revokePairing').mockResolvedValue({ kind: 'ok' })
})
afterEach(() => {
  vi.restoreAllMocks()
})

describe('retryPendingRevocations', () => {
  it('calls revokePairing for every pending entry and removes those answered 204 or unsupported', async () => {
    const s = usePendingRevocationsStore.getState()
    s.add('a', 'P1')
    s.add('b', 'P1')
    revoke.mockImplementation(async (hostId: string) => (hostId === 'a' ? { kind: 'ok' } : { kind: 'unsupported' }))
    await retryPendingRevocations()
    expect(revoke).toHaveBeenCalledTimes(2)
    expect(usePendingRevocationsStore.getState().items).toEqual([])
  })

  it('keeps an entry whose host failed', async () => {
    usePendingRevocationsStore.getState().add('a', 'P1')
    revoke.mockResolvedValue({ kind: 'failed', reason: 'network', status: 0 })
    await retryPendingRevocations()
    expect(usePendingRevocationsStore.getState().has('a', 'P1')).toBe(true)
  })

  it('B: keeps an entry whose host no longer exists (the only record that a phone may still be valid), sending nothing', async () => {
    usePendingRevocationsStore.setState({ items: [{ hostId: 'gone', pairingId: 'P1', endpoint: '9.9.9.9:1', createdAt: 1 }] })
    await retryPendingRevocations()
    expect(revoke).not.toHaveBeenCalled()
    expect(usePendingRevocationsStore.getState().has('gone', 'P1')).toBe(true)
    expect(resolvePendingTarget(usePendingRevocationsStore.getState().items[0])).toEqual({ kind: 'removed' })
  })

  it('A: a host re-pointed at another ip:port is not retried, the entry stays flagged', async () => {
    usePendingRevocationsStore.getState().add('a', 'P1')
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, a: { ...s.hosts.a, ip: '8.8.8.8' } } }))
    await retryPendingRevocations()
    await retryPendingRevocations('a')
    expect(revoke).not.toHaveBeenCalled()
    expect(usePendingRevocationsStore.getState().has('a', 'P1')).toBe(true)
    expect(resolvePendingTarget(usePendingRevocationsStore.getState().items[0])).toEqual({ kind: 'repointed' })
  })

  it('A: same endpoint but another daemonId is also a re-point', async () => {
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, a: { ...s.hosts.a, daemonId: 'D1' } } }))
    usePendingRevocationsStore.getState().add('a', 'P1')
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, a: { ...s.hosts.a, daemonId: 'D2' } } }))
    await retryPendingRevocations()
    expect(revoke).not.toHaveBeenCalled()
    expect(usePendingRevocationsStore.getState().has('a', 'P1')).toBe(true)
  })

  it('A: a changed port with the same daemonId is also a re-point', async () => {
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, a: { ...s.hosts.a, daemonId: 'D1' } } }))
    usePendingRevocationsStore.getState().add('a', 'P1')
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, a: { ...s.hosts.a, port: 2 } } }))
    await retryPendingRevocations()
    expect(revoke).not.toHaveBeenCalled()
  })

  it('A: the stored host id is gone but another host has the same daemonId (re-added): retried against it, entry removed on 204', async () => {
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, a: { ...s.hosts.a, daemonId: 'D1' } } }))
    usePendingRevocationsStore.getState().add('a', 'P1')
    useHostStore.setState((s) => {
      const { a: _a, ...rest } = s.hosts
      return {
        hosts: { ...rest, a2: { id: 'a2', name: 'a again', ip: '1.1.1.9', port: 5, order: 0, token: 'x', daemonId: 'D1' } },
        hostOrder: ['a2', 'b'],
      }
    })
    await retryPendingRevocations()
    expect(revoke).toHaveBeenCalledTimes(1)
    expect(revoke).toHaveBeenCalledWith('a2', 'P1')
    expect(usePendingRevocationsStore.getState().items).toEqual([])
  })

  it('A: a connect of the re-added host triggers the retry', async () => {
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, a: { ...s.hosts.a, daemonId: 'D1' } } }))
    usePendingRevocationsStore.getState().add('a', 'P1')
    useHostStore.setState((s) => {
      const { a: _a, ...rest } = s.hosts
      return { hosts: { ...rest, a2: { id: 'a2', name: 'a again', ip: '1.1.1.9', port: 5, order: 0, token: 'x', daemonId: 'D1' } } }
    })
    const stop = startPendingRevocationRetry()
    useHostStore.setState({ runtime: { a2: { status: 'connected' }, b: { status: 'disconnected' } } })
    await vi.waitFor(() => expect(revoke).toHaveBeenCalledWith('a2', 'P1'))
    stop()
  })

  it('1: an entry made when the host had no daemonId is never auto-retried, even if a daemonId shows up later', async () => {
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, a: { ...s.hosts.a, daemonId: undefined } } }))
    usePendingRevocationsStore.getState().add('a', 'P1')
    expect(usePendingRevocationsStore.getState().items[0].daemonId).toBeUndefined()
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, a: { ...s.hosts.a, daemonId: 'Dnew' } } }))
    await retryPendingRevocations()
    expect(revoke).not.toHaveBeenCalled()
    expect(usePendingRevocationsStore.getState().has('a', 'P1')).toBe(true)
    expect(resolvePendingTarget(usePendingRevocationsStore.getState().items[0])).toEqual({ kind: 'unverifiable' })
  })

  it('1: a stored daemonId but a host that now has none is unverifiable too', async () => {
    usePendingRevocationsStore.getState().add('a', 'P1')
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, a: { ...s.hosts.a, daemonId: undefined } } }))
    await retryPendingRevocations()
    expect(revoke).not.toHaveBeenCalled()
    expect(resolvePendingTarget(usePendingRevocationsStore.getState().items[0])).toEqual({ kind: 'unverifiable' })
  })

  it.each([
    ['first', ['x1', 'x2']],
    ['second', ['x2', 'x1']],
  ])('2: two hosts with the stored daemonId (%s order) are ambiguous: nothing sent, entry kept', async (_n, order) => {
    usePendingRevocationsStore.getState().add('a', 'P1')
    const mk = (id: string) => ({ id, name: id, ip: `2.2.2.${id.length}${id.slice(-1)}`, port: 5, order: 0, token: 'x', daemonId: 'Da' })
    useHostStore.setState({
      hosts: Object.fromEntries([...order.map((id) => [id, mk(id)]), ['b', useHostStore.getState().hosts.b]]),
      hostOrder: [...order, 'b'],
    })
    await retryPendingRevocations()
    expect(revoke).not.toHaveBeenCalled()
    expect(usePendingRevocationsStore.getState().has('a', 'P1')).toBe(true)
    expect(resolvePendingTarget(usePendingRevocationsStore.getState().items[0])).toEqual({ kind: 'ambiguous' })
  })

  it('3: DNS case and IPv6 spellings of the same endpoint are not a re-point; a real change still is', async () => {
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, a: { ...s.hosts.a, ip: 'MLab.Example.com', port: 7860 } } }))
    usePendingRevocationsStore.getState().add('a', 'P1')
    expect(usePendingRevocationsStore.getState().items[0].endpoint).toBe('mlab.example.com:7860')
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, a: { ...s.hosts.a, ip: 'mlab.EXAMPLE.com' } } }))
    expect(resolvePendingTarget(usePendingRevocationsStore.getState().items[0])).toEqual({ kind: 'retry', hostId: 'a' })
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, a: { ...s.hosts.a, ip: 'mlab.example.com', port: 7861 } } }))
    expect(resolvePendingTarget(usePendingRevocationsStore.getState().items[0])).toEqual({ kind: 'repointed' })
  })

  it('3: a raw-form stored endpoint still matches the canonical form at compare time (IPv6 expanded vs compressed)', async () => {
    usePendingRevocationsStore.setState({
      items: [{ hostId: 'a', pairingId: 'P1', endpoint: '[0:0:0:0:0:0:0:1]:7860', daemonId: 'Da' }],
    })
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, a: { ...s.hosts.a, ip: '[::1]', port: 7860 } } }))
    expect(resolvePendingTarget(usePendingRevocationsStore.getState().items[0])).toEqual({ kind: 'retry', hostId: 'a' })
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, a: { ...s.hosts.a, ip: '[::2]' } } }))
    expect(resolvePendingTarget(usePendingRevocationsStore.getState().items[0])).toEqual({ kind: 'repointed' })
  })

  it('legacy entry without an endpoint is never retried automatically', async () => {
    usePendingRevocationsStore.setState({ items: [{ hostId: 'a', pairingId: 'P1' }] })
    await retryPendingRevocations()
    expect(revoke).not.toHaveBeenCalled()
    expect(usePendingRevocationsStore.getState().has('a', 'P1')).toBe(true)
    expect(resolvePendingTarget(usePendingRevocationsStore.getState().items[0])).toEqual({ kind: 'unverifiable' })
    const stop = startPendingRevocationRetry()
    useHostStore.setState({ runtime: { a: { status: 'connected' }, b: { status: 'disconnected' } } })
    await Promise.resolve()
    expect(revoke).not.toHaveBeenCalled()
    stop()
  })

  it('limits to one host when asked', async () => {
    const s = usePendingRevocationsStore.getState()
    s.add('a', 'P1')
    s.add('b', 'P2')
    await retryPendingRevocations('b')
    expect(revoke).toHaveBeenCalledTimes(1)
    expect(revoke).toHaveBeenCalledWith('b', 'P2')
    expect(usePendingRevocationsStore.getState().items).toMatchObject([{ hostId: 'a', pairingId: 'P1' }])
  })

  it('does not double-send an entry that is already in flight', async () => {
    usePendingRevocationsStore.getState().add('a', 'P1')
    let release: (v: devicesApi.RevokeResult) => void = () => {}
    revoke.mockReturnValue(new Promise((r) => (release = r)))
    const first = retryPendingRevocations()
    const second = retryPendingRevocations()
    release({ kind: 'ok' })
    await Promise.all([first, second])
    expect(revoke).toHaveBeenCalledTimes(1)
  })

  it('does nothing and arms no timer when the list is empty', async () => {
    vi.useFakeTimers()
    await retryPendingRevocations()
    expect(revoke).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
    vi.useRealTimers()
  })
})

describe('startPendingRevocationRetry', () => {
  it('retries a host once per transition to connected, not on other changes, and leaves no timer', async () => {
    vi.useFakeTimers()
    usePendingRevocationsStore.getState().add('a', 'P1')
    revoke.mockResolvedValue({ kind: 'failed', reason: 'network', status: 0 })
    const stop = startPendingRevocationRetry()
    useHostStore.setState({ runtime: { a: { status: 'reconnecting' }, b: { status: 'disconnected' } } })
    expect(revoke).not.toHaveBeenCalled()
    useHostStore.setState({ runtime: { a: { status: 'connected' }, b: { status: 'disconnected' } } })
    await vi.advanceTimersByTimeAsync(0)
    expect(revoke).toHaveBeenCalledTimes(1)
    // A heartbeat-like update that keeps `a` connected is not a transition.
    useHostStore.setState({ runtime: { a: { status: 'connected', latency: 3 } as never, b: { status: 'disconnected' } } })
    await vi.advanceTimersByTimeAsync(0)
    expect(revoke).toHaveBeenCalledTimes(1)
    // Another host's transition does not retry a's entry.
    useHostStore.setState({ runtime: { a: { status: 'connected' }, b: { status: 'connected' } } })
    await vi.advanceTimersByTimeAsync(0)
    expect(revoke).toHaveBeenCalledTimes(1)
    // Down and up again: one more attempt.
    useHostStore.setState({ runtime: { a: { status: 'disconnected' }, b: { status: 'connected' } } })
    useHostStore.setState({ runtime: { a: { status: 'connected' }, b: { status: 'connected' } } })
    await vi.advanceTimersByTimeAsync(0)
    expect(revoke).toHaveBeenCalledTimes(2)
    expect(vi.getTimerCount()).toBe(0)
    stop()
    vi.useRealTimers()
  })

  it('removes the entry when the retry on connect answers 204', async () => {
    usePendingRevocationsStore.getState().add('a', 'P1')
    const stop = startPendingRevocationRetry()
    useHostStore.setState({ runtime: { a: { status: 'connected' }, b: { status: 'disconnected' } } })
    await vi.waitFor(() => expect(usePendingRevocationsStore.getState().items).toEqual([]))
    stop()
  })

  it('the returned function unsubscribes', async () => {
    usePendingRevocationsStore.getState().add('a', 'P1')
    const stop = startPendingRevocationRetry()
    stop()
    useHostStore.setState({ runtime: { a: { status: 'connected' }, b: { status: 'disconnected' } } })
    await Promise.resolve()
    expect(revoke).not.toHaveBeenCalled()
  })
})
