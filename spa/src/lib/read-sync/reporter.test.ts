// spa/src/lib/read-sync/reporter.test.ts — telling the host daemon "X read through T" (app-badge spec §7.1–§7.3): one
// POST per session per 2 s window, carrying the largest cursor captured in it; only for a host session on a host that
// declares `session.read_sync.v1`; best effort (a 404 or a network error is ignored).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useNexHostStore } from '../../stores/useNexHostStore'

vi.mock('../host-api', () => ({ pinnedHostFetch: vi.fn() }))
import { pinnedHostFetch } from '../host-api'
import { READ_DEBOUNCE_MS, reportRead, stopReadSync } from './reporter'

const post = vi.mocked(pinnedHostFetch)
const CAP = 'session.read_sync.v1'

function declare(hostId: string, caps: string[] = [CAP]) {
  useNexHostStore.setState((s) => ({ byHost: { ...s.byHost, [hostId]: { daemonCapabilities: caps } as never } }))
}
const calls = () => post.mock.calls.map(([host, path, init]) => ({ host, path, init: init as RequestInit }))

beforeEach(() => {
  vi.useFakeTimers()
  post.mockReset()
  post.mockResolvedValue(new Response(null, { status: 204 }))
  useNexHostStore.setState({ byHost: {} })
  declare('h1')
})
afterEach(() => {
  stopReadSync()
  vi.useRealTimers()
})

describe('reportRead — the POST', () => {
  it('sends once, 2 s after the first report, to the session\'s read route, with the cursor as `through`', () => {
    reportRead('h1', 'aaaaaa', { epoch: 'E1', seq: 7 })
    vi.advanceTimersByTime(READ_DEBOUNCE_MS - 1)
    expect(post).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(calls()).toHaveLength(1)
    const [c] = calls()
    expect(c.host).toBe('h1')
    expect(c.path).toBe('/api/sessions/aaaaaa/read')
    expect(c.init.method).toBe('POST')
    expect(new Headers(c.init.headers).get('Content-Type')).toBe('application/json')
    expect(JSON.parse(c.init.body as string)).toEqual({ through: { epoch: 'E1', seq: 7 } })
  })

  it('the debounce is 2 s', () => {
    expect(READ_DEBOUNCE_MS).toBe(2000)
  })

  it('no cursor: the body omits `through` (the daemon then uses the newest it has emitted)', () => {
    reportRead('h1', 'aaaaaa')
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(JSON.parse(calls()[0].init.body as string)).toEqual({})
  })

  it('reports of one session inside the window make one POST carrying the largest cursor, whatever the order', () => {
    reportRead('h1', 'aaaaaa', { epoch: 'E1', seq: 5 })
    vi.advanceTimersByTime(500)
    reportRead('h1', 'aaaaaa', { epoch: 'E1', seq: 9 })
    reportRead('h1', 'aaaaaa', { epoch: 'E1', seq: 3 })
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(calls()).toHaveLength(1)
    expect(JSON.parse(calls()[0].init.body as string)).toEqual({ through: { epoch: 'E1', seq: 9 } })
  })

  it('a captured cursor is not lowered to "none" by a later report with none', () => {
    reportRead('h1', 'aaaaaa', { epoch: 'E1', seq: 5 })
    reportRead('h1', 'aaaaaa')
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(JSON.parse(calls()[0].init.body as string)).toEqual({ through: { epoch: 'E1', seq: 5 } })
  })

  it('the window is fixed from the first report (a busy agent cannot starve the POST)', () => {
    reportRead('h1', 'aaaaaa', { epoch: 'E1', seq: 1 })
    for (let i = 1; i <= 4; i++) {
      vi.advanceTimersByTime(400) // 400, 800, 1200, 1600 ms: every one a report inside the window
      reportRead('h1', 'aaaaaa', { epoch: 'E1', seq: 1 + i })
    }
    expect(post).not.toHaveBeenCalled()
    vi.advanceTimersByTime(READ_DEBOUNCE_MS - 1600) // the window ends 2 s after the FIRST report, not the last
    expect(calls()).toHaveLength(1)
    expect(JSON.parse(calls()[0].init.body as string)).toEqual({ through: { epoch: 'E1', seq: 5 } })
  })

  it('after the POST a new report starts a new window', () => {
    reportRead('h1', 'aaaaaa', { epoch: 'E1', seq: 1 })
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    reportRead('h1', 'aaaaaa', { epoch: 'E1', seq: 4 })
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(calls().map((c) => JSON.parse(c.init.body as string).through.seq)).toEqual([1, 4])
  })

  it('sessions and hosts have their own windows', () => {
    declare('h2')
    reportRead('h1', 'aaaaaa', { epoch: 'E1', seq: 1 })
    reportRead('h1', 'bbbbbb', { epoch: 'E1', seq: 2 })
    reportRead('h2', 'aaaaaa', { epoch: 'E9', seq: 3 })
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(calls().map((c) => `${c.host}${c.path}`).sort()).toEqual([
      'h1/api/sessions/aaaaaa/read', 'h1/api/sessions/bbbbbb/read', 'h2/api/sessions/aaaaaa/read',
    ])
  })

  it('a code is path-encoded', () => {
    reportRead('h1', 'a/b c', { epoch: 'E1', seq: 1 })
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(calls()[0].path).toBe('/api/sessions/a%2Fb%20c/read')
  })
})

