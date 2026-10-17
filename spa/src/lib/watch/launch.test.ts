// W5e-2: the launch panel's two requests (啟動守門員 / 叫醒守門員) and the state they leave behind. The panel's own tests are in
// components/watch/WatchLaunchPanel.buttons.test.tsx; this file is the request, every answer of the daemon, and what ends "busy".
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

const hostFetch = vi.hoisted(() => vi.fn())
const fetchWatchStatus = vi.hoisted(() => vi.fn())
vi.mock('../host-api', async (orig) => ({ ...(await orig<typeof import('../host-api')>()), hostFetch: (...a: unknown[]) => hostFetch(...a) }))
vi.mock('./api', () => ({ fetchWatchStatus: (...a: unknown[]) => fetchWatchStatus(...a) }))
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { startWatchStatus, useWatchStatusStore, type WatchEntry } from './status-store'
import { handleWatchEvent } from './watch-ws'
import { isLaunchBusy, isLaunchLocked, requestLaunch, startTarget, startWatchLaunch, useWatchLaunchStore, WATCH_LAUNCH_TIMEOUT_MS } from './launch'
import type { WatchStatus } from './status-types'

const status = (hostId: string, state: 'awake' | 'resting' | 'none'): WatchStatus =>
  ({ self: { host_id: hostId, state, since: '2026-10-16T01:00:00Z', session_id: state === 'none' ? undefined : 'conv-1' }, enabled: true, auto_wake: true, mode: 'patrol', targets: [] })
const answer = (appHost: string, s: WatchStatus) => useWatchStatusStore.setState((st) => ({ byHost: { ...st.byHost, [appHost]: { kind: 'ok', status: s, at: 1 } } }))
const reply = (status: number, body: unknown) => hostFetch.mockResolvedValueOnce(new Response(JSON.stringify(body), { status }))
const infoOf = (...caps: string[]) => ({ info: null, capabilities: null, phase: 'unavailable', error: null, fetchedAt: 1, generation: 1, fingerprint: 'f', daemonCapabilities: caps })
const flush = () => new Promise((r) => setTimeout(r, 0))
const at = (ip: string, token = 't') => useHostStore.setState({
  hosts: { h1: { id: 'h1', name: 'mlab', ip, port: 1, token, order: 0, daemonId: 'd1' } }, hostOrder: ['h1'], runtime: { h1: { status: 'connected' } },
} as never) // the runtime stays connected on a re-point: that is the case
const slot = () => useWatchLaunchStore.getState().slot
const deferred = <T,>() => { let resolve!: (v: T) => void; const promise = new Promise<T>((r) => { resolve = r }); return { promise, resolve } }

let stops: Array<() => void> = []
beforeEach(() => {
  hostFetch.mockReset(); fetchWatchStatus.mockReset()
  useWatchLaunchStore.setState({ slot: null, seq: 0 })
  useWatchStatusStore.getState().reset()
  useNexHostStore.setState({ byHost: { h1: infoOf('watch.v1') }, ensure: async () => {} } as never)
  at('1.1.1.1')
})
afterEach(() => { for (const s of stops) s(); stops = [] })

