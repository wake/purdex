// spa/src/lib/tab-lifecycle.test.ts — Tests for unified closeTab helper
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useTabStore } from '../stores/useTabStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { useEditorStore } from '../stores/useEditorStore'
import { createTab } from '../types/tab'
import type { PaneContent, PaneLayout } from '../types/tab'
import type { FileSource } from '../types/fs'
import { closeTab } from './tab-lifecycle'
import { bufferKey } from './editor-buffer-key'
import { useManualGroupsStore } from '../stores/useManualGroupsStore'
import { useGroupViewStore } from '../stores/useGroupViewStore'
import { useLayoutStore } from '../stores/useLayoutStore'
import { useTeamUiStore } from '../stores/useTeamUiStore'
import { manualGroupKey } from './task-groups/group-key'

function resetStores() {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
  useWorkspaceStore.getState().reset()
}

describe('closeTab', () => {
  beforeEach(() => {
    resetStores()
    Object.defineProperty(window, 'electronAPI', {
      value: { destroyBrowserView: vi.fn() },
      configurable: true,
      writable: true,
    })
  })

  afterEach(() => {
    delete window.electronAPI
  })

  it('closes an unlocked tab', () => {
    const tab = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(tab)
    expect(useTabStore.getState().tabs[tab.id]).toBeDefined()

    closeTab(tab.id)

    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
  })

  it('calls destroyBrowserViewIfNeeded for browser tabs', () => {
    const tab = createTab({ kind: 'browser', url: 'https://example.com' })
    useTabStore.getState().addTab(tab)

    closeTab(tab.id)

    const paneId = tab.layout.type === 'leaf' ? tab.layout.pane.id : ''
    expect(window.electronAPI?.destroyBrowserView).toHaveBeenCalledWith(paneId)
  })

  it('does not close a locked tab', () => {
    const tab = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(tab)
    useTabStore.getState().toggleLock(tab.id)
    expect(useTabStore.getState().tabs[tab.id]?.locked).toBe(true)

    closeTab(tab.id)

    expect(useTabStore.getState().tabs[tab.id]).toBeDefined()
  })

  it('does nothing for nonexistent tabId', () => {
    // Should not throw
    expect(() => closeTab('nonexistent')).not.toThrow()
  })

  it('forwards skipHistory option', () => {
    const tab = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(tab)

    closeTab(tab.id, { skipHistory: true })

    // Tab should be removed
    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
    // History should NOT have a record
    expect(useHistoryStore.getState().closedTabs).toHaveLength(0)
  })

  it('records to history by default', () => {
    const tab = createTab({ kind: 'new-tab' })
    useTabStore.getState().addTab(tab)

    closeTab(tab.id)

    expect(useHistoryStore.getState().closedTabs).toHaveLength(1)
  })
})

