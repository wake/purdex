import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

const fetchWatchStatus = vi.fn()
vi.mock('./api', () => ({ fetchWatchStatus: (...a: unknown[]) => fetchWatchStatus(...a) }))
import { useHostStore } from '../../stores/useHostStore'
import { deriveGuardian, startWatchStatus, useWatchStatusStore, STATUS_POLL_MS, type WatchEntry } from './status-store'
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

describe('startWatchStatus', () => {
  let stop: () => void
  beforeEach(() => {
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

  it('reads again every 30 s while visible', async () => {
    setHosts(['h1'], ['h1'])
    stop = startWatchStatus()
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchWatchStatus).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(STATUS_POLL_MS)
    expect(fetchWatchStatus).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(STATUS_POLL_MS)
    expect(fetchWatchStatus).toHaveBeenCalledTimes(3)
  })

  it('does not read while the window is hidden, and reads at once when it shows again', async () => {
    setHosts(['h1'], ['h1'])
    stop = startWatchStatus()
    await vi.advanceTimersByTimeAsync(0)
    Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true })
    await vi.advanceTimersByTimeAsync(STATUS_POLL_MS * 3)
    expect(fetchWatchStatus).toHaveBeenCalledTimes(1)
    Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchWatchStatus).toHaveBeenCalledTimes(2)
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
})
