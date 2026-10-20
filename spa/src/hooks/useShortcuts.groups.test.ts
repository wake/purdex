// spa/src/hooks/useShortcuts.groups.test.ts — the tab shortcuts act on the selected group's tabs in the task-group view (task groups spec §6.2, TG-4a).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

// the member collapse of the workspace bar (spec R8): a spy over the real one, to see whether a view applies it
const collapseSpy = vi.hoisted(() => vi.fn())
vi.mock('../lib/team/team-actions', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../lib/team/team-actions')>()
  return { ...actual, visibleTabIds: (...a: Parameters<typeof actual.visibleTabIds>) => { collapseSpy(); return actual.visibleTabIds(...a) } }
})

import { renderHook } from '@testing-library/react'
import { useShortcuts } from './useShortcuts'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useManualGroupsStore } from '../stores/useManualGroupsStore'
import { useGroupViewStore } from '../stores/useGroupViewStore'
import { useLayoutStore } from '../stores/useLayoutStore'
import { useTeamUiStore } from '../stores/useTeamUiStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { manualGroupKey } from '../lib/task-groups/group-key'
import { createTab, type Tab } from '../types/tab'

const G = manualGroupKey('aaaaaaaa')

function fireShortcut() {
  let cb: ((p: { action: string }) => void) | null = null
  ;(window as unknown as Record<string, unknown>).electronAPI = {
    onShortcut: (f: (p: { action: string }) => void) => { cb = f; return () => {} },
    signalReady: () => {},
  }
  return (action: string) => cb?.({ action })
}

/** Tabs d (rank A), a (M), b (T) are in the group — d in workspace 2, a and b in workspace 1 — and c is in workspace 1 outside it. The group's range is d, a, b. */
function seed(view: 'groups' | 'workspace') {
  const mk = (over: Partial<Tab>): Tab => ({ ...createTab({ kind: 'new-tab' }), ...over })
  const a = mk({ group: G, groupRank: 'M' })
  const b = mk({ group: G, groupRank: 'T' })
  const c = mk({})
  const d = mk({ group: G, groupRank: 'A' })
  useTabStore.setState({ tabs: Object.fromEntries([a, b, c, d].map((t) => [t.id, t])), tabOrder: [a.id, b.id, c.id, d.id], activeTabId: a.id })
  useWorkspaceStore.setState({
    workspaces: [
      { id: 'w1', name: 'W1', tabs: [a.id, b.id, c.id], activeTabId: a.id },
      { id: 'w2', name: 'W2', tabs: [d.id], activeTabId: d.id },
    ],
    activeWorkspaceId: 'w1',
  } as never)
  useManualGroupsStore.setState({ order: ['aaaaaaaa'], groups: { aaaaaaaa: { name: 'G', members: [], createdAt: 1 } } })
  useGroupViewStore.setState({ selectedGroup: G, lastActiveTab: {}, expanded: {}, lastByView: { workspace: null, group: null } })
  useTeamUiStore.setState({ sidebarView: view })
  useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'top' })
  return { a, b, c, d }
}

beforeEach(() => {
  collapseSpy.mockClear()
  useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
})
afterEach(() => {
  delete (window as unknown as Record<string, unknown>).electronAPI
  useTeamUiStore.setState({ sidebarView: 'workspace' })
})

describe('the tab shortcuts in the task-group view', () => {
  it('⌘1–9 pick from the selected group\'s tabs, across workspaces, in the bar\'s order', () => {
    const { a, b, d } = seed('groups')
    const fire = fireShortcut()
    renderHook(() => useShortcuts())
    fire('switch-tab-1')
    expect(useTabStore.getState().activeTabId).toBe(d.id)
    fire('switch-tab-3')
    expect(useTabStore.getState().activeTabId).toBe(b.id)
    fire('switch-tab-last')
    expect(useTabStore.getState().activeTabId).toBe(b.id)
    fire('switch-tab-2')
    expect(useTabStore.getState().activeTabId).toBe(a.id)
  })

  it('next / previous step through the group\'s tabs and wrap', () => {
    const { a, b, d } = seed('groups')
    const fire = fireShortcut()
    renderHook(() => useShortcuts())
    fire('next-tab')
    expect(useTabStore.getState().activeTabId).toBe(b.id)
    fire('next-tab')
    expect(useTabStore.getState().activeTabId).toBe(d.id)
    fire('prev-tab')
    expect(useTabStore.getState().activeTabId).toBe(b.id)
    fire('prev-tab')
    expect(useTabStore.getState().activeTabId).toBe(a.id)
  })

  it('a group\'s bar lists every tab: the member collapse of the workspace bar is not applied (it is, in the workspace view)', () => {
    seed('groups')
    const fire = fireShortcut()
    renderHook(() => useShortcuts())
    fire('next-tab')
    expect(collapseSpy).not.toHaveBeenCalled()
    useTeamUiStore.setState({ sidebarView: 'workspace' })
    fire('next-tab')
    expect(collapseSpy).toHaveBeenCalled()
  })

  it('the same keys in the workspace view act on the workspace\'s tabs', () => {
    const { a, b, c } = seed('workspace')
    const fire = fireShortcut()
    renderHook(() => useShortcuts())
    fire('switch-tab-3')
    expect(useTabStore.getState().activeTabId).toBe(c.id)
    fire('next-tab')
    expect(useTabStore.getState().activeTabId).toBe(a.id) // wraps inside w1: a, b, c
    expect(b.id).toBeDefined()
  })

  it('a narrow sidebar behaves as the workspace view whatever the stored view says', () => {
    const { c } = seed('groups')
    useLayoutStore.setState({ activityBarWidth: 'narrow' })
    const fire = fireShortcut()
    renderHook(() => useShortcuts())
    fire('switch-tab-3')
    expect(useTabStore.getState().activeTabId).toBe(c.id)
  })

  it('no group selected yet: the workspace rules', () => {
    const { c } = seed('groups')
    useGroupViewStore.setState({ selectedGroup: null })
    const fire = fireShortcut()
    renderHook(() => useShortcuts())
    fire('switch-tab-3')
    expect(useTabStore.getState().activeTabId).toBe(c.id)
  })

  it('⌘W closes the active tab of the group, and leaves alone a tab of another group the pointer is stale at', () => {
    const { a, c } = seed('groups')
    const fire = fireShortcut()
    renderHook(() => useShortcuts())
    useTabStore.setState({ activeTabId: c.id }) // c is in the workspace, in no selected-group range
    fire('close-tab')
    expect(useTabStore.getState().tabs[c.id]).toBeDefined()
    useTabStore.setState({ activeTabId: a.id })
    fire('close-tab')
    expect(useTabStore.getState().tabs[a.id]).toBeUndefined()
  })
})
