import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'
import type { PaneContent } from '../types/tab'

const newTabContent: PaneContent = { kind: 'new-tab' }

beforeEach(() => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
})

// Helper: create a tab and add it to the store, returning its id
function addTab(content: PaneContent = newTabContent) {
  const tab = createTab(content)
  useTabStore.getState().addTab(tab)
  return tab
}

describe('splitPane', () => {
  it('splits a leaf pane into a horizontal split', () => {
    const tab = addTab()
    const paneId = (tab.layout as { pane: { id: string } }).pane.id
    const splitContent: PaneContent = { kind: 'dashboard' }

    useTabStore.getState().splitPane(tab.id, paneId, 'h', splitContent)

    const updatedTab = useTabStore.getState().tabs[tab.id]
    expect(updatedTab.layout.type).toBe('split')
    if (updatedTab.layout.type === 'split') {
      expect(updatedTab.layout.direction).toBe('h')
      expect(updatedTab.layout.children).toHaveLength(2)
      expect(updatedTab.layout.sizes).toEqual([50, 50])
      expect(updatedTab.layout.children[0]).toEqual({ type: 'leaf', pane: tab.layout.type === 'leaf' ? tab.layout.pane : null })
      const secondChild = updatedTab.layout.children[1]
      expect(secondChild.type).toBe('leaf')
      if (secondChild.type === 'leaf') {
        expect(secondChild.pane.content).toEqual(splitContent)
      }
    }
  })

  it('is a no-op for a nonexistent tab', () => {
    const stateBefore = useTabStore.getState().tabs

    useTabStore.getState().splitPane('nonexistent-tab-id', 'pane-id', 'v', newTabContent)

    expect(useTabStore.getState().tabs).toBe(stateBefore)
  })
})

describe('closePane', () => {
  it('closes the entire tab when the tab has only one pane', () => {
    const tab = addTab()
    expect(useTabStore.getState().tabs[tab.id]).toBeDefined()

    const paneId = (tab.layout as { pane: { id: string } }).pane.id
    useTabStore.getState().closePane(tab.id, paneId)

    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
    expect(useTabStore.getState().tabOrder).not.toContain(tab.id)
  })

  it('promotes the sibling when closing a pane in a split', () => {
    const tab = addTab()
    const originalPaneId = (tab.layout as { pane: { id: string } }).pane.id
    const splitContent: PaneContent = { kind: 'hosts' }

    // First split to create a two-pane layout
    useTabStore.getState().splitPane(tab.id, originalPaneId, 'h', splitContent)

    const splitLayout = useTabStore.getState().tabs[tab.id].layout
    expect(splitLayout.type).toBe('split')
    if (splitLayout.type !== 'split') return

    // Close the first pane — second pane (sibling) should be promoted
    useTabStore.getState().closePane(tab.id, originalPaneId)

    const resultLayout = useTabStore.getState().tabs[tab.id].layout
    expect(resultLayout.type).toBe('leaf')
    if (resultLayout.type === 'leaf') {
      expect(resultLayout.pane.content).toEqual(splitContent)
    }
    // Tab itself should still exist
    expect(useTabStore.getState().tabs[tab.id]).toBeDefined()
  })
})

describe('resizePanes', () => {
  it('updates sizes on the matching split node', () => {
    const tab = addTab()
    const paneId = (tab.layout as { pane: { id: string } }).pane.id

    useTabStore.getState().splitPane(tab.id, paneId, 'h', newTabContent)

    const splitLayout = useTabStore.getState().tabs[tab.id].layout
    expect(splitLayout.type).toBe('split')
    if (splitLayout.type !== 'split') return

    const splitId = splitLayout.id
    useTabStore.getState().resizePanes(tab.id, splitId, [30, 70])

    const updatedLayout = useTabStore.getState().tabs[tab.id].layout
    expect(updatedLayout.type).toBe('split')
    if (updatedLayout.type === 'split') {
      expect(updatedLayout.sizes).toEqual([30, 70])
    }
  })
})

describe('applyLayout', () => {
  it('applies grid-4 pattern creating 4-pane layout', () => {
    const tab = addTab()
    useTabStore.getState().applyLayout(tab.id, 'grid-4')

    const layout = useTabStore.getState().tabs[tab.id].layout
    expect(layout.type).toBe('split')
    if (layout.type === 'split') {
      expect(layout.direction).toBe('v')
      expect(layout.children).toHaveLength(2)
      layout.children.forEach((child) => {
        expect(child.type).toBe('split')
        if (child.type === 'split') {
          expect(child.direction).toBe('h')
          expect(child.children).toHaveLength(2)
        }
      })
    }
  })

  it('applies single pattern to flatten a split layout back to one pane', () => {
    const tab = addTab()
    const paneId = (tab.layout as { pane: { id: string } }).pane.id

    // Create a split first
    useTabStore.getState().splitPane(tab.id, paneId, 'v', { kind: 'dashboard' })
    expect(useTabStore.getState().tabs[tab.id].layout.type).toBe('split')

    // Apply 'single' to flatten
    useTabStore.getState().applyLayout(tab.id, 'single')

    const layout = useTabStore.getState().tabs[tab.id].layout
    expect(layout.type).toBe('leaf')
    if (layout.type === 'leaf') {
      // Should keep the first (primary) pane
      expect(layout.pane.id).toBe(paneId)
    }
  })
})

describe('detachPane', () => {
  it('detaches a pane from a split into a new tab', () => {
    const tab = addTab()
    const originalPaneId = (tab.layout as { pane: { id: string } }).pane.id
    const detachContent: PaneContent = { kind: 'history' }

    useTabStore.getState().splitPane(tab.id, originalPaneId, 'h', detachContent)

    const splitLayout = useTabStore.getState().tabs[tab.id].layout
    expect(splitLayout.type).toBe('split')
    if (splitLayout.type !== 'split') return

    const secondPaneId = (splitLayout.children[1] as { pane: { id: string } }).pane.id

    const tabsBefore = Object.keys(useTabStore.getState().tabs).length
    const newTabId = useTabStore.getState().detachPane(tab.id, secondPaneId)

    expect(newTabId).not.toBeNull()
    expect(Object.keys(useTabStore.getState().tabs).length).toBe(tabsBefore + 1)
    expect(useTabStore.getState().tabOrder).toContain(newTabId)

    // Original tab should now be a single pane
    const originalLayout = useTabStore.getState().tabs[tab.id].layout
    expect(originalLayout.type).toBe('leaf')

    // New tab should contain the detached pane's content
    const newTab = useTabStore.getState().tabs[newTabId!]
    expect(newTab).toBeDefined()
    const newTabLayout = newTab.layout
    expect(newTabLayout.type).toBe('leaf')
    if (newTabLayout.type === 'leaf') {
      expect(newTabLayout.pane.content).toEqual(detachContent)
    }
  })

  it('returns null when trying to detach from a single-pane tab', () => {
    const tab = addTab()
    const paneId = (tab.layout as { pane: { id: string } }).pane.id

    const result = useTabStore.getState().detachPane(tab.id, paneId)

    expect(result).toBeNull()
    // Tab should be unchanged
    expect(useTabStore.getState().tabs[tab.id]).toBeDefined()
    expect(useTabStore.getState().tabs[tab.id].layout.type).toBe('leaf')
  })
})
