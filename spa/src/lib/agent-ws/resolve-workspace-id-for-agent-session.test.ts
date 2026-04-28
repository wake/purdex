import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import type { Tab, PaneContent } from '../../types/tab'
import { resolveWorkspaceIdForAgentSession } from './resolve-workspace-id-for-agent-session'

const seedTab = (id: string, content: PaneContent): Tab => ({
  id,
  pinned: false,
  locked: false,
  createdAt: 0,
  layout: { type: 'leaf', pane: { id: `p_${id}`, content } },
})

describe('resolveWorkspaceIdForAgentSession', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [] } as never, false)
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null } as never, false)
  })

  it('returns null when no tab matches the session', () => {
    expect(resolveWorkspaceIdForAgentSession('h1', 'sess')).toBeNull()
  })

  it('returns the unique workspace when only one tab matches (ignores active hint)', () => {
    const t = seedTab('t1', { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess', mode: 'terminal', cachedName: 'work', tmuxInstance: 'i1' })
    useTabStore.setState({ tabs: { t1: t }, tabOrder: ['t1'] } as never, false)
    useWorkspaceStore.setState({
      workspaces: [
        { id: 'w1', name: 'A', tabs: ['t1'], activeTabId: 't1', moduleConfig: {} },
        { id: 'w2', name: 'B', tabs: [], activeTabId: null, moduleConfig: {} },
      ],
      activeWorkspaceId: 'w2',
    } as never, false)
    expect(resolveWorkspaceIdForAgentSession('h1', 'sess')).toBe('w1')
  })

  it('returns null when multiple workspaces own matching tabs (avoids racy active-priority)', () => {
    const t1 = seedTab('t1', { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess', mode: 'terminal', cachedName: 'work', tmuxInstance: 'i1' })
    const t2 = seedTab('t2', { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess', mode: 'terminal', cachedName: 'work', tmuxInstance: 'i1' })
    useTabStore.setState({ tabs: { t1, t2 }, tabOrder: ['t1', 't2'] } as never, false)
    useWorkspaceStore.setState({
      workspaces: [
        { id: 'w1', name: 'A', tabs: ['t1'], activeTabId: 't1', moduleConfig: {} },
        { id: 'w2', name: 'B', tabs: ['t2'], activeTabId: 't2', moduleConfig: {} },
      ],
      activeWorkspaceId: 'w2',
    } as never, false)
    expect(resolveWorkspaceIdForAgentSession('h1', 'sess')).toBeNull()
  })

  it('returns null when the matching tab is standalone (no workspace owns it)', () => {
    const t = seedTab('t1', { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess', mode: 'terminal', cachedName: 'work', tmuxInstance: 'i1' })
    useTabStore.setState({ tabs: { t1: t }, tabOrder: ['t1'] } as never, false)
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w1', name: 'A', tabs: [], activeTabId: null, moduleConfig: {} }],
      activeWorkspaceId: 'w1',
    } as never, false)
    expect(resolveWorkspaceIdForAgentSession('h1', 'sess')).toBeNull()
  })

  it('matches sessions hosted in a non-primary pane of a split layout', () => {
    const splitTab: Tab = {
      id: 't1',
      pinned: false,
      locked: false,
      createdAt: 0,
      layout: {
        type: 'split',
        id: 's1',
        direction: 'h',
        children: [
          { type: 'leaf', pane: { id: 'pa', content: { kind: 'new-tab' } } },
          { type: 'leaf', pane: { id: 'pb', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess', mode: 'terminal', cachedName: 'work', tmuxInstance: 'i1' } } },
        ],
        sizes: [50, 50],
      },
    }
    useTabStore.setState({ tabs: { t1: splitTab }, tabOrder: ['t1'] } as never, false)
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w1', name: 'A', tabs: ['t1'], activeTabId: 't1', moduleConfig: {} }],
      activeWorkspaceId: 'w1',
    } as never, false)
    expect(resolveWorkspaceIdForAgentSession('h1', 'sess')).toBe('w1')
  })

  it('discriminates by hostId when sessionCode collides across hosts', () => {
    const t1 = seedTab('t1', { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess', mode: 'terminal', cachedName: 'work', tmuxInstance: 'i1' })
    const t2 = seedTab('t2', { kind: 'tmux-session', hostId: 'h2', sessionCode: 'sess', mode: 'terminal', cachedName: 'work', tmuxInstance: 'i1' })
    useTabStore.setState({ tabs: { t1, t2 }, tabOrder: ['t1', 't2'] } as never, false)
    useWorkspaceStore.setState({
      workspaces: [
        { id: 'w1', name: 'A', tabs: ['t1'], activeTabId: 't1', moduleConfig: {} },
        { id: 'w2', name: 'B', tabs: ['t2'], activeTabId: 't2', moduleConfig: {} },
      ],
      activeWorkspaceId: 'w1',
    } as never, false)
    expect(resolveWorkspaceIdForAgentSession('h1', 'sess')).toBe('w1')
    expect(resolveWorkspaceIdForAgentSession('h2', 'sess')).toBe('w2')
  })
})