describe('啟動守門員 → POST /api/watch/start', () => {
  it('posts to the host asked, and is busy while the request is on its way', async () => {
    const wait = deferred<Response>()
    hostFetch.mockReturnValueOnce(wait.promise)
    const p = requestLaunch('start', 'h1')
    expect(hostFetch).toHaveBeenCalledTimes(1)
    expect(hostFetch.mock.calls[0][0]).toBe('h1')
    expect(hostFetch.mock.calls[0][1]).toBe('/api/watch/start')
    expect(hostFetch.mock.calls[0][2]).toMatchObject({ method: 'POST' })
    expect(isLaunchBusy(slot())).toBe(true)
    wait.resolve(new Response(JSON.stringify({ host: 'mlab', host_id: 'd1' }), { status: 202 }))
    await p
  })

  it('200: a watcher is already awake — a plain note, not busy, and the status is read again', async () => {
    stops.push(startWatchStatus())
    fetchWatchStatus.mockResolvedValue({ kind: 'ok', status: status('d1', 'none') })
    await flush()
    expect(fetchWatchStatus).toHaveBeenCalledTimes(1)
    reply(200, { host: 'mlab', host_id: 'd1', state: 'awake', session_id: 's' })
    await requestLaunch('start', 'h1')
    expect(slot()).toMatchObject({ phase: 'awake_already', kind: 'start' })
    expect(isLaunchBusy(slot())).toBe(false)
    await flush()
    expect(fetchWatchStatus).toHaveBeenCalledTimes(2)
  })

  it('202: it is starting on the host the daemon names — busy, with that host kept', async () => {
    reply(202, { host: 'air26', host_id: 'd2' })
    await requestLaunch('start', 'h1')
    expect(slot()).toMatchObject({ phase: 'busy', kind: 'start', startingOn: { alias: 'air26', daemonId: 'd2' } })
    expect(isLaunchBusy(slot())).toBe(true)
  })

  it('409 cooldown: the time is the answer\'s `detail` (RFC 3339), and the button is free again', async () => {
    reply(409, { error: 'cooldown', detail: '2026-10-17T06:30:00Z' })
    await requestLaunch('start', 'h1')
    expect(slot()).toMatchObject({ phase: 'cooldown', kind: 'start', until: Date.parse('2026-10-17T06:30:00Z') })
    expect(isLaunchBusy(slot())).toBe(false)
  })

  it('409 cooldown with a `detail` that is not a time keeps the cooldown, without a time', async () => {
    reply(409, { error: 'cooldown', detail: 'later' })
    await requestLaunch('start', 'h1')
    const s = slot()
    expect(s?.phase).toBe('cooldown')
    expect(s?.phase === 'cooldown' && Number.isNaN(s.until)).toBe(true)
  })

  it('403: the daemon\'s own text is kept, and the button is free again', async () => {
    reply(403, { error: 'forbidden', detail: 'only the admin token starts the watcher' })
    await requestLaunch('start', 'h1')
    expect(slot()).toMatchObject({ phase: 'error', kind: 'start', detail: 'only the admin token starts the watcher', status: 403 })
    expect(isLaunchBusy(slot())).toBe(false)
  })

  it('an error without a readable body keeps its status', async () => {
    hostFetch.mockResolvedValueOnce(new Response('<html>bad gateway</html>', { status: 502 }))
    await requestLaunch('start', 'h1')
    expect(slot()).toMatchObject({ phase: 'error', status: 502 })
    expect((slot() as { detail?: string }).detail).toBeUndefined()
  })

  it('a request that never got an answer is a network error, and the button is free again', async () => {
    hostFetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))
    await requestLaunch('start', 'h1')
    expect(slot()).toMatchObject({ phase: 'error', kind: 'start', network: true })
    expect(isLaunchBusy(slot())).toBe(false)
  })
})

describe('叫醒守門員 → POST /api/watch/wake with an empty prompt', () => {
  it('posts `{}` (the wake prompt alone)', async () => {
    reply(200, { starting: false })
    await requestLaunch('wake', 'h1')
    expect(hostFetch.mock.calls[0][1]).toBe('/api/watch/wake')
    expect(hostFetch.mock.calls[0][2]).toMatchObject({ method: 'POST', body: '{}' })
  })

  it('200 and 202 are both success: busy until the watcher registers', async () => {
    reply(200, { starting: false })
    await requestLaunch('wake', 'h1')
    expect(slot()).toMatchObject({ phase: 'busy', kind: 'wake' })
    useWatchLaunchStore.setState({ slot: null })
    reply(202, { starting: true })
    await requestLaunch('wake', 'h1')
    expect(slot()).toMatchObject({ phase: 'busy', kind: 'wake' })
  })

  it('409 cooldown: the time is `detail`', async () => {
    reply(409, { error: 'cooldown', detail: '2026-10-17T06:30:00Z' })
    await requestLaunch('wake', 'h1')
    expect(slot()).toMatchObject({ phase: 'cooldown', kind: 'wake', until: Date.parse('2026-10-17T06:30:00Z') })
    expect(isLaunchBusy(slot())).toBe(false)
  })

  it('403: the daemon\'s own text', async () => {
    reply(403, { error: 'forbidden', detail: 'only the admin token wakes the watcher' })
    await requestLaunch('wake', 'h1')
    expect(slot()).toMatchObject({ phase: 'error', kind: 'wake', detail: 'only the admin token wakes the watcher' })
    expect(isLaunchBusy(slot())).toBe(false)
  })

  it('a network error frees the button', async () => {
    hostFetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))
    await requestLaunch('wake', 'h1')
    expect(slot()).toMatchObject({ phase: 'error', kind: 'wake', network: true })
    expect(isLaunchBusy(slot())).toBe(false)
  })
})

