// spa/src/lib/read-sync/user-read.test.ts — a USER read is the only read that is told to the daemon (app-badge spec §7.3):
// the unread -> read transition of a host session key, with the cursor captured at that moment. The callers
// (active-tab-mark-read, notification-click) go through `markReadByUser`; a read received from the daemon, `running`,
// `clear` and clearing by host prefix do not.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useAgentStore, type NormalizedEvent } from '../../stores/useAgentStore'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { createTab } from '../../types/tab'
import { handleNotificationClick } from '../notification-click'
import { startActiveTabMarkRead } from '../active-tab-mark-read'

vi.mock('../host-api', async (orig) => ({ ...(await orig<typeof import('../host-api')>()), pinnedHostFetch: vi.fn() }))
import { pinnedHostFetch } from '../host-api'
import { READ_DEBOUNCE_MS, stopReadSync } from './reporter'
import { markReadByUser } from './user-read'

const post = vi.mocked(pinnedHostFetch)
const H = 'h1'
const posted = () => post.mock.calls.map(([host, path, init]) => ({
  host, path, body: JSON.parse((init as RequestInit).body as string) as { through?: { epoch: string; seq: number } },
}))

const ev = (status: string, epoch: string, seq: number): NormalizedEvent =>
  ({ agent_type: 'cc', status, raw_event_name: 'PdxStop', broadcast_ts: 1, epoch, seq })
/** An unread of `code` set by an event with cursor (epoch, seq), no tab showing it. */
function unreadBy(code: string, epoch: string, seq: number) {
  const s = useAgentStore.getState()
  if (s.statuses[`${H}:${code}`] !== 'running') s.applyHookEvent(H, code, ev('running', epoch, seq - 1))
  useAgentStore.getState().applyHookEvent(H, code, ev('idle', epoch, seq))
}
const tmuxTab = (code: string) => ({
  ...createTab({ kind: 'tmux-session', hostId: H, sessionCode: code, mode: 'terminal', cachedName: code, tmuxInstance: 'i' }),
  id: `tab-${code}`,
})

let stop: () => void = () => {}

beforeEach(() => {
  vi.useFakeTimers()
  post.mockReset()
  post.mockResolvedValue(new Response(null, { status: 204 }))
  useHostStore.setState({ hosts: { [H]: { id: H, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } }, hostOrder: [H], activeHostId: H })
  useNexHostStore.setState({ byHost: { [H]: { daemonCapabilities: ['session.read_sync.v1'] } as never } })
  useAgentStore.setState({ statuses: {}, agentTypes: {}, models: {}, subagents: {}, lastEvents: {}, oscTitles: {}, ccStatus: {}, unread: {}, unreadCursor: {} })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
})
afterEach(() => {
  stop()
  stop = () => {}
  stopReadSync()
  vi.useRealTimers()
})

describe('markReadByUser', () => {
  it('an unread host session: marks it read and POSTs the cursor of the event that set it, 2 s later', () => {
    unreadBy('aaaaaa', 'E1', 7)
    markReadByUser(H, 'aaaaaa')
    expect(useAgentStore.getState().unread[`${H}:aaaaaa`]).toBeUndefined()
    expect(post).not.toHaveBeenCalled()
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(posted()).toEqual([{ host: H, path: '/api/sessions/aaaaaa/read', body: { through: { epoch: 'E1', seq: 7 } } }])
  })

  it('the cursor is the one captured at the transition: an event that sets the unread again inside the debounce does not raise T', () => {
    unreadBy('aaaaaa', 'E1', 7)
    markReadByUser(H, 'aaaaaa')
    // a later event, the agent not on screen: marked unread again with a higher cursor — that one was not read by this
    useAgentStore.getState().applyHookEvent(H, 'aaaaaa', ev('running', 'E1', 8))
    useAgentStore.getState().applyHookEvent(H, 'aaaaaa', ev('idle', 'E1', 9))
    expect(useAgentStore.getState().unreadCursor[`${H}:aaaaaa`]).toEqual({ epoch: 'E1', seq: 9 })
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(posted()).toEqual([{ host: H, path: '/api/sessions/aaaaaa/read', body: { through: { epoch: 'E1', seq: 7 } } }])
  })

  it('a key that was already read sends nothing', () => {
    markReadByUser(H, 'aaaaaa')
    unreadBy('bbbbbb', 'E1', 3)
    markReadByUser(H, 'bbbbbb')
    markReadByUser(H, 'bbbbbb') // the second finds it read already
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(posted().map((p) => p.path)).toEqual(['/api/sessions/bbbbbb/read'])
  })

  it('an unread with no cursor posts with none', () => {
    unreadBy('aaaaaa', '', 0)
    markReadByUser(H, 'aaaaaa')
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(posted()).toEqual([{ host: H, path: '/api/sessions/aaaaaa/read', body: {} }])
  })

  it('a worker (exec-*) key and a session outside tmux (cc-*) are marked read and send nothing', () => {
    for (const code of ['exec-e1', 'cc-abc']) {
      useAgentStore.setState({ unread: { [`${H}:${code}`]: true } })
      markReadByUser(H, code)
      expect(useAgentStore.getState().unread[`${H}:${code}`]).toBeUndefined()
    }
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(post).not.toHaveBeenCalled()
  })

  it('a host that does not declare the capability: marked read, nothing sent (an older daemon)', () => {
    useNexHostStore.setState({ byHost: { [H]: { daemonCapabilities: [] } as never } })
    unreadBy('aaaaaa', 'E1', 7)
    markReadByUser(H, 'aaaaaa')
    expect(useAgentStore.getState().unread[`${H}:aaaaaa`]).toBeUndefined()
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(post).not.toHaveBeenCalled()
  })
})

