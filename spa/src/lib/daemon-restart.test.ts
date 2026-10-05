// spa/src/lib/daemon-restart.test.ts — restartDaemon (boot-id completion, one
// 60 s deadline, IPC vs API path), postRestart, readBootId, readShutdownWarnings,
// isManagedLocal and countRunningWorkers (daemon restart spec §3.2, D5–D7, D13).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  restartDaemon, postRestart, readBootId, readShutdownWarnings, isManagedLocal, countRunningWorkers,
  DaemonRestartError, RESTART_TIMEOUT_MS, RESTART_POLL_MS, MAX_WORKER_PAGES, type RestartDeps,
} from './daemon-restart'
import * as hostApi from './host-api'
import * as nexApi from './nex/nex-api'
import { useHostStore } from '../stores/useHostStore'
import { useNexHostStore } from '../stores/useNexHostStore'

vi.mock('./host-api', async (orig) => ({ ...(await orig<typeof import('./host-api')>()), pinnedHostFetch: vi.fn() }))
vi.mock('./nex/nex-api', async (orig) => ({ ...(await orig<typeof import('./nex/nex-api')>()), listExecutions: vi.fn() }))

const deps = (over: Partial<RestartDeps>): Partial<RestartDeps> => ({
  readBootId: async () => null,
  postRestart: async () => 'old',
  isManagedLocal: async () => false,
  localRestart: async () => { throw new Error('unused') },
  readShutdownWarnings: async () => 0,
  ...over,
})

/** Settle-state probe that also attaches a handler at once (no unhandled-rejection noise). */
function track<T>(p: Promise<T>) {
  const s: { done: boolean; value?: T; error?: unknown } = { done: false }
  p.then((v) => { s.done = true; s.value = v }, (e) => { s.done = true; s.error = e })
  return s
}

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })

beforeEach(() => {
  vi.mocked(hostApi.pinnedHostFetch).mockReset()
  vi.mocked(nexApi.listExecutions).mockReset()
  useHostStore.getState().reset()
})

describe('restartDaemon — success needs a NEW boot id', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('resolves once health answers a different boot id', async () => {
    const ids = ['old', 'old', 'new']
    const s = track(restartDaemon('h1', deps({ readBootId: async () => ids.shift() ?? 'new' })))
    await vi.advanceTimersByTimeAsync(3 * RESTART_POLL_MS)
    expect(s).toMatchObject({ done: true, value: { ipc: null, shutdownWarnings: 0 } })
  })

  it("passes the new boot's shutdown warnings through", async () => {
    const ids = ['old', 'new']
    const s = track(restartDaemon('h1', deps({
      readBootId: async () => ids.shift() ?? 'new',
      readShutdownWarnings: async (_h, boot) => (boot === 'new' ? 2 : 0),
    })))
    await vi.advanceTimersByTimeAsync(2 * RESTART_POLL_MS)
    expect(s).toMatchObject({ done: true, value: { ipc: null, shutdownWarnings: 2 } })
  })

  it('times out at 60 s when health keeps answering the old boot id', async () => {
    // The old process answers health through its whole shutdown budget.
    // Dropping the boot-id comparison turns this red (mutation deliverable).
    const s = track(restartDaemon('h1', deps({ readBootId: async () => 'old' })))
    await vi.advanceTimersByTimeAsync(RESTART_TIMEOUT_MS - 1)
    expect(s.done).toBe(false)
    await vi.advanceTimersByTimeAsync(1)
    expect(s.error).toBeInstanceOf(DaemonRestartError)
    expect((s.error as DaemonRestartError).kind).toBe('timeout')
  })

  it('an unreachable host (null) is not success', async () => {
    const s = track(restartDaemon('h1', deps({ readBootId: async () => null })))
    await vi.advanceTimersByTimeAsync(RESTART_TIMEOUT_MS)
    expect((s.error as DaemonRestartError).kind).toBe('timeout')
  })

  it.each<[string, Partial<RestartDeps>]>([
    ['the status IPC', { isManagedLocal: () => new Promise<boolean>(() => {}) }],
    ['the POST', { postRestart: () => new Promise<string>(() => {}) }],
    ['the IPC restart', { isManagedLocal: async () => true, localRestart: () => new Promise<ElectronLocalDaemonResult>(() => {}) }],
  ])('%s hanging still ends in the 60 s timeout', async (_, over) => {
    const s = track(restartDaemon('h1', deps(over)))
    await vi.advanceTimersByTimeAsync(RESTART_TIMEOUT_MS)
    expect((s.error as DaemonRestartError).kind).toBe('timeout')
  })

  it('stops probing health once timed out', async () => {
    const probe = vi.fn(async () => 'old')
    track(restartDaemon('h1', deps({ readBootId: probe })))
    await vi.advanceTimersByTimeAsync(RESTART_TIMEOUT_MS)
    const n = probe.mock.calls.length
    await vi.advanceTimersByTimeAsync(10 * RESTART_POLL_MS)
    expect(probe.mock.calls.length).toBe(n)
  })
})