describe('busy', () => {
  it('a second press while a request is on its way or the watcher is coming up sends nothing', async () => {
    reply(202, { host: 'mlab', host_id: 'd1' })
    await requestLaunch('start', 'h1')
    await requestLaunch('start', 'h1')
    await requestLaunch('wake', 'h1')
    expect(hostFetch).toHaveBeenCalledTimes(1)
  })

  it('a cooldown or an error frees the button: it can be pressed again', async () => {
    reply(409, { error: 'cooldown', detail: '2026-10-17T06:30:00Z' })
    await requestLaunch('start', 'h1')
    reply(403, { error: 'forbidden', detail: 'no' })
    await requestLaunch('start', 'h1')
    reply(202, { host: 'mlab', host_id: 'd1' })
    await requestLaunch('start', 'h1')
    expect(hostFetch).toHaveBeenCalledTimes(3)
    expect(slot()?.phase).toBe('busy')
  })

  it('stays busy with no `watch.changed`: other status changes and time do not end it', async () => {
    stops.push(startWatchLaunch())
    reply(202, { host: 'mlab', host_id: 'd1' })
    await requestLaunch('start', 'h1')
    answer('h1', status('d1', 'none'))
    await flush()
    expect(isLaunchBusy(slot())).toBe(true)
  })

  it('a start ends when the watcher registers resting too (it has a session by then)', async () => {
    stops.push(startWatchLaunch())
    answer('h1', status('d1', 'none'))
    reply(202, { host: 'mlab', host_id: 'd1' })
    await requestLaunch('start', 'h1')
    expect(isLaunchBusy(slot())).toBe(true)
    answer('h1', status('d1', 'resting'))
    expect(slot()).toBeNull()
  })

  it('a start that was answered after the watcher registered resting leaves nothing to wait for', async () => {
    answer('h1', status('d1', 'none'))
    const wait = deferred<Response>()
    hostFetch.mockReturnValueOnce(wait.promise)
    const p = requestLaunch('start', 'h1')
    answer('h1', status('d1', 'resting'))
    wait.resolve(new Response(JSON.stringify({ host: 'mlab', host_id: 'd1' }), { status: 202 }))
    await p
    expect(slot()).toBeNull()
  })

  it('a start still ends when the watcher registers awake', async () => {
    stops.push(startWatchLaunch())
    answer('h1', status('d1', 'none'))
    reply(202, { host: 'mlab', host_id: 'd1' })
    await requestLaunch('start', 'h1')
    answer('h1', status('d1', 'awake'))
    expect(slot()).toBeNull()
  })

  it('a wake starts from resting: still resting is still busy, awake ends it', async () => {
    stops.push(startWatchLaunch())
    answer('h1', status('d1', 'resting'))
    reply(202, { starting: true })
    await requestLaunch('wake', 'h1')
    expect(isLaunchBusy(slot())).toBe(true)
    answer('h1', status('d1', 'resting')) // another read that still says resting
    expect(isLaunchBusy(slot())).toBe(true)
    answer('h1', status('d1', 'awake'))
    expect(slot()).toBeNull()
  })

  it('ends when the watcher registers: a `watch.changed` read that says awake', async () => {
    stops.push(startWatchStatus(), startWatchLaunch())
    fetchWatchStatus.mockResolvedValueOnce({ kind: 'ok', status: status('d1', 'none') })
    await flush()
    reply(202, { host: 'mlab', host_id: 'd1' })
    await requestLaunch('start', 'h1')
    expect(isLaunchBusy(slot())).toBe(true)
    fetchWatchStatus.mockResolvedValue({ kind: 'ok', status: status('d1', 'awake') })
    handleWatchEvent('h1', 'watch.changed')
    await flush()
    expect(slot()).toBeNull()
  })

  it('a watcher that registered while the request was on its way leaves nothing to wait for', async () => {
    const wait = deferred<Response>()
    hostFetch.mockReturnValueOnce(wait.promise)
    const p = requestLaunch('wake', 'h1')
    answer('h1', status('d1', 'awake')) // the installer is not running: the answer itself must see it
    wait.resolve(new Response('{}', { status: 202 }))
    await p
    expect(slot()).toBeNull()
  })
})