describe('closeTab — unsaved editor warning', () => {
  let closeSpy: ReturnType<typeof vi.spyOn>
  let confirmSpy: ReturnType<typeof vi.spyOn>

  function editorContent(filePath: string, source: FileSource = { type: 'inapp' }): PaneContent {
    return { kind: 'editor', source, filePath }
  }

  function seedBuffer(filePath: string, isDirty: boolean, source: FileSource = { type: 'inapp' }) {
    const key = bufferKey(source, filePath)
    useEditorStore.setState({
      buffers: {
        ...useEditorStore.getState().buffers,
        [key]: { isDirty } as never,
      },
    })
  }

  function addTabWithLayout(layout: PaneLayout): string {
    const tab = createTab({ kind: 'new-tab' })
    const withLayout = { ...tab, layout }
    useTabStore.getState().addTab(withLayout)
    return withLayout.id
  }

  beforeEach(() => {
    resetStores()
    useEditorStore.setState({ buffers: {} })
    Object.defineProperty(window, 'electronAPI', {
      value: { destroyBrowserView: vi.fn() },
      configurable: true,
      writable: true,
    })
    closeSpy = vi.spyOn(useWorkspaceStore.getState(), 'closeTabInWorkspace')
    confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  afterEach(() => {
    closeSpy.mockRestore()
    confirmSpy.mockRestore()
    delete window.electronAPI
  })

  it('confirms and closes when editor is dirty and user accepts', () => {
    seedBuffer('/a.md', true)
    const id = addTabWithLayout({ type: 'leaf', pane: { id: 'p1', content: editorContent('/a.md') } })

    closeTab(id)

    expect(confirmSpy).toHaveBeenCalledTimes(1)
    expect(closeSpy).toHaveBeenCalledWith(id, undefined)
  })

  it('aborts close when editor is dirty and user cancels', () => {
    confirmSpy.mockReturnValue(false)
    seedBuffer('/a.md', true)
    const id = addTabWithLayout({ type: 'leaf', pane: { id: 'p1', content: editorContent('/a.md') } })

    closeTab(id)

    expect(confirmSpy).toHaveBeenCalledTimes(1)
    expect(closeSpy).not.toHaveBeenCalled()
  })

  it('does not confirm when editor buffer is clean', () => {
    seedBuffer('/a.md', false)
    const id = addTabWithLayout({ type: 'leaf', pane: { id: 'p1', content: editorContent('/a.md') } })

    closeTab(id)

    expect(confirmSpy).not.toHaveBeenCalled()
    expect(closeSpy).toHaveBeenCalledWith(id, undefined)
  })

  it('confirms and closes when a daemon-sourced editor is dirty (H1a)', () => {
    const daemon: FileSource = { type: 'daemon', hostId: 'h1' }
    seedBuffer('/a.md', true, daemon)
    const id = addTabWithLayout({
      type: 'leaf',
      pane: { id: 'p1', content: editorContent('/a.md', daemon) },
    })

    closeTab(id)

    expect(confirmSpy).toHaveBeenCalledTimes(1)
    expect(closeSpy).toHaveBeenCalledWith(id, undefined)
  })

  it('does not confirm when a daemon-sourced editor buffer is clean (H1a)', () => {
    const daemon: FileSource = { type: 'daemon', hostId: 'h1' }
    seedBuffer('/a.md', false, daemon)
    const id = addTabWithLayout({
      type: 'leaf',
      pane: { id: 'p1', content: editorContent('/a.md', daemon) },
    })

    closeTab(id)

    expect(confirmSpy).not.toHaveBeenCalled()
    expect(closeSpy).toHaveBeenCalledWith(id, undefined)
  })

  it('does not confirm when no buffer exists for the editor pane', () => {
    const id = addTabWithLayout({ type: 'leaf', pane: { id: 'p1', content: editorContent('/missing.md') } })

    closeTab(id)

    expect(confirmSpy).not.toHaveBeenCalled()
    expect(closeSpy).toHaveBeenCalledWith(id, undefined)
  })

  it('does not confirm for non-editor tabs', () => {
    const id = addTabWithLayout({ type: 'leaf', pane: { id: 'p1', content: { kind: 'new-tab' } } })

    closeTab(id)

    expect(confirmSpy).not.toHaveBeenCalled()
    expect(closeSpy).toHaveBeenCalledWith(id, undefined)
  })

  it('confirms exactly once for a split tab with one dirty and one clean editor', () => {
    seedBuffer('/dirty.md', true)
    seedBuffer('/clean.md', false)
    const id = addTabWithLayout({
      type: 'split',
      id: 's1',
      direction: 'h',
      children: [
        { type: 'leaf', pane: { id: 'p1', content: editorContent('/dirty.md') } },
        { type: 'leaf', pane: { id: 'p2', content: editorContent('/clean.md') } },
      ],
      sizes: [50, 50],
    })

    closeTab(id)

    expect(confirmSpy).toHaveBeenCalledTimes(1)
    expect(closeSpy).toHaveBeenCalledWith(id, undefined)
  })

  it('returns early for a locked tab without confirming', () => {
    seedBuffer('/a.md', true)
    const id = addTabWithLayout({ type: 'leaf', pane: { id: 'p1', content: editorContent('/a.md') } })
    useTabStore.getState().toggleLock(id)

    closeTab(id)

    expect(confirmSpy).not.toHaveBeenCalled()
    expect(closeSpy).not.toHaveBeenCalled()
  })

  it('confirms and closes for a dirty untitled draft (AC11b)', () => {
    seedBuffer('untitled:draft', true)
    const id = addTabWithLayout({
      type: 'leaf',
      pane: { id: 'p1', content: editorContent('untitled:draft') },
    })

    closeTab(id)

    expect(confirmSpy).toHaveBeenCalledTimes(1)
    expect(closeSpy).toHaveBeenCalledWith(id, undefined)
  })

  it('does not confirm for a clean untitled draft (AC11b)', () => {
    seedBuffer('untitled:draft', false)
    const id = addTabWithLayout({
      type: 'leaf',
      pane: { id: 'p1', content: editorContent('untitled:draft') },
    })

    closeTab(id)

    expect(confirmSpy).not.toHaveBeenCalled()
    expect(closeSpy).toHaveBeenCalledWith(id, undefined)
  })
})

// TG-4a — every close goes through closeTab (the bar's button, ⌘W, the menu, the content area's close buttons): in the
// task-group view the tab shown next comes from the selected group's range, not from the closed tab's workspace.
describe('closeTab in the task-group view', () => {
  const G = manualGroupKey('aaaaaaaa')
  afterEach(() => useTeamUiStore.setState({ sidebarView: 'workspace' }))

  function world(view: 'groups' | 'workspace') {
    resetStores()
    const g1 = { ...createTab({ kind: 'new-tab' }), group: G, groupRank: 'A' }
    const g2 = { ...createTab({ kind: 'new-tab' }), group: G, groupRank: 'M' }
    const x = createTab({ kind: 'new-tab' })
    useTabStore.setState({ tabs: { [g1.id]: g1, [g2.id]: g2, [x.id]: x }, tabOrder: [g1.id, x.id, g2.id], activeTabId: g1.id, visitHistory: [] })
    const wsA = useWorkspaceStore.getState().addWorkspace('A')
    const wsB = useWorkspaceStore.getState().addWorkspace('B')
    for (const t of [g1, x]) useWorkspaceStore.getState().addTabToWorkspace(wsA.id, t.id)
    useWorkspaceStore.getState().addTabToWorkspace(wsB.id, g2.id)
    useWorkspaceStore.getState().setActiveWorkspace(wsA.id)
    useManualGroupsStore.setState({ order: ['aaaaaaaa'], groups: { aaaaaaaa: { name: 'G', members: [], createdAt: 1 } } })
    useGroupViewStore.setState({ selectedGroup: G, lastActiveTab: {}, expanded: {}, lastByView: { workspace: null, group: null } })
    useTeamUiStore.setState({ sidebarView: view })
    useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'top' })
    return { g1, g2, x, wsB }
  }

  it('the next tab is the group\'s neighbour in another workspace, and that workspace is shown', () => {
    const { g1, g2, wsB } = world('groups')
    closeTab(g1.id)
    expect(useTabStore.getState().activeTabId).toBe(g2.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(wsB.id)
  })

  it('in the workspace view it is the workspace\'s neighbour, as before', () => {
    const { g1, x } = world('workspace')
    closeTab(g1.id)
    expect(useTabStore.getState().activeTabId).toBe(x.id)
  })

  it('an explicit scope wins over the range', () => {
    const { g1, x } = world('groups')
    closeTab(g1.id, { scope: [g1.id, x.id] })
    expect(useTabStore.getState().activeTabId).toBe(x.id)
  })
})
