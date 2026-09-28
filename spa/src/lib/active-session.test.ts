import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from '../stores/useTabStore'
import { createTab } from '../types/tab'
import { getActiveSessionCode, getActiveSessionInfo } from './active-session'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'
import { compositeKey } from './composite-key'
import type { Tab } from '../types/tab'

beforeEach(() => {
  useTabStore.setState({ tabs: {}, activeTabId: null, tabOrder: [] })
})

describe('getActiveSessionCode', () => {
  it('returns sessionCode when active tab is a session', () => {
    const tab = { ...createTab({ kind: 'tmux-session', hostId: 'test-host', sessionCode: 'dev', mode: 'terminal', cachedName: '', tmuxInstance: '' }), id: 't1' }
    useTabStore.setState({ tabs: { t1: tab }, activeTabId: 't1' })
    expect(getActiveSessionCode()).toBe('dev')
  })

  it('returns null when active tab is not a session', () => {
    const tab = { ...createTab({ kind: 'settings', scope: 'global' }), id: 't3' }
    useTabStore.setState({ tabs: { t3: tab }, activeTabId: 't3' })
    expect(getActiveSessionCode()).toBeNull()
  })

  it('returns null when no active tab', () => {
    expect(getActiveSessionCode()).toBeNull()
  })

  it('returns null when activeTabId points to missing tab', () => {
    useTabStore.setState({ tabs: {}, activeTabId: 'nonexistent' })
    expect(getActiveSessionCode()).toBeNull()
  })
})

describe('getActiveSessionInfo — worker (execution) tab (spec §8.2)', () => {
  const execTab = (host?: string): Tab => ({
    id: 'tx', pinned: false, locked: false, createdAt: 0,
    layout: { type: 'leaf', pane: { id: 'px', content: { kind: 'execution', executionId: 'e1', ...(host ? { host } : {}) } } },
  })

  beforeEach(() => {
    useHostStore.setState({ hostOrder: ['h1'] })
    useAgentStore.setState({ lastEvents: {}, statuses: {}, unread: {}, subagents: {}, models: {}, agentTypes: {} })
  })

  it('returns the exec-<id> key and the pane host', () => {
    useTabStore.setState({ tabs: { tx: execTab('h2') }, activeTabId: 'tx' })
    expect(getActiveSessionInfo()).toEqual({ hostId: 'h2', sessionCode: 'exec-e1' })
  })

  it('a host-less pane resolves to the first host', () => {
    useTabStore.setState({ tabs: { tx: execTab() }, activeTabId: 'tx' })
    expect(getActiveSessionInfo()).toEqual({ hostId: 'h1', sessionCode: 'exec-e1' })
  })

  it('a pane with an empty-string host resolves to the first host, same as no host', () => {
    const tab: Tab = {
      id: 'tx', pinned: false, locked: false, createdAt: 0,
      layout: { type: 'leaf', pane: { id: 'px', content: { kind: 'execution', executionId: 'e1', host: '' } } },
    }
    useTabStore.setState({ tabs: { tx: tab }, activeTabId: 'tx' })
    expect(getActiveSessionInfo()).toEqual({ hostId: 'h1', sessionCode: 'exec-e1' })
  })

  it('active exec tab is not unread', () => {
    useTabStore.setState({ tabs: { tx: execTab('h1') }, activeTabId: 'tx' })
    useAgentStore.getState().handleNormalizedEvent('h1', 'exec-e1', {
      agent_type: 'cc', status: 'idle', raw_event_name: 'Stop', broadcast_ts: 1, detail: {},
    })
    expect(useAgentStore.getState().unread[compositeKey('h1', 'exec-e1')]).toBeUndefined()
  })

  it('a background exec tab does become unread', () => {
    useTabStore.setState({ tabs: {}, activeTabId: null })
    useAgentStore.getState().handleNormalizedEvent('h1', 'exec-e1', {
      agent_type: 'cc', status: 'idle', raw_event_name: 'Stop', broadcast_ts: 1, detail: {},
    })
    expect(useAgentStore.getState().unread[compositeKey('h1', 'exec-e1')]).toBe(true)
  })
})