describe('re-point with the runtime still connected', () => {
  it('the answer of the old endpoint is not applied to the host that now stands there', async () => {
    const wait = deferred<Response>()
    hostFetch.mockReturnValueOnce(wait.promise)
    const p = requestLaunch('start', 'h1')
    at('2.2.2.2') // the installer is not running here: the identity check alone has to hold
    wait.resolve(new Response(JSON.stringify({ host: 'mlab', host_id: 'd1' }), { status: 202 }))
    await p
    expect(slot()?.phase).toBe('sending') // not 'busy'
  })

  it('a token change is a re-point too', async () => {
    const wait = deferred<Response>()
    hostFetch.mockReturnValueOnce(wait.promise)
    const p = requestLaunch('start', 'h1')
    at('1.1.1.1', 'other-token')
    wait.resolve(new Response(JSON.stringify({ host: 'mlab', host_id: 'd1' }), { status: 202 }))
    await p
    expect(slot()?.phase).toBe('sending')
  })

  it('a re-point drops a busy state of the old daemon (installed), and an old answer then lands nowhere', async () => {
    stops.push(startWatchLaunch())
    const wait = deferred<Response>()
    hostFetch.mockReturnValueOnce(wait.promise)
    const p = requestLaunch('start', 'h1')
    at('2.2.2.2')
    expect(slot()).toBeNull()
    wait.resolve(new Response(JSON.stringify({ host: 'mlab', host_id: 'd1' }), { status: 202 }))
    await p
    expect(slot()).toBeNull()
  })

  it('a busy state is dropped when its host is re-pointed, removed or disconnected', () => {
    stops.push(startWatchLaunch())
    const busy = () => useWatchLaunchStore.setState({ slot: { phase: 'busy', kind: 'start', hostId: 'h1', seq: 1 } })
    busy(); at('2.2.2.2'); expect(slot()).toBeNull()
    busy(); useHostStore.setState({ runtime: { h1: { status: 'disconnected' } } } as never); expect(slot()).toBeNull()
    at('2.2.2.2'); busy(); useHostStore.setState({ hosts: {}, hostOrder: [] } as never); expect(slot()).toBeNull()
  })

  it('an older request\'s answer does not land on a newer request', async () => {
    const old = deferred<Response>()
    hostFetch.mockReturnValueOnce(old.promise)
    const p = requestLaunch('start', 'h1')
    useWatchLaunchStore.setState({ slot: null }) // freed (as a host change would), then pressed again
    const next = deferred<Response>()
    hostFetch.mockReturnValueOnce(next.promise)
    const q = requestLaunch('start', 'h1')
    old.resolve(new Response(JSON.stringify({ host: 'mlab', host_id: 'd1' }), { status: 202 }))
    await p
    expect(slot()).toMatchObject({ phase: 'sending', seq: 2 })
    next.resolve(new Response(JSON.stringify({ error: 'cooldown', detail: '2026-10-17T06:30:00Z' }), { status: 409 }))
    await q
    expect(slot()?.phase).toBe('cooldown')
  })
})

