import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useNotificationDispatcher } from './useNotificationDispatcher'
import { __resetDebounceStateForTests } from '../lib/notification-gate'
import { STORAGE_KEYS } from '../lib/storage'
import { useAgentStore } from '../stores/useAgentStore'
import type { NormalizedEvent } from '../stores/useAgentStore'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'

// A proxy subagent's Stop / StopFailure (a codex that the pane's main agent runs as a tool, `from_proxy` on the
// frame) ends a tool, not the main agent's turn: no desktop notification and no unread. Waiting stays as is.

const H = 'host-a'
const CODE = 'aigora4'
const CK = `${H}:${CODE}`

function ev(over: Partial<NormalizedEvent>): NormalizedEvent {
  return { agent_type: 'codex', status: 'idle', raw_event_name: 'PdxStop', broadcast_ts: 2, ...over }
}

describe('proxy subagent Stop: no desktop notification, no unread', () => {
  let showNotification: ReturnType<typeof vi.fn>

  beforeEach(() => {
    __resetDebounceStateForTests()
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [CK]: 1 }))
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useAgentStore.setState({ lastEvents: {}, statuses: { [CK]: 'running' }, unread: {}, subagents: {}, models: {}, agentTypes: {} })
    useNotificationSettingsStore.setState({ agents: {} })
    useNotificationSettingsStore.getState().setNotifyWithoutTab('codex', true)
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    showNotification = vi.fn()
    Object.defineProperty(window, 'electronAPI', { value: { showNotification }, writable: true, configurable: true })
  })

  afterEach(() => {
    Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
  })

  function receive(event: NormalizedEvent) {
    const { unmount } = renderHook(() => useNotificationDispatcher())
    useAgentStore.getState().handleNormalizedEvent(H, CODE, event)
    unmount()
  }

  it('a plain Stop notifies and marks unread', () => {
    receive(ev({}))
    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(useAgentStore.getState().unread[CK]).toBe(true)
  })

  it('a from_proxy Stop neither notifies nor marks unread, yet the status still follows', () => {
    receive(ev({ from_proxy: true }))
    expect(showNotification).not.toHaveBeenCalled()
    expect(useAgentStore.getState().unread[CK]).toBeUndefined()
    expect(useAgentStore.getState().statuses[CK]).toBe('idle')
  })

  it('a from_proxy StopFailure neither notifies nor marks unread', () => {
    receive(ev({ status: 'error', raw_event_name: 'PdxStopFailure', from_proxy: true, detail: { error: 'boom' } }))
    expect(showNotification).not.toHaveBeenCalled()
    expect(useAgentStore.getState().unread[CK]).toBeUndefined()
  })

  it('a waiting event still notifies and marks unread, even if flagged', () => {
    receive(ev({ status: 'waiting', raw_event_name: 'PdxPermissionRequest', from_proxy: true, detail: { tool_name: 'Bash' } }))
    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(useAgentStore.getState().unread[CK]).toBe(true)
  })
})