describe('reportRead — who may send', () => {
  const none = () => {
    vi.advanceTimersByTime(READ_DEBOUNCE_MS * 2)
    expect(post).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
  }

  it('a worker (exec-*) key never sends', () => {
    reportRead('h1', 'exec-abc123', { epoch: 'E1', seq: 1 })
    none()
  })

  it('a session outside tmux (cc-*) never sends', () => {
    reportRead('h1', 'cc-abc123', { epoch: 'E1', seq: 1 })
    none()
  })

  it('a host that does not declare session.read_sync.v1 gets nothing (an older daemon)', () => {
    declare('h3', ['team.ask_chat.v1'])
    reportRead('h3', 'aaaaaa', { epoch: 'E1', seq: 1 })
    none()
  })

  it('a host whose capabilities are not known yet (or that has no entry) gets nothing', () => {
    useNexHostStore.setState({ byHost: { h4: {} as never } })
    reportRead('h4', 'aaaaaa', { epoch: 'E1', seq: 1 })
    reportRead('hX', 'aaaaaa', { epoch: 'E1', seq: 1 })
    none()
  })

  it('a malformed code (empty) never sends', () => {
    reportRead('h1', '', { epoch: 'E1', seq: 1 })
    none()
  })
})

describe('reportRead — best effort', () => {
  it('a 404 is ignored (a session the daemon does not have)', async () => {
    post.mockResolvedValue(new Response('{"error":"no_such_session"}', { status: 404 }))
    reportRead('h1', 'aaaaaa', { epoch: 'E1', seq: 1 })
    await vi.advanceTimersByTimeAsync(READ_DEBOUNCE_MS)
    expect(post).toHaveBeenCalledTimes(1)
  })

  it('a network failure or a throw is ignored, nothing is retried', async () => {
    post.mockRejectedValue(new Error('network down'))
    reportRead('h1', 'aaaaaa', { epoch: 'E1', seq: 1 })
    await vi.advanceTimersByTimeAsync(READ_DEBOUNCE_MS * 3)
    expect(post).toHaveBeenCalledTimes(1)
    post.mockImplementation(() => { throw new Error('sync throw') })
    reportRead('h1', 'bbbbbb', { epoch: 'E1', seq: 1 })
    await expect(vi.advanceTimersByTimeAsync(READ_DEBOUNCE_MS)).resolves.not.toThrow()
  })

  it('stopReadSync cancels every pending window', () => {
    reportRead('h1', 'aaaaaa', { epoch: 'E1', seq: 1 })
    reportRead('h1', 'bbbbbb', { epoch: 'E1', seq: 1 })
    stopReadSync()
    vi.advanceTimersByTime(READ_DEBOUNCE_MS * 2)
    expect(post).not.toHaveBeenCalled()
  })
})
