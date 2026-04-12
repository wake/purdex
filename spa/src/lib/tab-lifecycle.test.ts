// spa/src/lib/tab-lifecycle.test.ts — Tests for unified closeTab helper
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useTabStore } from '../stores/useTabStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { createTab } from '../types/tab'
import { closeTab } from './tab-lifecycle'

function resetStores() {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
  useWorkspaceStore.getState().reset()
}

describe('closeTab', () => {
  beforeEach(() => {
    resetStores()
    // @ts-expect-error -- test-only partial mock
    window.electronAPI = { destroyBrowserView: vi.fn() }
  })

  afterEach(() => {
    // @ts-expect-error -- cleanup
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
