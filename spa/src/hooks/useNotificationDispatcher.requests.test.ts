// Permission channel PC2 as amended 2026-10-07 (user): a worker waiting for approval notifies like an agent ask,
// once per request. The dispatcher's persistent dedupe for such an event (`detail.request_id`) is by request id, not by
// stamp: Nexen serializes a worker's requests but does not give them distinct timestamps, so two requests can share
// the millisecond their `since` is stamped in.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { clearSeenTs, __resetDebounceStateForTests, useNotificationDispatcher } from './useNotificationDispatcher'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'
import { STORAGE_KEYS } from '../lib/storage'
import { useTabStore } from '../stores/useTabStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { useExecutionStore } from '../stores/useExecutionStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import type { Tab } from '../types/tab'

const HOST = 'h1'
const CODE = 'exec-e1'
const CK = `${HOST}:${CODE}`
const TMUX = 'ses001'
const TMUX_CK = `${HOST}:${TMUX}`

const tabs: Record<string, Tab> = {
  tx: { id: 'tx', pinned: false, locked: false, createdAt: 0,
    layout: { type: 'leaf', pane: { id: 'px', content: { kind: 'execution', executionId: 'e1', host: HOST } } } },
  tt: { id: 'tt', pinned: false, locked: false, createdAt: 0,
    layout: { type: 'leaf', pane: { id: 'pt', content: { kind: 'tmux-session', hostId: HOST, sessionCode: TMUX, mode: 'terminal', cachedName: '', tmuxInstance: '' } } } },
}

/** What useWorkerAgentProjection dispatches for a worker awaiting approval: stamped with the request's `since`. */
const ask = (requestId: string, since: number, tool = 'Bash') =>
  useAgentStore.getState().handleNormalizedEvent(HOST, CODE, {
    agent_type: 'cc', status: 'waiting', raw_event_name: 'PermissionRequest', broadcast_ts: since, detail: { tool_name: tool, request_id: requestId },
  })
const workerEvent = (status: 'running' | 'idle', raw_event_name: string, broadcast_ts: number) =>
  useAgentStore.getState().handleNormalizedEvent(HOST, CODE, { agent_type: 'cc', status, raw_event_name, broadcast_ts, detail: {} })
/** A terminal Claude Code agent's ask, as the host event stream delivers it — no request id. */
const terminalAsk = (broadcast_ts: number) =>
  useAgentStore.getState().handleNormalizedEvent(HOST, TMUX, { agent_type: 'cc', status: 'waiting', raw_event_name: 'PdxPermissionRequest', broadcast_ts, detail: { tool_name: 'Bash' } })

const seenTs = (): Record<string, number> => JSON.parse(localStorage.getItem(STORAGE_KEYS.NOTIFICATION_SEEN) || '{}')
const seenRequests = (): Record<string, string[]> => JSON.parse(localStorage.getItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS) || '{}')
const resetAgentStore = () => useAgentStore.setState({ lastEvents: {}, statuses: {}, unread: {}, subagents: {}, models: {}, agentTypes: {} })