describe('busy times out after 120 s without a registration', () => {
  // Only the timer pair is faked: the mocked requests and `res.json()` keep running on the real event loop.
  beforeEach(() => { vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] }) })
  afterEach(() => { vi.useRealTimers() })
  const goBusy = async (kind: 'start' | 'wake' = 'start') => {
    reply(202, { host: 'mlab', host_id: 'd1' })
    await requestLaunch(kind, 'h1')
    expect(slot()?.phase).toBe('busy')
  }

  it('still busy at 119_999 ms; at 120_000 ms it is released with a timeout phase, and the button can be pressed again', async () => {
    expect(WATCH_LAUNCH_TIMEOUT_MS).toBe(120_000)
    await goBusy()
    vi.advanceTimersByTime(119_999)
    expect(isLaunchBusy(slot())).toBe(true)
    vi.advanceTimersByTime(1)
    expect(slot()).toMatchObject({ phase: 'timeout', kind: 'start', hostId: 'h1' })
    expect(isLaunchBusy(slot())).toBe(false)
    reply(202, { host: 'mlab', host_id: 'd1' })
    await requestLaunch('start', 'h1')
    expect(hostFetch).toHaveBeenCalledTimes(2)
    expect(slot()).toMatchObject({ phase: 'busy', seq: 2 })
  })

  it('a wake times out the same way', async () => {
    await goBusy('wake')
    vi.advanceTimersByTime(120_000)
    expect(slot()).toMatchObject({ phase: 'timeout', kind: 'wake' })
  })

  it('the watcher registering within the 120 s ends busy, and the timer is gone: nothing is left to appear at the deadline', async () => {
    stops.push(startWatchLaunch())
    await goBusy()
    vi.advanceTimersByTime(60_000)
    answer('h1', status('d1', 'awake'))
    expect(slot()).toBeNull()
    expect(vi.getTimerCount()).toBe(0)
    vi.advanceTimersByTime(120_000)
    expect(slot()).toBeNull()
  })

  it('a start whose watcher registers resting ends busy, and the timer is gone: no timeout line at the deadline', async () => {
    stops.push(startWatchLaunch())
    answer('h1', status('d1', 'none'))
    await goBusy()
    vi.advanceTimersByTime(60_000)
    answer('h1', status('d1', 'resting'))
    expect(slot()).toBeNull()
    expect(vi.getTimerCount()).toBe(0)
    vi.advanceTimersByTime(120_000)
    expect(slot()).toBeNull()
  })

  it('the watcher registering clears the timeout line too', async () => {
    stops.push(startWatchLaunch())
    await goBusy()
    vi.advanceTimersByTime(120_000)
    expect(slot()?.phase).toBe('timeout')
    answer('h1', status('d1', 'awake'))
    expect(slot()).toBeNull()
  })

  it('pressing again after the timeout replaces the line, and its own 120 s start from that press', async () => {
    await goBusy()
    vi.advanceTimersByTime(120_000)
    expect(slot()?.phase).toBe('timeout')
    reply(202, { host: 'mlab', host_id: 'd1' })
    await requestLaunch('start', 'h1')
    expect(slot()).toMatchObject({ phase: 'busy', seq: 2 })
    vi.advanceTimersByTime(119_999)
    expect(slot()).toMatchObject({ phase: 'busy', seq: 2 })
    vi.advanceTimersByTime(1)
    expect(slot()).toMatchObject({ phase: 'timeout', seq: 2 })
  })

  it('an older request\'s timer does not release a newer request', async () => {
    await goBusy() // seq 1, armed at 0 s
    vi.advanceTimersByTime(50_000)
    useWatchLaunchStore.setState({ slot: null }) // freed (as a host change would), then pressed again
    expect(vi.getTimerCount()).toBe(0)
    await goBusy() // seq 2, armed at 50 s
    expect(vi.getTimerCount()).toBe(1)
    vi.advanceTimersByTime(70_000) // 120 s after the first request
    expect(slot()).toMatchObject({ phase: 'busy', seq: 2 })
    vi.advanceTimersByTime(49_999)
    expect(slot()).toMatchObject({ phase: 'busy', seq: 2 })
    vi.advanceTimersByTime(1)
    expect(slot()).toMatchObject({ phase: 'timeout', seq: 2 })
  })

  it('a busy slot of another request (no non-busy slot in between) takes the timer over: the old deadline does not carry over', async () => {
    await goBusy() // seq 1, armed at 0 s
    vi.advanceTimersByTime(50_000)
    useWatchLaunchStore.setState({ seq: 2, slot: { phase: 'busy', kind: 'start', hostId: 'h1', seq: 2 } }) // armed at 50 s
    expect(vi.getTimerCount()).toBe(1)
    vi.advanceTimersByTime(70_000)
    expect(slot()).toMatchObject({ phase: 'busy', seq: 2 })
    vi.advanceTimersByTime(50_000)
    expect(slot()).toMatchObject({ phase: 'timeout', seq: 2 })
  })

  // A host change may leave a timer of its own behind (not ours): `added` is how many a bare host change leaves, so that
  // "ours is gone" reads as: before + added - 1.
  const added = (change: () => void): number => { const n = vi.getTimerCount(); change(); const d = vi.getTimerCount() - n; at('1.1.1.1'); return d }

  it('a re-point of the host drops the busy state and its timer: the deadline changes nothing', async () => {
    stops.push(startWatchLaunch())
    const repoint = () => at('2.2.2.2')
    const k = added(repoint)
    await goBusy()
    const before = vi.getTimerCount()
    repoint()
    expect(slot()).toBeNull()
    expect(vi.getTimerCount()).toBe(before + k - 1)
    vi.advanceTimersByTime(120_000)
    expect(slot()).toBeNull()
  })

  it('a host that is removed or disconnected does the same', async () => {
    stops.push(startWatchLaunch())
    const disconnect = () => useHostStore.setState({ runtime: { h1: { status: 'disconnected' } } } as never)
    const remove = () => useHostStore.setState({ hosts: {}, hostOrder: [] } as never)
    const kDisconnect = added(disconnect)
    const kRemove = added(remove)
    await goBusy()
    let before = vi.getTimerCount()
    disconnect()
    expect(slot()).toBeNull()
    expect(vi.getTimerCount()).toBe(before + kDisconnect - 1)
    at('1.1.1.1')
    await goBusy()
    before = vi.getTimerCount()
    remove()
    expect(slot()).toBeNull()
    expect(vi.getTimerCount()).toBe(before + kRemove - 1)
    vi.advanceTimersByTime(120_000)
    expect(slot()).toBeNull()
  })

  it('a store reset drops the timer', async () => {
    await goBusy()
    useWatchLaunchStore.setState({ slot: null, seq: 0 })
    expect(vi.getTimerCount()).toBe(0)
  })

  it('cooldown and error answers arm nothing', async () => {
    reply(409, { error: 'cooldown', detail: '2026-10-17T06:30:00Z' })
    await requestLaunch('start', 'h1')
    expect(vi.getTimerCount()).toBe(0)
    reply(403, { error: 'forbidden', detail: 'no' })
    await requestLaunch('start', 'h1')
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('startTarget', () => {
  const ok = { kind: 'ok', status: status('d', 'none'), at: 1 } as WatchEntry
  it('is the first host in the host order that answered, else any that did, else none', () => {
    expect(startTarget({ b: ok, a: ok }, ['a', 'b'])).toBe('a')
    expect(startTarget({ a: { kind: 'error' }, b: ok }, ['a', 'b'])).toBe('b')
    expect(startTarget({ x: ok }, ['a'])).toBe('x')
    expect(startTarget({ a: { kind: 'unsupported' } }, ['a'])).toBeUndefined()
  })
})

describe('W5e-3: starting and cooldown_until end the busy state', () => {
  const T0 = Date.parse('2026-10-17T10:00:00Z')
  beforeEach(() => { vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] }); vi.setSystemTime(T0) })
  afterEach(() => { vi.useRealTimers() })
  const withFields = (extra: Partial<WatchStatus>): WatchStatus => ({ ...status('d1', 'none'), ...extra })
  const goBusy = async (kind: 'start' | 'wake' = 'start') => {
    stops.push(startWatchLaunch())
    reply(202, { host: 'mlab', host_id: 'd1' })
    await requestLaunch(kind, 'h1')
  }
  const until = (ms: number) => new Date(T0 + ms).toISOString()

  it('a cooldown_until in the future releases busy at once, with the time; the button stays locked', async () => {
    await goBusy()
    expect(slot()?.phase).toBe('busy')
    answer('h1', withFields({ cooldown_until: until(600_000) }))
    expect(slot()).toMatchObject({ phase: 'cooldown', kind: 'start', until: T0 + 600_000, timed: true })
    expect(isLaunchBusy(slot())).toBe(false)
    expect(isLaunchLocked(slot())).toBe(true)
  })

  it('a cooldown already in the status when the 202 comes releases busy too', async () => {
    answer('h1', withFields({ cooldown_until: until(60_000) }))
    await goBusy()
    expect(slot()).toMatchObject({ phase: 'cooldown', timed: true })
  })

  it('a cooldown_until in the past changes nothing', async () => {
    await goBusy()
    answer('h1', withFields({ cooldown_until: until(-1000) }))
    expect(slot()?.phase).toBe('busy')
  })

  it('the cooldown ends by itself at its time: the slot clears and the button is free', async () => {
    await goBusy()
    answer('h1', withFields({ cooldown_until: until(600_000) }))
    vi.advanceTimersByTime(599_999)
    expect(isLaunchLocked(slot())).toBe(true)
    vi.advanceTimersByTime(1)
    expect(slot()).toBeNull()
  })

  it('no cooldown timer is hung without a cooldown, and a 409 cooldown hangs none', async () => {
    await goBusy()
    expect(vi.getTimerCount()).toBe(1) // the 120 s timeout only
    useWatchLaunchStore.setState({ slot: null })
    reply(409, { error: 'cooldown', detail: until(600_000) })
    await requestLaunch('start', 'h1')
    expect(slot()).toMatchObject({ phase: 'cooldown' })
    expect(isLaunchLocked(slot())).toBe(false)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('starting true keeps it busy', async () => {
    await goBusy()
    answer('h1', withFields({ starting: true }))
    expect(slot()).toMatchObject({ phase: 'busy', sawStarting: true })
  })

  it('starting true then false with no watcher and no cooldown releases with the "did not report" line', async () => {
    await goBusy()
    answer('h1', withFields({ starting: true }))
    answer('h1', withFields({ starting: false }))
    expect(slot()).toMatchObject({ phase: 'timeout', kind: 'start' })
    expect(vi.getTimerCount()).toBe(0)
  })

  it('starting true then a registration just ends busy (no timeout line)', async () => {
    await goBusy()
    answer('h1', withFields({ starting: true }))
    answer('h1', status('d1', 'awake'))
    expect(slot()).toBeNull()
  })

  it('a daemon without the two fields behaves as before: busy until registered or the 120 s', async () => {
    await goBusy()
    answer('h1', status('d1', 'none'))
    answer('h1', status('d1', 'none'))
    expect(slot()?.phase).toBe('busy')
    vi.advanceTimersByTime(WATCH_LAUNCH_TIMEOUT_MS)
    expect(slot()?.phase).toBe('timeout')
  })

  it('a re-point drops the cooldown and its timer: the old cooldown does not reach the new host', async () => {
    stops.push(startWatchStatus())
    fetchWatchStatus.mockResolvedValue({ kind: 'ok', status: status('d1', 'none') })
    await goBusy()
    answer('h1', withFields({ cooldown_until: until(600_000) }))
    expect(slot()?.phase).toBe('cooldown')
    at('2.2.2.2')
    expect(slot()).toBeNull()
    vi.advanceTimersByTime(600_000) // the old cooldown's deadline passes: nothing of it is left to act
    expect(slot()).toBeNull()
    const e =useWatchStatusStore.getState().byHost.h1
    expect(e === undefined || (e.kind === 'ok' && e.status.cooldown_until === undefined)).toBe(true)
    reply(202, { host: 'mlab', host_id: 'd1' })
    await requestLaunch('start', 'h1')
    expect(slot()?.phase).toBe('busy')
  })
})
