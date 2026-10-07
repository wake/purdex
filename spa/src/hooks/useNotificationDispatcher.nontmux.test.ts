import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { shouldNotify, handleNotificationClick, __resetDebounceStateForTests, useNotificationDispatcher } from './useNotificationDispatcher'
import type { NotificationSettings } from '../stores/useNotificationSettingsStore'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'
import { STORAGE_KEYS } from '../lib/storage'
import { useTabStore } from '../stores/useTabStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'

const defaultSettings: NotificationSettings = {
  enabled: true, events: {}, notifyWithoutTab: false, reopenTabOnClick: false,
}

describe('non-tmux (cc-<session id>) sessions', () => {
  const HOST = 'h1'
  const CODE = 'cc-sid-1'
  const CK = `${HOST}:${CODE}`
  const base = { derived: 'idle', eventName: 'PdxStop', compositeKey: CK, visibleInActiveTab: false, hasTab: false, settings: defaultSettings }

  beforeEach(() => {
    __resetDebounceStateForTests()
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [CK]: 1 }))
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useAgentStore.setState({ lastEvents: {}, statuses: {}, unread: {}, subagents: {}, models: {}, agentTypes: {} })
    useNotificationSettingsStore.setState({ agents: {} })
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    useHostStore.setState({ hostOrder: [HOST] })
    useShownHostsStore.setState({ ids: [HOST] })
  })
  afterEach(() => {
    vi.restoreAllMocks()
    Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
    localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN)
  })

  it('notifies without a tab although notifyWithoutTab is false', () => {
    expect(shouldNotify({ ...base, nonTmux: true })).toBe(true)
  })
  it('a tmux session with no tab stays quiet (behaviour unchanged)', () => {
    expect(shouldNotify({ ...base, nonTmux: false })).toBe(false)
  })
  it('keeps the active-tab + focused-window suppression and the per-event/enabled switches', () => {
    vi.spyOn(document, 'hasFocus').mockReturnValue(true)
    expect(shouldNotify({ ...base, nonTmux: true, visibleInActiveTab: true })).toBe(false)
    expect(shouldNotify({ ...base, nonTmux: true, settings: { ...defaultSettings, enabled: false } })).toBe(false)
    expect(shouldNotify({ ...base, nonTmux: true, settings: { ...defaultSettings, events: { Stop: false } } })).toBe(false)
  })

  it('dispatches a desktop notification for a keyed non-tmux session with no tab', () => {
    const showNotification = vi.fn()
    Object.defineProperty(window, 'electronAPI', { value: { showNotification }, writable: true, configurable: true })
    const { unmount } = renderHook(() => useNotificationDispatcher())
    useAgentStore.getState().handleNormalizedEvent(HOST, CODE, { agent_type: 'cc', status: 'idle', raw_event_name: 'PdxStop', broadcast_ts: 2 } as never)
    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(showNotification.mock.calls[0][0].action).toEqual({ kind: 'open-session', hostId: HOST, sessionCode: CODE })
    unmount()
  })

  it('a tmux session with no tab still raises nothing through the dispatcher', () => {
    const showNotification = vi.fn()
    Object.defineProperty(window, 'electronAPI', { value: { showNotification }, writable: true, configurable: true })
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [`${HOST}:ses001`]: 1 }))
    const { unmount } = renderHook(() => useNotificationDispatcher())
    useAgentStore.getState().handleNormalizedEvent(HOST, 'ses001', { agent_type: 'cc', status: 'idle', raw_event_name: 'PdxStop', broadcast_ts: 2 } as never)
    expect(showNotification).not.toHaveBeenCalled()
    unmount()
  })

  it('click focuses the app, clears unread, and never opens a tmux tab', () => {
    const focusMyWindow = vi.fn()
    Object.defineProperty(window, 'electronAPI', { value: { focusMyWindow }, writable: true, configurable: true })
    useNotificationSettingsStore.setState({ agents: { cc: { ...defaultSettings, reopenTabOnClick: true } } })
    useAgentStore.setState({ unread: { [CK]: true } as never })
    handleNotificationClick({ kind: 'open-session', hostId: HOST, sessionCode: CODE })
    expect(focusMyWindow).toHaveBeenCalledTimes(1)
    expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(0)
    expect(useAgentStore.getState().unread[CK]).toBeUndefined()
  })
})