describe('approval requests (a worker waiting) dedupe by request id', () => {
  let showNotification: ReturnType<typeof vi.fn>
  let dispatcher: { unmount: () => void } | null
  const mount = () => { dispatcher = renderHook(() => useNotificationDispatcher()) }
  /** An App reload: the in-memory stores start fresh, the seen maps (localStorage) survive. */
  const reload = () => {
    dispatcher?.unmount()
    resetAgentStore()
    mount()
  }
  const stamps = () => showNotification.mock.calls.map((c) => (c[0] as { broadcastTs: number }).broadcastTs)
  const workerNotices = () => showNotification.mock.calls.filter((c) => (c[0] as { sessionCode: string }).sessionCode === CODE).length

  beforeEach(() => {
    __resetDebounceStateForTests()
    localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN)
    localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS)
    useTabStore.setState({ tabs, tabOrder: ['tx', 'tt'], activeTabId: null, visitHistory: [] })
    resetAgentStore()
    useNotificationSettingsStore.setState({ agents: {} })
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    useHostStore.setState({ hostOrder: [HOST] })
    useShownHostsStore.setState({ ids: [HOST] })
    useExecutionStore.setState({ executions: {} })
    useExecutionListStore.setState({ byHost: {} })
    showNotification = vi.fn()
    Object.defineProperty(window, 'electronAPI', { value: { showNotification }, writable: true, configurable: true })
    vi.spyOn(document, 'hasFocus').mockReturnValue(false)
    dispatcher = null
  })
  afterEach(() => {
    dispatcher?.unmount()
    vi.restoreAllMocks()
    Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
    localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN)
    localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS)
  })

  it('two requests in the same millisecond on a known worker both notify, with distinct Electron stamps', () => {
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [CK]: 1 }))
    mount()
    ask('r1', 50)
    ask('r2', 50, 'Write')
    expect(showNotification).toHaveBeenCalledTimes(2)
    expect(showNotification.mock.calls.map((c) => (c[0] as { body: string }).body)).toEqual(['Permission required: Bash', 'Permission required: Write'])
    // Electron main dedupes by the numeric stamp for 5 s across every window: equal numbers would drop the second.
    const [a, b] = stamps()
    expect(a).not.toBe(b)
    for (const s of [a, b]) {
      expect(Number.isFinite(s)).toBe(true)
      // Inside the request's millisecond: never an integer another event (or a neighbouring millisecond) could carry.
      expect(s).toBeGreaterThan(50)
      expect(s).toBeLessThan(51)
    }
  })

  it('the Electron stamp is the same for the same request (a second window still dedupes against the first)', () => {
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [CK]: 1 }))
    mount()
    ask('r1', 1_760_000_000_000)
    // Another window: its own agent store, but the same request — seen fresh because it raced this one.
    dispatcher?.unmount()
    resetAgentStore()
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [CK]: 1 }))
    localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS)
    mount()
    ask('r1', 1_760_000_000_000)
    expect(showNotification).toHaveBeenCalledTimes(2)
    const [a, b] = stamps()
    expect(a).toBe(b)
    expect(a).toBeGreaterThan(1_760_000_000_000)
    expect(a).toBeLessThan(1_760_000_000_001)
  })

  it('a request already seen never notifies again: a refetch, another event in between, a reload', () => {
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [CK]: 1 }))
    mount()
    ask('r1', 50)
    expect(showNotification).toHaveBeenCalledTimes(1)
    // A refetch / reconnect hands the projection the same request again.
    ask('r1', 50)
    // A list-row <-> live switch: another event of the key in between, then the same request.
    workerEvent('running', 'UserPromptSubmit', 45)
    ask('r1', 50)
    expect(showNotification).toHaveBeenCalledTimes(1)
    // An App reload: the projection's first snapshot carries the same request.
    reload()
    ask('r1', 50)
    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(seenRequests()[CK]).toEqual(['r1'])
  })

  it('a worker never seen on this client: its first request is a baseline, not a notification (like an agent ask\'s first event)', () => {
    mount()
    ask('r1', 50)
    expect(showNotification).not.toHaveBeenCalled()
    expect(seenRequests()[CK]).toEqual(['r1'])
    expect(seenTs()[CK]).toBe(50)
    // The next request is news — even in the same millisecond.
    ask('r2', 50)
    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(seenRequests()[CK]).toEqual(['r1', 'r2'])
  })

  it('after a reload, a request this client has not seen on a known worker notifies once — whatever its since', () => {
    // Before the reload this client saw r1 (since 50) and recorded its stamp.
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [CK]: 50 }))
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS, JSON.stringify({ [CK]: ['r1'] }))
    mount()
    ask('r1', 50)
    expect(showNotification).not.toHaveBeenCalled()
    // r1 was answered and r2 asked in the same millisecond while the App was closed.
    ask('r2', 50)
    expect(workerNotices()).toBe(1)
    reload()
    ask('r2', 50)
    expect(workerNotices()).toBe(1)
  })

  it('the stamp map keeps the newest stamp, so the key\'s other events keep their dedupe', () => {
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [CK]: 1 }))
    mount()
    ask('r1', 50)
    expect(seenTs()[CK]).toBe(50)
    // A Stop older than the request is not news; a newer one is.
    workerEvent('idle', 'Stop', 40)
    expect(showNotification).toHaveBeenCalledTimes(1)
    workerEvent('idle', 'Stop', 80)
    expect(showNotification).toHaveBeenCalledTimes(2)
    // A request stamped before the stored stamp still notifies (its id is new) and leaves the stored stamp alone.
    ask('r3', 30)
    expect(showNotification).toHaveBeenCalledTimes(3)
    expect(seenTs()[CK]).toBe(80)
  })

  it('a terminal agent\'s ask keeps the stamp dedupe it always had', () => {
    mount()
    terminalAsk(2) // never seen: a baseline
    expect(showNotification).not.toHaveBeenCalled()
    terminalAsk(3)
    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(stamps()).toEqual([3])
    // The same ask replayed by a reconnect snapshot: not again.
    reload()
    terminalAsk(3)
    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(seenTs()[TMUX_CK]).toBe(3)
    expect(seenRequests()[TMUX_CK]).toBeUndefined()
  })

  it('keeps the last 20 request ids per worker', () => {
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [CK]: 1 }))
    mount()
    for (let i = 0; i < 25; i++) ask(`r${i}`, 100 + i)
    expect(showNotification).toHaveBeenCalledTimes(25)
    expect(seenRequests()[CK]).toEqual(Array.from({ length: 20 }, (_, i) => `r${i + 5}`))
    reload()
    ask('r24', 124)
    expect(showNotification).toHaveBeenCalledTimes(25)
  })

  it('a corrupt request map is read as empty, not thrown on', () => {
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [CK]: 1 }))
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS, '{not json')
    mount()
    ask('r1', 50)
    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(seenRequests()[CK]).toEqual(['r1'])
  })

  it('the key\'s request ids go where its stamp goes: dropped when the key is cleared', () => {
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [CK]: 1, [TMUX_CK]: 1 }))
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS, JSON.stringify({ [TMUX_CK]: ['x'] }))
    mount()
    ask('r1', 50)
    expect(seenRequests()[CK]).toEqual(['r1'])
    // The worker's tab closes: the projection clears the key (SessionEnd).
    useAgentStore.getState().handleNormalizedEvent(HOST, CODE, { agent_type: '', status: 'clear', raw_event_name: 'SessionEnd', broadcast_ts: 60, detail: {} })
    expect(seenTs()[CK]).toBeUndefined()
    expect(seenRequests()[CK]).toBeUndefined()
    // Another key's entry is untouched.
    expect(seenRequests()[TMUX_CK]).toEqual(['x'])
    // Reopened while still waiting on r1: a key this client no longer knows — a baseline, as for an agent ask.
    ask('r1', 50)
    expect(showNotification).toHaveBeenCalledTimes(1)
  })

  it('clearSeenTs drops the key from both maps', () => {
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [CK]: 5, [TMUX_CK]: 6 }))
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS, JSON.stringify({ [CK]: ['r1'], [TMUX_CK]: ['x'] }))
    clearSeenTs(CK)
    expect(seenTs()).toEqual({ [TMUX_CK]: 6 })
    expect(seenRequests()).toEqual({ [TMUX_CK]: ['x'] })
  })
})
