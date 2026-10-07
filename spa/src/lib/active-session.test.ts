import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from '../stores/useTabStore'
import { createTab } from '../types/tab'
import { getActiveSessionCode, getActiveSessionInfo, isAgentVisibleInActiveTab } from './active-session'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'
import { compositeKey } from './composite-key'
import type { PaneLayout, Tab } from '../types/tab'

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

// #1840: the notification dispatcher's "the user is looking at it" check — any pane of the active tab counts.
describe('isAgentVisibleInActiveTab (#1840)', () => {
  const tmux = (id: string, hostId: string, sessionCode: string): PaneLayout =>
    ({ type: 'leaf', pane: { id, content: { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: '', tmuxInstance: '' } } })
  const exec = (id: string, executionId: string, host?: string): PaneLayout =>
    ({ type: 'leaf', pane: { id, content: { kind: 'execution', executionId, ...(host !== undefined ? { host } : {}) } } })
  const blank = (id: string): PaneLayout => ({ type: 'leaf', pane: { id, content: { kind: 'new-tab' } } })
  const split = (id: string, children: PaneLayout[]): PaneLayout =>
    ({ type: 'split', id, direction: 'h', children, sizes: children.map(() => 100 / children.length) })
  const tab = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 0, layout })

  beforeEach(() => {
    useHostStore.setState({ hostOrder: ['h1'] })
  })

  it('false with no active tab, or an active id that names no tab', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', tmux('p1', 'h', 'abc123')) }, activeTabId: null })
    expect(isAgentVisibleInActiveTab('h', 'abc123')).toBe(false)
    useTabStore.setState({ activeTabId: 'gone' })
    expect(isAgentVisibleInActiveTab('h', 'abc123')).toBe(false)
  })

  it('true for the primary pane of the active tab (unchanged case)', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', tmux('p1', 'h', 'abc123')) }, activeTabId: 't1' })
    expect(isAgentVisibleInActiveTab('h', 'abc123')).toBe(true)
  })

  it('true for a SECONDARY pane of the active split tab, at any depth', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', split('s1', [blank('p1'), tmux('p2', 'h', 'abc123')])) }, activeTabId: 't1' })
    expect(isAgentVisibleInActiveTab('h', 'abc123')).toBe(true)
    useTabStore.setState({
      tabs: { t1: tab('t1', split('s1', [blank('p1'), split('s2', [blank('p2'), split('s3', [blank('p3'), tmux('p4', 'h', 'abc123')])])])) },
      activeTabId: 't1',
    })
    expect(isAgentVisibleInActiveTab('h', 'abc123')).toBe(true)
  })

  it('false when only ANOTHER tab shows the agent', () => {
    useTabStore.setState({
      tabs: { t1: tab('t1', split('s1', [blank('p1'), blank('p2')])), t2: tab('t2', split('s2', [blank('p3'), tmux('p4', 'h', 'abc123')])) },
      activeTabId: 't1',
    })
    expect(isAgentVisibleInActiveTab('h', 'abc123')).toBe(false)
  })

  it('the same code on another host is not this agent', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', split('s1', [blank('p1'), tmux('p2', 'host-b', 'zk16vd')])) }, activeTabId: 't1' })
    expect(isAgentVisibleInActiveTab('host-a', 'zk16vd')).toBe(false)
    expect(isAgentVisibleInActiveTab('host-b', 'zk16vd')).toBe(true)
  })

  it('a worker in a secondary pane matches exec-<id> on its host hint, a host-less one on the first host', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', split('s1', [blank('p1'), exec('p2', 'e1', 'h2')])) }, activeTabId: 't1' })
    expect(isAgentVisibleInActiveTab('h2', 'exec-e1')).toBe(true)
    expect(isAgentVisibleInActiveTab('h1', 'exec-e1')).toBe(false)
    expect(isAgentVisibleInActiveTab('h2', 'e1')).toBe(false)
    useTabStore.setState({ tabs: { t1: tab('t1', split('s1', [blank('p1'), exec('p2', 'e1')])) }, activeTabId: 't1' })
    expect(isAgentVisibleInActiveTab('h1', 'exec-e1')).toBe(true)
    expect(isAgentVisibleInActiveTab('h2', 'exec-e1')).toBe(false)
  })

  it('leaves getActiveSessionInfo primary-only (its other callers are out of scope)', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', split('s1', [blank('p1'), tmux('p2', 'h', 'abc123')])) }, activeTabId: 't1' })
    expect(getActiveSessionInfo()).toBeNull()
  })
})
