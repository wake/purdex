import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

const fetchWatchStatus = vi.fn()
vi.mock('./api', () => ({ fetchWatchStatus: (...a: unknown[]) => fetchWatchStatus(...a) }))
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { deriveGuardian, startWatchStatus, rereadWatchStatus, useWatchStatusStore, type WatchEntry } from './status-store'
import type { WatchStatus } from './status-types'

const st = (host_id: string, state: 'awake' | 'resting' | 'none', since?: string, extra: Partial<WatchStatus> = {}): WatchStatus => ({
  self: { host_id, state, since, session_id: state === 'none' ? undefined : `sess-${host_id}` },
  enabled: true, auto_wake: true, mode: 'patrol', targets: [], ...extra,
})
const ok = (status: WatchStatus): WatchEntry => ({ kind: 'ok', status, at: 1 })
const where = (host_id: string, state: 'awake' | 'resting') => ({ alias: host_id, host_id, session_id: 'x', state })

describe('deriveGuardian', () => {
  it('1. awake hosts: the earliest since wins', () => {
    const g = deriveGuardian({
      a: ok(st('da', 'awake', '2026-10-16T02:00:00Z')),
      b: ok(st('db', 'awake', '2026-10-16T01:00:00Z')),
    })
    expect(g).toMatchObject({ state: 'awake', hostId: 'db', appHostId: 'b', reachable: true })
  })
  it('1. equal since: the smaller host id wins', () => {
    const g = deriveGuardian({
      a: ok(st('db', 'awake', '2026-10-16T01:00:00Z')),
      b: ok(st('da', 'awake', '2026-10-16T01:00:00Z')),
    })
    expect(g.hostId).toBe('da')
  })
  it('1. an awake host beats a resting one', () => {
    const g = deriveGuardian({ a: ok(st('da', 'resting', '2026-10-16T05:00:00Z')), b: ok(st('db', 'awake', '2026-10-16T09:00:00Z')) })
    expect(g).toMatchObject({ state: 'awake', hostId: 'db' })
  })
  it('1. a Go zero since is unknown and loses to a real one', () => {
    const g = deriveGuardian({ a: ok(st('da', 'awake', '0001-01-01T00:00:00Z')), b: ok(st('db', 'awake', '2026-10-16T01:00:00Z')) })
    expect(g.hostId).toBe('db')
  })
  it('2. else a resting self: the latest since', () => {
    const g = deriveGuardian({ a: ok(st('da', 'resting', '2026-10-16T01:00:00Z')), b: ok(st('db', 'resting', '2026-10-16T03:00:00Z')) })
    expect(g).toMatchObject({ state: 'resting', hostId: 'db', reachable: true })
  })
  it('3. else a current that is awake or resting: not reachable', () => {
    const g = deriveGuardian({ a: ok(st('da', 'none', undefined, { current: where('far', 'resting') })) })
    expect(g).toMatchObject({ state: 'resting', hostId: 'far', reachable: false })
    expect(g.appHostId).toBeUndefined()
  })
  it('4. none when at least one host answered ok', () => {
    expect(deriveGuardian({ a: ok(st('da', 'none')), b: { kind: 'unsupported' } })).toMatchObject({ state: 'none', reachable: true })
  })
  it('a 404 host plus an ok host gives none', () => {
    expect(deriveGuardian({ a: { kind: 'unsupported' }, b: ok(st('db', 'none')) }).state).toBe('none')
  })
  it('5. unknown when no host answered ok', () => {
    expect(deriveGuardian({}).state).toBe('unknown')
    expect(deriveGuardian({ a: { kind: 'unsupported' }, b: { kind: 'error' } }).state).toBe('unknown')
  })
  it('carries the holder mode and the tmux session of its own watcher view', () => {
    const g = deriveGuardian({ a: ok(st('da', 'awake', '2026-10-16T01:00:00Z', { mode: 'resident', watcher: { host_id: 'da', alias: 'mlab', session_id: 's', state: 'awake', tmux_session: 'pdx-watch-2' } })) })
    expect(g).toMatchObject({ mode: 'resident', alias: 'mlab', tmuxSession: 'pdx-watch-2' })
  })
  it('uses pdx-watch until the status carries the tmux session', () => {
    expect(deriveGuardian({ a: ok(st('da', 'awake', '2026-10-16T01:00:00Z')) }).tmuxSession).toBe('pdx-watch')
  })
})