describe('restartDaemon — path choice', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('managed local daemon → IPC, never the API', async () => {
    const post = vi.fn()
    const result = { url: 'http://127.0.0.1:7860', token: 't', hash: 'h', version: 'v', hostname: 'air' }
    const ids = ['old', 'new']
    const s = track(restartDaemon('h1', deps({
      isManagedLocal: async () => true,
      localRestart: async () => result,
      postRestart: post,
      readBootId: async () => ids.shift() ?? 'new',
    })))
    await vi.advanceTimersByTimeAsync(RESTART_POLL_MS)
    expect(s.value).toEqual({ ipc: result, shutdownWarnings: 0 })
    expect(post).not.toHaveBeenCalled()
  })

  it('any other host → API', async () => {
    const post = vi.fn(async () => 'old')
    const local = vi.fn()
    track(restartDaemon('h1', deps({ postRestart: post, localRestart: local, readBootId: async () => 'new' })))
    await vi.advanceTimersByTimeAsync(RESTART_POLL_MS)
    expect(post).toHaveBeenCalledWith('h1')
    expect(local).not.toHaveBeenCalled()
  })

  it('IPC failure → request error with its message', async () => {
    const s = track(restartDaemon('h1', deps({ isManagedLocal: async () => true, localRestart: async () => { throw new Error('cannot restart: external') } })))
    await vi.advanceTimersByTimeAsync(0)
    expect(s.error).toMatchObject({ kind: 'request', message: expect.stringContaining('cannot restart: external') })
  })
})

describe('postRestart', () => {
  it('202 → the pre-restart boot id', async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockResolvedValueOnce(json(202, { boot_id: 'b1' }))
    await expect(postRestart('h1')).resolves.toBe('b1')
    expect(vi.mocked(hostApi.pinnedHostFetch).mock.calls[0].slice(0, 2)).toEqual(['h1', '/api/daemon/restart'])
    expect(vi.mocked(hostApi.pinnedHostFetch).mock.calls[0][2]).toMatchObject({ method: 'POST' })
  })
  it('409 with boot id → follows the restart already under way', async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockResolvedValueOnce(json(409, { error: 'restart_in_progress', boot_id: 'b1' }))
    await expect(postRestart('h1')).resolves.toBe('b1')
  })
  it('404 → unsupported (daemon predates the endpoint)', async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockResolvedValueOnce(new Response('404 page not found', { status: 404 }))
    await expect(postRestart('h1')).rejects.toMatchObject({ kind: 'unsupported' })
  })
  it('503 → request error carrying the daemon error text', async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockResolvedValueOnce(json(503, { error: 'restart_unavailable' }))
    await expect(postRestart('h1')).rejects.toMatchObject({ kind: 'request', message: 'restart_unavailable' })
  })
  it('a host no longer configured (pinned fetch rejects) → request error, nothing sent elsewhere', async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockRejectedValueOnce(new Error('host h1 is not configured'))
    await expect(postRestart('h1')).rejects.toMatchObject({ kind: 'request', message: 'host h1 is not configured' })
  })
})

describe('readBootId', () => {
  it('reads boot_id from /api/health through the pinned fetch', async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockResolvedValueOnce(json(200, { ok: true, boot_id: 'b9' }))
    await expect(readBootId('h1')).resolves.toBe('b9')
    expect(vi.mocked(hostApi.pinnedHostFetch).mock.calls[0].slice(0, 2)).toEqual(['h1', '/api/health'])
  })
  it('no boot_id / non-200 / throw → null', async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockResolvedValueOnce(json(200, { ok: true }))
    await expect(readBootId('h1')).resolves.toBeNull()
    vi.mocked(hostApi.pinnedHostFetch).mockResolvedValueOnce(json(500, {}))
    await expect(readBootId('h1')).resolves.toBeNull()
    vi.mocked(hostApi.pinnedHostFetch).mockRejectedValueOnce(new Error('down'))
    await expect(readBootId('h1')).resolves.toBeNull()
  })
})

