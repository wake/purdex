import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useTabStore } from '../stores/useTabStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useHostStore } from '../stores/useHostStore'
import { createTab } from '../types/tab'
import type { PaneContent, PaneLayout, Tab } from '../types/tab'
import { compositeKey } from './composite-key'
import { collectLeaves } from './pane-tree'
import { startActiveTabMarkRead } from './active-tab-mark-read'

const execTab: Tab = {
  id: 'tx', pinned: false, locked: false, createdAt: 0,
  layout: { type: 'leaf', pane: { id: 'px', content: { kind: 'execution', executionId: 'e1', host: 'h1' } } },
}

const realMarkRead = useAgentStore.getState().markRead
let stop: () => void = () => {}

beforeEach(() => {
  useHostStore.setState({ hostOrder: ['h1'] })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useAgentStore.setState({ unread: {} })
})
afterEach(() => {
  stop()
  stop = () => {}
  useAgentStore.setState({ markRead: realMarkRead })
})

describe('startActiveTabMarkRead', () => {
  it('activating exec tab marks read', () => {
    const ck = compositeKey('h1', 'exec-e1')
    useAgentStore.setState({ unread: { [ck]: true } })
    stop = startActiveTabMarkRead()
    useTabStore.setState({ tabs: { tx: execTab }, activeTabId: 'tx' })
    expect(useAgentStore.getState().unread[ck]).toBeUndefined()
  })

  it('activating a tmux tab marks read (unchanged)', () => {
    const tab = { ...createTab({ kind: 'tmux-session', hostId: 'h1', sessionCode: 'dev', mode: 'terminal', cachedName: '', tmuxInstance: '' }), id: 't1' }
    const ck = compositeKey('h1', 'dev')
    useAgentStore.setState({ unread: { [ck]: true } })
    stop = startActiveTabMarkRead()
    useTabStore.setState({ tabs: { t1: tab }, activeTabId: 't1' })
    expect(useAgentStore.getState().unread[ck]).toBeUndefined()
  })

  it('a tab-store change that keeps the same active tab does not mark read again', () => {
    const ck = compositeKey('h1', 'exec-e1')
    useTabStore.setState({ tabs: { tx: execTab }, activeTabId: 'tx' })
    stop = startActiveTabMarkRead()
    useAgentStore.setState({ unread: { [ck]: true } })
    useTabStore.setState({ tabOrder: ['tx'] })
    expect(useAgentStore.getState().unread[ck]).toBe(true)
  })
})

// #1853: every agent a pane of the active tab shows is on screen — not only the primary pane's.
describe('startActiveTabMarkRead — every pane of the active tab (#1853)', () => {
  const tmuxContent = (sessionCode: string, hostId = 'h1'): PaneContent =>
    ({ kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: '', tmuxInstance: '' })
  const tmux = (id: string, sessionCode: string, hostId = 'h1'): PaneLayout => ({ type: 'leaf', pane: { id, content: tmuxContent(sessionCode, hostId) } })
  const ended = (id: string, sessionCode: string): PaneLayout =>
    ({ type: 'leaf', pane: { id, content: { ...tmuxContent(sessionCode), terminated: 'tmux-restarted' } as PaneContent } })
  const exec = (id: string, executionId: string): PaneLayout =>
    ({ type: 'leaf', pane: { id, content: { kind: 'execution', executionId, host: 'h1' } } })
  const blank = (id: string): PaneLayout => ({ type: 'leaf', pane: { id, content: { kind: 'new-tab' } } })
  const split = (id: string, children: PaneLayout[]): PaneLayout =>
    ({ type: 'split', id, direction: 'h', children, sizes: children.map(() => 100 / children.length) })
  const tab = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 0, layout })
  const unread = (code: string, hostId = 'h1') => useAgentStore.getState().unread[compositeKey(hostId, code)]
  const setUnread = (...keys: Array<[string, string]>) =>
    useAgentStore.setState({ unread: Object.fromEntries(keys.map(([hostId, code]) => [compositeKey(hostId, code), true])) })

  it('switching to a split tab marks every live agent of it read; agents not in it stay unread', () => {
    useTabStore.setState({
      tabs: {
        t1: tab('t1', tmux('p0', 'zzz')),
        t2: tab('t2', split('s1', [tmux('p1', 'aaa'), split('s2', [blank('p2'), split('s3', [tmux('p3', 'bbb'), exec('p4', 'e1')])])])),
      },
      activeTabId: 't1',
    })
    stop = startActiveTabMarkRead()
    setUnread(['h1', 'aaa'], ['h1', 'bbb'], ['h1', 'exec-e1'], ['h1', 'ccc'], ['h2', 'aaa'])
    useTabStore.getState().setActiveTab('t2')
    expect(unread('aaa')).toBeUndefined()
    expect(unread('bbb')).toBeUndefined()
    expect(unread('exec-e1')).toBeUndefined()
    expect(unread('ccc')).toBe(true)
    // the same code on another host is another agent
    expect(unread('aaa', 'h2')).toBe(true)
  })

  it('splitting the active tab and opening an agent in the new pane marks that agent read, and only it', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', tmux('p1', 'aaa')) }, activeTabId: 't1' })
    stop = startActiveTabMarkRead()
    setUnread(['h1', 'aaa'], ['h1', 'bbb'])
    useTabStore.getState().splitPaneBlank('t1', 'p1', 'h')
    const fresh = collectLeaves(useTabStore.getState().tabs.t1.layout).find((p) => p.id !== 'p1')!
    useTabStore.getState().setPaneContent('t1', fresh.id, tmuxContent('bbb'))
    expect(unread('bbb')).toBeUndefined()
    // 'aaa' was already on screen: only an agent that newly appears is marked
    expect(unread('aaa')).toBe(true)
  })

  it('an ended (terminated) pane does not mark its key read', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', blank('p0')), t2: tab('t2', split('s1', [ended('p1', 'aaa'), tmux('p2', 'bbb')])) }, activeTabId: 't1' })
    stop = startActiveTabMarkRead()
    setUnread(['h1', 'aaa'], ['h1', 'bbb'])
    useTabStore.getState().setActiveTab('t2')
    expect(unread('aaa')).toBe(true)
    expect(unread('bbb')).toBeUndefined()
  })

  it('a tab-store change that leaves the same agents on screen makes no markRead call', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', split('s1', [tmux('p1', 'aaa'), exec('p2', 'e1')])) }, activeTabId: 't1' })
    stop = startActiveTabMarkRead()
    const markRead = vi.fn()
    useAgentStore.setState({ markRead })
    useTabStore.setState({ tabOrder: ['t1'] })
    // same agents, other order and an extra blank pane
    useTabStore.setState({ tabs: { t1: tab('t1', split('s1', [exec('p2', 'e1'), blank('p3'), tmux('p1', 'aaa')])) } })
    expect(markRead).not.toHaveBeenCalled()
  })

  it('starting makes no markRead call', () => {
    useTabStore.setState({ tabs: { t1: tab('t1', split('s1', [tmux('p1', 'aaa'), exec('p2', 'e1')])) }, activeTabId: 't1' })
    const markRead = vi.fn()
    useAgentStore.setState({ markRead })
    stop = startActiveTabMarkRead()
    expect(markRead).not.toHaveBeenCalled()
  })
})