const setHosts = (ids: string[], connected: string[]) => useHostStore.setState({
  hosts: Object.fromEntries(ids.map((id) => [id, { id, name: id, ip: '1', port: 1, token: 't', order: 0 }])),
  hostOrder: ids,
  runtime: Object.fromEntries(connected.map((id) => [id, { status: 'connected' }])),
} as never)

/** What the nex host store holds for a host whose `/api/info` answered with these capabilities (the ensure itself is stubbed). */
const infoOf = (...caps: string[]) => ({ info: null, capabilities: null, phase: 'unavailable', error: null, fetchedAt: 1, generation: 1, fingerprint: 'f', daemonCapabilities: caps })
const declare = (...ids: string[]) => useNexHostStore.setState({
  byHost: Object.fromEntries(ids.map((id) => [id, infoOf('watch.v1')])),
  ensure: async () => {},
} as never)

describe('startWatchStatus', () => {
  let stop: () => void
  beforeEach(() => {
    declare('h1', 'h2')
    vi.useFakeTimers()
    fetchWatchStatus.mockReset()
    fetchWatchStatus.mockResolvedValue({ kind: 'ok', status: st('da', 'awake', '2026-10-16T01:00:00Z') })
    useWatchStatusStore.getState().reset()
    setHosts(['h1'], [])
    Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
  })
  afterEach(() => { stop?.(); vi.useRealTimers() })

  it('reads a host when it connects, and only then', async () => {
    stop = startWatchStatus()
    expect(fetchWatchStatus).not.toHaveBeenCalled()
    setHosts(['h1'], ['h1'])
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchWatchStatus).toHaveBeenCalledTimes(1)
    expect(fetchWatchStatus).toHaveBeenCalledWith('h1')
    expect(useWatchStatusStore.getState().byHost.h1.kind).toBe('ok')
  })

  it('has no timer: nothing is read again by the passing of time or by the window showing', async () => {
    setHosts(['h1'], ['h1'])
    stop = startWatchStatus()
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchWatchStatus).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(10 * 60_000)
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchWatchStatus).toHaveBeenCalledTimes(1)
  })

  it('a watch.changed push reads that host again, and only a connected one', async () => {
    setHosts(['h1', 'h2'], ['h1'])
    stop = startWatchStatus()
    await vi.advanceTimersByTimeAsync(0)
    fetchWatchStatus.mockResolvedValue({ kind: 'ok', status: st('da', 'resting', '2026-10-16T03:00:00Z') })
    rereadWatchStatus('h1')
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchWatchStatus).toHaveBeenCalledTimes(2)
    const e = useWatchStatusStore.getState().byHost.h1
    expect(e.kind === 'ok' && e.status.self.state).toBe('resting')
    rereadWatchStatus('h2')
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchWatchStatus).toHaveBeenCalledTimes(2)
  })

  it('of two reads in flight the newer one wins, whichever answers last', async () => {
    setHosts(['h1'], ['h1'])
    let first!: (v: unknown) => void
    fetchWatchStatus.mockReturnValueOnce(new Promise((r) => { first = r }))
    stop = startWatchStatus()
    await vi.advanceTimersByTimeAsync(0) // the first read has reached the status request (it waits for /api/info first)
    fetchWatchStatus.mockResolvedValueOnce({ kind: 'ok', status: st('da', 'resting', '2026-10-16T03:00:00Z') })
    rereadWatchStatus('h1')
    await vi.advanceTimersByTimeAsync(0)
    first({ kind: 'ok', status: st('da', 'awake', '2026-10-16T01:00:00Z') })
    await vi.advanceTimersByTimeAsync(0)
    const e = useWatchStatusStore.getState().byHost.h1
    expect(e.kind === 'ok' && e.status.self.state).toBe('resting')
  })

  it('a push after stop does nothing', async () => {
    setHosts(['h1'], ['h1'])
    stop = startWatchStatus()
    await vi.advanceTimersByTimeAsync(0)
    stop()
    rereadWatchStatus('h1')
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchWatchStatus).toHaveBeenCalledTimes(1)
  })

  it('forgets a host that leaves, and drops an answer that lands after it left', async () => {
    let resolve!: (v: unknown) => void
    fetchWatchStatus.mockReturnValueOnce(new Promise((r) => { resolve = r }))
    setHosts(['h1'], ['h1'])
    stop = startWatchStatus()
    setHosts([], [])
    resolve({ kind: 'ok', status: st('da', 'awake') })
    await vi.advanceTimersByTimeAsync(0)
    expect(useWatchStatusStore.getState().byHost.h1).toBeUndefined()
  })

  it('forgets the answer of a host that disconnects', async () => {
    setHosts(['h1'], ['h1'])
    stop = startWatchStatus()
    await vi.advanceTimersByTimeAsync(0)
    expect(useWatchStatusStore.getState().byHost.h1).toBeDefined()
    setHosts(['h1'], [])
    expect(useWatchStatusStore.getState().byHost.h1).toBeUndefined()
  })

  it('a host whose /api/info does not list watch.v1 is unsupported, and its status is never read', async () => {
    useNexHostStore.setState({ byHost: { h1: infoOf('fleet.v1') }, ensure: async () => {} } as never)
    setHosts(['h1'], ['h1'])
    stop = startWatchStatus()
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchWatchStatus).not.toHaveBeenCalled()
    expect(useWatchStatusStore.getState().byHost.h1.kind).toBe('unsupported')
  })

  it('reads /api/info through the nex host store first, and only reads the status when it lists watch.v1', async () => {
    let release!: () => void
    useNexHostStore.setState({ byHost: {}, ensure: () => new Promise<void>((r) => { release = () => { useNexHostStore.setState({ byHost: { h1: infoOf('watch.v1') } } as never); r() } }) } as never)
    setHosts(['h1'], ['h1'])
    stop = startWatchStatus()
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchWatchStatus).not.toHaveBeenCalled()
    expect(useWatchStatusStore.getState().byHost.h1).toBeUndefined()
    release()
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchWatchStatus).toHaveBeenCalledTimes(1)
    expect(useWatchStatusStore.getState().byHost.h1.kind).toBe('ok')
  })

  // `/api/info` not read (the nex host store's entry has no capabilities) is "not known yet", not "not a watch host".
  const flaky = (okFrom: number) => {
    let calls = 0
    useNexHostStore.setState({ byHost: {}, ensure: async () => { calls++; if (calls >= okFrom) useNexHostStore.setState({ byHost: { h1: infoOf('watch.v1') } } as never) } } as never)
    return () => calls
  }

  it('an /api/info that could not be read is not written as unsupported: it is asked again, and the status follows', async () => {
    const calls = flaky(2)
    setHosts(['h1'], ['h1'])
    stop = startWatchStatus()
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchWatchStatus).not.toHaveBeenCalled()
    expect(useWatchStatusStore.getState().byHost.h1).toBeUndefined() // not settled, so nothing downstream decides on it
    await vi.advanceTimersByTimeAsync(5_000)
    expect(calls()).toBe(2)
    expect(fetchWatchStatus).toHaveBeenCalledTimes(1)
    expect(useWatchStatusStore.getState().byHost.h1.kind).toBe('ok')
  })

  it('asks again after 5 s, 15 s and 60 s, then settles on an error (not unsupported)', async () => {
    const calls = flaky(99)
    setHosts(['h1'], ['h1'])
    stop = startWatchStatus()
    await vi.advanceTimersByTimeAsync(0)
    expect(calls()).toBe(1)
    await vi.advanceTimersByTimeAsync(5_000)
    expect(calls()).toBe(2)
    await vi.advanceTimersByTimeAsync(15_000)
    expect(calls()).toBe(3)
    expect(useWatchStatusStore.getState().byHost.h1).toBeUndefined()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(calls()).toBe(4)
    expect(useWatchStatusStore.getState().byHost.h1.kind).toBe('error')
    await vi.advanceTimersByTimeAsync(10 * 60_000)
    expect(calls()).toBe(4) // bounded: it stops
    expect(fetchWatchStatus).not.toHaveBeenCalled()
  })

  it('a retry of a host that has since disconnected or been re-pointed does nothing', async () => {
    const calls = flaky(99)
    setHosts(['h1'], ['h1'])
    stop = startWatchStatus()
    await vi.advanceTimersByTimeAsync(0)
    setHosts(['h1'], [])
    await vi.advanceTimersByTimeAsync(5_000)
    expect(calls()).toBe(1)
  })

  it('a /api/info that was read and does not list watch.v1 is unsupported at once, with no retry', async () => {
    useNexHostStore.setState({ byHost: { h1: infoOf('fleet.v1') }, ensure: async () => {} } as never)
    setHosts(['h1'], ['h1'])
    stop = startWatchStatus()
    await vi.advanceTimersByTimeAsync(0)
    expect(useWatchStatusStore.getState().byHost.h1.kind).toBe('unsupported')
  })
})
