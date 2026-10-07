import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from '../stores/useTabStore'
import { createTab } from '../types/tab'
import { getActiveTabAgents, isAgentVisibleInActiveTab } from './active-session'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'
import { compositeKey } from './composite-key'
import type { PaneLayout, Tab } from '../types/tab'

beforeEach(() => {
  useTabStore.setState({ tabs: {}, activeTabId: null, tabOrder: [] })
})

const tmux = (id: string, hostId: string, sessionCode: string): PaneLayout =>
  ({ type: 'leaf', pane: { id, content: { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: '', tmuxInstance: '' } } })
const ended = (id: string, hostId: string, sessionCode: string): PaneLayout =>
  ({ type: 'leaf', pane: { id, content: { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: '', tmuxInstance: '', terminated: 'tmux-restarted' } } })
const exec = (id: string, executionId: string, host?: string): PaneLayout =>
  ({ type: 'leaf', pane: { id, content: { kind: 'execution', executionId, ...(host !== undefined ? { host } : {}) } } })
const blank = (id: string): PaneLayout => ({ type: 'leaf', pane: { id, content: { kind: 'new-tab' } } })
const split = (id: string, children: PaneLayout[]): PaneLayout =>
  ({ type: 'split', id, direction: 'h', children, sizes: children.map(() => 100 / children.length) })
const tab = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 0, layout })

// #1853: every agent key on screen in the active tab — what unread marking and auto mark-read look at.
describe('getActiveTabAgents (#1853)', () => {
  beforeEach(() => {
    useHostStore.setState({ hostOrder: ['h1'] })
  })

  it('[] with no active tab, or an active id that names no tab', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', tmux('p1', 'h', 'dev')) }, activeTabId: null })
    expect(getActiveTabAgents()).toEqual([])
    useTabStore.setState({ activeTabId: 'nonexistent' })
    expect(getActiveTabAgents()).toEqual([])
  })

  it('[] when the active tab shows no agent', () => {
    const settings = { ...createTab({ kind: 'settings', scope: 'global' }), id: 't3' }
    useTabStore.setState({ tabs: { t3: settings }, activeTabId: 't3' })
    expect(getActiveTabAgents()).toEqual([])
    useTabStore.setState({ tabs: { t3: tab('t3', split('s1', [blank('p1'), blank('p2')])) } })
    expect(getActiveTabAgents()).toEqual([])
  })

  it('a single session tab → its host and code', () => {
    const t1 = { ...createTab({ kind: 'tmux-session', hostId: 'test-host', sessionCode: 'dev', mode: 'terminal', cachedName: '', tmuxInstance: '' }), id: 't1' }
    useTabStore.setState({ tabs: { t1 }, activeTabId: 't1' })
    expect(getActiveTabAgents()).toEqual([{ hostId: 'test-host', sessionCode: 'dev' }])
  })

  it('every pane of a split, at any depth, in layout order, each key once', () => {
    useTabStore.setState({
      tabs: {
        t1: tab('t1', split('s1', [
          tmux('p1', 'h', 'aaa'),
          split('s2', [blank('p2'), exec('p3', 'e1', 'h2'), split('s3', [tmux('p4', 'h', 'bbb'), tmux('p5', 'h', 'aaa')])]),
          tmux('p6', 'h2', 'aaa'),
          exec('p7', 'e1', 'h2'),
        ])),
        t2: tab('t2', tmux('p8', 'h', 'ccc')),
      },
      activeTabId: 't1',
    })
    expect(getActiveTabAgents()).toEqual([
      { hostId: 'h', sessionCode: 'aaa' },
      { hostId: 'h2', sessionCode: 'exec-e1' },
      { hostId: 'h', sessionCode: 'bbb' },
      // the same code on another host is a different agent
      { hostId: 'h2', sessionCode: 'aaa' },
    ])
  })

  it('an ended (terminated) pane shows no agent; a live pane on the same code still does', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', split('s1', [ended('p1', 'h', 'aaa'), tmux('p2', 'h', 'bbb')])) }, activeTabId: 't1' })
    expect(getActiveTabAgents()).toEqual([{ hostId: 'h', sessionCode: 'bbb' }])
    useTabStore.setState({ tabs: { t1: tab('t1', split('s1', [ended('p1', 'h', 'aaa'), tmux('p2', 'h', 'aaa')])) } })
    expect(getActiveTabAgents()).toEqual([{ hostId: 'h', sessionCode: 'aaa' }])
  })

  it('names exactly the keys isAgentVisibleInActiveTab calls visible (one rule)', () => {
    useHostStore.setState({ hostOrder: ['h9'] })
    useTabStore.setState({
      tabs: { t1: tab('t1', split('s1', [ended('p1', 'h', 'aaa'), split('s2', [tmux('p2', 'h', 'bbb'), exec('p3', 'e1'), exec('p4', 'e2', 'h2')])])) },
      activeTabId: 't1',
    })
    const shown = new Set(getActiveTabAgents().map((a) => compositeKey(a.hostId, a.sessionCode)))
    const probes: Array<[string, string]> = [['h', 'aaa'], ['h', 'bbb'], ['h2', 'bbb'], ['h9', 'exec-e1'], ['h1', 'exec-e1'], ['h2', 'exec-e2'], ['h2', 'e2']]
    for (const [hostId, code] of probes) {
      expect(shown.has(compositeKey(hostId, code))).toBe(isAgentVisibleInActiveTab(hostId, code))
    }
  })
})

describe('getActiveTabAgents — worker (execution) pane (spec §8.2)', () => {
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
    expect(getActiveTabAgents()).toEqual([{ hostId: 'h2', sessionCode: 'exec-e1' }])
  })

  it('a host-less pane resolves to the first host', () => {
    useTabStore.setState({ tabs: { tx: execTab() }, activeTabId: 'tx' })
    expect(getActiveTabAgents()).toEqual([{ hostId: 'h1', sessionCode: 'exec-e1' }])
  })

  it('a pane with an empty-string host resolves to the first host, same as no host', () => {
    useTabStore.setState({ tabs: { tx: tab('tx', exec('px', 'e1', '')) }, activeTabId: 'tx' })
    expect(getActiveTabAgents()).toEqual([{ hostId: 'h1', sessionCode: 'exec-e1' }])
  })

  it('a worker in a secondary pane resolves the same way', () => {
    useTabStore.setState({ tabs: { tx: tab('tx', split('s1', [blank('p1'), exec('p2', 'e1', '')])) }, activeTabId: 'tx' })
    expect(getActiveTabAgents()).toEqual([{ hostId: 'h1', sessionCode: 'exec-e1' }])
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

  // #1840 review A2: an ended pane's code may be a NEW live session's (tmux restarts reuse `$N`); it shows nothing.
  it('an ended (terminated) pane in the active tab does not show the agent', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', ended('p1', 'h', 'abc123')) }, activeTabId: 't1' })
    expect(isAgentVisibleInActiveTab('h', 'abc123')).toBe(false)
    useTabStore.setState({ tabs: { t1: tab('t1', split('s1', [blank('p1'), ended('p2', 'h', 'abc123')])) }, activeTabId: 't1' })
    expect(isAgentVisibleInActiveTab('h', 'abc123')).toBe(false)
    // An ended primary next to the live session in a secondary pane: the live one is on screen.
    useTabStore.setState({ tabs: { t1: tab('t1', split('s1', [ended('p1', 'h', 'abc123'), tmux('p2', 'h', 'abc123')])) }, activeTabId: 't1' })
    expect(isAgentVisibleInActiveTab('h', 'abc123')).toBe(true)
  })
})