describe('readShutdownWarnings', () => {
  const info = (last_shutdown: unknown) => json(200, { last_shutdown })
  it('a record for this boot → its error count', async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockResolvedValueOnce(info({ at: 1, errors: ['a', 'b'], boot_id: 'new' }))
    await expect(readShutdownWarnings('h1', 'new')).resolves.toBe(2)
    expect(vi.mocked(hostApi.pinnedHostFetch).mock.calls[0].slice(0, 2)).toEqual(['h1', '/api/info'])
  })
  it("another boot's record → 0", async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockResolvedValueOnce(info({ at: 1, errors: ['a', 'b'], boot_id: 'older' }))
    await expect(readShutdownWarnings('h1', 'new')).resolves.toBe(0)
  })
  it('last_shutdown null → 0', async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockResolvedValueOnce(info(null))
    await expect(readShutdownWarnings('h1', 'new')).resolves.toBe(0)
  })
  it('a fetch rejection → 0', async () => {
    vi.mocked(hostApi.pinnedHostFetch).mockRejectedValueOnce(new Error('down'))
    await expect(readShutdownWarnings('h1', 'new')).resolves.toBe(0)
  })
})

describe('isManagedLocal', () => {
  const st = (o: Partial<ElectronLocalDaemonStatus>) => ({ managed: 'managed', config: { bind: '100.64.0.4', port: 7860, token: 't' }, ...o }) as ElectronLocalDaemonStatus
  afterEach(() => { window.electronAPI = undefined })
  it('true only when managed and the host is at the local daemon endpoint', async () => {
    const id = useHostStore.getState().registerLocalHost({ url: 'http://100.64.0.4:7860', token: 't', hostname: 'air' })
    window.electronAPI = { ...window.electronAPI!, localDaemonStatus: async () => st({}), localDaemonRestart: vi.fn() } as typeof window.electronAPI
    await expect(isManagedLocal(id)).resolves.toBe(true)
    window.electronAPI = { ...window.electronAPI!, localDaemonStatus: async () => st({ managed: 'external' }) } as typeof window.electronAPI
    await expect(isManagedLocal(id)).resolves.toBe(false)
  })
  it('false without Electron', async () => {
    window.electronAPI = undefined
    await expect(isManagedLocal('h1')).resolves.toBe(false)
  })
})

describe('countRunningWorkers', () => {
  const setNex = (hostId: string, info: unknown) =>
    useNexHostStore.setState({ byHost: { [hostId]: { info } } } as never)
  beforeEach(() => {
    vi.spyOn(useNexHostStore.getState(), 'ensure').mockResolvedValue()
  })
  it('counts running executions across pages', async () => {
    setNex('h1', { ready: true, mounted: true, configured: true })
    vi.mocked(nexApi.listExecutions)
      .mockResolvedValueOnce({ items: [{ state: 'running' }, { state: 'running' }] as never, next_cursor: 'c2' })
      .mockResolvedValueOnce({ items: [{ state: 'running' }] as never, next_cursor: '' })
    await expect(countRunningWorkers('h1')).resolves.toBe(3)
    expect(vi.mocked(nexApi.listExecutions).mock.calls[0][1]).toMatchObject({ state: 'running' })
    expect(vi.mocked(nexApi.listExecutions).mock.calls[1][1]).toMatchObject({ state: 'running', cursor: 'c2' })
  })
  it('more pages than MAX_WORKER_PAGES → null (a truncated count would under-report)', async () => {
    setNex('h1', { ready: true, mounted: true, configured: true })
    vi.mocked(nexApi.listExecutions).mockResolvedValue({ items: [{ state: 'running' }] as never, next_cursor: 'more' })
    await expect(countRunningWorkers('h1')).resolves.toBeNull()
    expect(nexApi.listExecutions).toHaveBeenCalledTimes(MAX_WORKER_PAGES)
  })
  it('nex not ready → 0 (no workers can run)', async () => {
    setNex('h1', { ready: false, mounted: false, configured: false })
    await expect(countRunningWorkers('h1')).resolves.toBe(0)
    expect(nexApi.listExecutions).not.toHaveBeenCalled()
  })
  it('no nex info, or the list fails → null (unknown)', async () => {
    setNex('h1', null)
    await expect(countRunningWorkers('h1')).resolves.toBeNull()
    setNex('h1', { ready: true, mounted: true, configured: true })
    vi.mocked(nexApi.listExecutions).mockRejectedValueOnce(new Error('503'))
    await expect(countRunningWorkers('h1')).resolves.toBeNull()
  })
})
