import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'
import type { PaneContent } from '../types/tab'

function makeSessionTab(code: string, mode: 'terminal' | 'stream' = 'terminal') {
  return createTab({ kind: 'session', sessionCode: code, mode })
}

describe('useTabStore', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  })

  it('addTab adds tab to tabs + tabOrder', () => {
    const tab = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab)
    const state = useTabStore.getState()
    expect(state.tabs[tab.id]).toEqual(tab)
    expect(state.tabOrder).toContain(tab.id)
  })

  it('addTab sets activeTabId if none active', () => {
    const tab = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab)
    expect(useTabStore.getState().activeTabId).toBe(tab.id)
  })

  it('addTab does not change activeTabId when adding second tab', () => {
    const tab1 = makeSessionTab('dev001')
    const tab2 = makeSessionTab('cld001', 'stream')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    expect(useTabStore.getState().activeTabId).toBe(tab1.id)
  })

  it('closeTab removes from tabs + tabOrder', () => {
    const tab = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab)
    useTabStore.getState().closeTab(tab.id)
    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
    expect(useTabStore.getState().tabOrder).not.toContain(tab.id)
  })

  it('closeTab on locked tab is no-op', () => {
    const tab = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab)
    useTabStore.getState().toggleLock(tab.id)
    useTabStore.getState().closeTab(tab.id)
    expect(useTabStore.getState().tabs[tab.id]).toBeDefined()
    expect(useTabStore.getState().tabOrder).toContain(tab.id)
  })

  it('closeTab activates adjacent tab when removing active', () => {
    const tab1 = makeSessionTab('dev001')
    const tab2 = makeSessionTab('cld001')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().setActiveTab(tab1.id)
    useTabStore.getState().closeTab(tab1.id)
    expect(useTabStore.getState().activeTabId).toBe(tab2.id)
  })

  it('closeTab sets null when removing last tab', () => {
    const tab = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab)
    useTabStore.getState().closeTab(tab.id)
    expect(useTabStore.getState().activeTabId).toBeNull()
  })

  it('setActiveTab updates activeTabId', () => {
    const tab1 = makeSessionTab('dev001')
    const tab2 = makeSessionTab('cld001')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().setActiveTab(tab2.id)
    expect(useTabStore.getState().activeTabId).toBe(tab2.id)
  })

  it('setActiveTab ignores nonexistent id', () => {
    const tab = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab)
    useTabStore.getState().setActiveTab('nonexistent')
    expect(useTabStore.getState().activeTabId).toBe(tab.id)
  })

  it('openSingletonTab returns existing tab id if content matches', () => {
    const content: PaneContent = { kind: 'session', sessionCode: 'dev001', mode: 'terminal' }
    const tab = createTab(content)
    useTabStore.getState().addTab(tab)
    const returnedId = useTabStore.getState().openSingletonTab(content)
    expect(returnedId).toBe(tab.id)
  })

  it('openSingletonTab creates new tab if no match', () => {
    const content: PaneContent = { kind: 'dashboard' }
    const returnedId = useTabStore.getState().openSingletonTab(content)
    expect(useTabStore.getState().tabs[returnedId]).toBeDefined()
    expect(useTabStore.getState().tabOrder).toContain(returnedId)
  })

  it('openSingletonTab activates existing tab', () => {
    const content: PaneContent = { kind: 'dashboard' }
    const tab = createTab(content)
    useTabStore.getState().addTab(tab)
    // add another tab and make it active
    const tab2 = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().setActiveTab(tab2.id)
    expect(useTabStore.getState().activeTabId).toBe(tab2.id)
    // openSingletonTab should activate the existing dashboard tab
    useTabStore.getState().openSingletonTab(content)
    expect(useTabStore.getState().activeTabId).toBe(tab.id)
  })

  it('setViewMode updates pane mode', () => {
    const tab = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab)
    const paneId = (tab.layout as any).pane.id
    useTabStore.getState().setViewMode(tab.id, paneId, 'stream')
    const updated = useTabStore.getState().tabs[tab.id]
    const content = (updated.layout as any).pane.content
    expect(content.mode).toBe('stream')
  })

  it('setViewMode is no-op for nonexistent tab', () => {
    useTabStore.getState().setViewMode('nonexistent', 'pane1', 'stream')
    expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(0)
  })

  it('reorderTabs updates tabOrder', () => {
    const tab1 = makeSessionTab('a')
    const tab2 = makeSessionTab('b')
    const tab3 = makeSessionTab('c')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().addTab(tab3)
    useTabStore.getState().reorderTabs([tab3.id, tab1.id, tab2.id])
    expect(useTabStore.getState().tabOrder).toEqual([tab3.id, tab1.id, tab2.id])
  })

  it('closeTab is no-op for nonexistent id', () => {
    const tab = makeSessionTab('dev001')
    useTabStore.getState().addTab(tab)
    useTabStore.getState().closeTab('nonexistent')
    expect(useTabStore.getState().tabOrder).toHaveLength(1)
  })
})