describe('what is NOT a user read', () => {
  it('running clears the unread and sends nothing', () => {
    unreadBy('aaaaaa', 'E1', 7)
    useAgentStore.getState().applyHookEvent(H, 'aaaaaa', ev('running', 'E1', 8))
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(post).not.toHaveBeenCalled()
  })

  it('clear, clearSession, removeHost and a read received from the daemon send nothing', () => {
    unreadBy('aaaaaa', 'E1', 7)
    unreadBy('bbbbbb', 'E1', 8)
    unreadBy('cccccc', 'E1', 9)
    useAgentStore.getState().applyHookEvent(H, 'aaaaaa', ev('clear', 'E1', 10))
    useAgentStore.getState().clearSession(H, 'bbbbbb')
    useAgentStore.getState().applyReads(H, [{ code: 'cccccc', through: { epoch: 'E1', seq: 9 } }])
    useAgentStore.getState().removeHost(H)
    vi.advanceTimersByTime(READ_DEBOUNCE_MS * 2)
    expect(post).not.toHaveBeenCalled()
  })
})

describe('the callers', () => {
  it('a tab that shows the session coming on screen (active-tab-mark-read) is a user read', () => {
    unreadBy('aaaaaa', 'E1', 7)
    stop = startActiveTabMarkRead()
    const tab = tmuxTab('aaaaaa')
    useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id })
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(posted()).toEqual([{ host: H, path: '/api/sessions/aaaaaa/read', body: { through: { epoch: 'E1', seq: 7 } } }])
  })

  it('a notification click that opens the session\'s tab is a user read', () => {
    unreadBy('aaaaaa', 'E1', 7)
    const tab = tmuxTab('aaaaaa')
    useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: null })
    handleNotificationClick({ kind: 'open-session', hostId: H, sessionCode: 'aaaaaa' })
    expect(useAgentStore.getState().unread[`${H}:aaaaaa`]).toBeUndefined()
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(posted()).toEqual([{ host: H, path: '/api/sessions/aaaaaa/read', body: { through: { epoch: 'E1', seq: 7 } } }])
  })

  it('a notification click on a worker with no tab clears the unread and sends nothing', () => {
    useAgentStore.setState({ unread: { [`${H}:exec-e1`]: true } })
    handleNotificationClick({ kind: 'open-session', hostId: H, sessionCode: 'exec-e1' })
    expect(useAgentStore.getState().unread[`${H}:exec-e1`]).toBeUndefined()
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(post).not.toHaveBeenCalled()
  })

  it('a notification click on a session outside tmux clears the unread and sends nothing', () => {
    useAgentStore.setState({ unread: { [`${H}:cc-abc`]: true } })
    handleNotificationClick({ kind: 'open-session', hostId: H, sessionCode: 'cc-abc' })
    expect(useAgentStore.getState().unread[`${H}:cc-abc`]).toBeUndefined()
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(post).not.toHaveBeenCalled()
  })

  it('a click on a session that was already read sends nothing', () => {
    const tab = tmuxTab('aaaaaa')
    useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: null })
    handleNotificationClick({ kind: 'open-session', hostId: H, sessionCode: 'aaaaaa' })
    vi.advanceTimersByTime(READ_DEBOUNCE_MS)
    expect(post).not.toHaveBeenCalled()
  })
})
