// spa/src/lib/tab-lifecycle.test.ts — Tests for unified closeTab helper
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useTabStore } from '../stores/useTabStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { useEditorStore } from '../stores/useEditorStore'
import { createTab } from '../types/tab'
import type { PaneContent, PaneLayout } from '../types/tab'
import { closeTab } from './tab-lifecycle'
import { bufferKey } from './editor-buffer-key'

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

  function editorContent(filePath: string): PaneContent {
    return { kind: 'editor', source: { type: 'inapp' }, filePath }
  }

  function seedBuffer(filePath: string, isDirty: boolean) {
    const key = bufferKey({ type: 'inapp' }, filePath)
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
