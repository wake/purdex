import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore, migrateTabStore } from './useTabStore'
import { createTab } from '../types/tab'
import type { PaneContent } from '../types/tab'
import { getPrimaryPane } from '../lib/pane-tree'

function makeSessionTab(code: string, mode: 'terminal' | 'stream' = 'terminal') {
  return createTab({ kind: 'tmux-session', hostId: 'test-host', sessionCode: code, mode, cachedName: '', tmuxInstance: '' })
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

  it('addTab with afterTabId inserts after specified tab', () => {
    const tab1 = makeSessionTab('dev001')
    const tab2 = makeSessionTab('dev002')
    const tab3 = makeSessionTab('dev003')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().addTab(tab3, tab1.id)
    expect(useTabStore.getState().tabOrder).toEqual([tab1.id, tab3.id, tab2.id])
  })

  it('addTab with afterTabId appends when afterTabId not found', () => {
    const tab1 = makeSessionTab('dev001')
    const tab2 = makeSessionTab('dev002')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2, 'nonexistent')
    expect(useTabStore.getState().tabOrder).toEqual([tab1.id, tab2.id])
  })

  it('addTab with afterTabId pointing to pinned tab inserts after pinned group', () => {
    const tab1 = makeSessionTab('dev001')
    const tab2 = makeSessionTab('dev002')
    const tab3 = makeSessionTab('dev003')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().togglePin(tab1.id)
    useTabStore.getState().togglePin(tab2.id)
    // tab1 and tab2 are pinned; insert after tab1 should skip past all pinned
    useTabStore.getState().addTab(tab3, tab1.id)
    expect(useTabStore.getState().tabOrder).toEqual([tab1.id, tab2.id, tab3.id])
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

  it('closeTab sets activeTabId to null when removing active tab', () => {
    const tab1 = makeSessionTab('dev001')
    const tab2 = makeSessionTab('cld001')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().setActiveTab(tab1.id)
    useTabStore.getState().closeTab(tab1.id)
    expect(useTabStore.getState().activeTabId).toBeNull()
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

  it('openSingletonTab returns existing tab id if content matches (singleton kinds)', () => {
    const content: PaneContent = { kind: 'dashboard' }
    const tab = createTab(content)
    useTabStore.getState().addTab(tab)
    const returnedId = useTabStore.getState().openSingletonTab(content)
    expect(returnedId).toBe(tab.id)
  })

  it('openSingletonTab always creates new tab for session (non-singleton)', () => {
    const content: PaneContent = { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' }
    const tab = createTab(content)
    useTabStore.getState().addTab(tab)
    const returnedId = useTabStore.getState().openSingletonTab(content)
    expect(returnedId).not.toBe(tab.id) // sessions are never singletons
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
    const paneId = tab.layout.type === 'leaf' ? tab.layout.pane.id : ''
    useTabStore.getState().setViewMode(tab.id, paneId, 'stream')
    const updated = useTabStore.getState().tabs[tab.id]
    const content = updated.layout.type === 'leaf' ? updated.layout.pane.content : undefined
    expect(content?.kind === 'tmux-session' && content.mode).toBe('stream')
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

  describe('setPaneContent', () => {
    it('updates pane content by tabId and paneId', () => {
      const tab = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab)
      const paneId = tab.layout.type === 'leaf' ? tab.layout.pane.id : ''
      const newContent: PaneContent = { kind: 'dashboard' }
      useTabStore.getState().setPaneContent(tab.id, paneId, newContent)
      const updated = useTabStore.getState().tabs[tab.id]
      expect(updated.layout.type).toBe('leaf')
      if (updated.layout.type === 'leaf') {
        expect(updated.layout.pane.content).toEqual({ kind: 'dashboard' })
      }
    })

    it('is no-op for nonexistent tab', () => {
      useTabStore.getState().setPaneContent('nonexistent', 'pane1', { kind: 'dashboard' })
      expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(0)
    })

    it('is no-op for nonexistent pane (layout unchanged)', () => {
      const tab = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab)
      const before = useTabStore.getState().tabs[tab.id].layout
      useTabStore.getState().setPaneContent(tab.id, 'nonexistent-pane', { kind: 'dashboard' })
      const after = useTabStore.getState().tabs[tab.id].layout
      // Layout should be structurally the same (content unchanged)
      if (before.type === 'leaf' && after.type === 'leaf') {
        expect(after.pane.content).toEqual(before.pane.content)
      }
    })
  })

  describe('updateSessionCache', () => {
    it('updates cachedName for matching session tab', () => {
      const tab = makeSessionTab('dev001', 'terminal')
      useTabStore.getState().addTab(tab)
      const tabId = useTabStore.getState().tabOrder[0]

      useTabStore.getState().updateSessionCache('test-host', 'dev001', 'renamed-session')

      const content = getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content
      expect(content.kind).toBe('tmux-session')
      if (content.kind === 'tmux-session') {
        expect(content.cachedName).toBe('renamed-session')
      }
    })

    it('does not update tab with different sessionCode', () => {
      const tab = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab)
      const tabId = useTabStore.getState().tabOrder[0]

      useTabStore.getState().updateSessionCache('test-host', 'dev999', 'renamed')

      const content = getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content
      expect(content.kind).toBe('tmux-session')
      if (content.kind === 'tmux-session') {
        expect(content.cachedName).toBe('')
      }
    })

    it('does not update tab with different hostId', () => {
      const tab = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab)
      const tabId = useTabStore.getState().tabOrder[0]

      useTabStore.getState().updateSessionCache('other-host', 'dev001', 'renamed')

      const content = getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content
      expect(content.kind).toBe('tmux-session')
      if (content.kind === 'tmux-session') {
        expect(content.cachedName).toBe('')
      }
    })

    it('is no-op when cachedName is already the same', () => {
      const tab = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab)
      const tabId = useTabStore.getState().tabOrder[0]
      const before = useTabStore.getState().tabs[tabId]

      useTabStore.getState().updateSessionCache('test-host', 'dev001', '')

      const after = useTabStore.getState().tabs[tabId]
      expect(after).toBe(before) // same reference — no update
    })

    it('updates multiple matching tabs', () => {
      const tab1 = makeSessionTab('dev001')
      const tab2 = makeSessionTab('dev001', 'stream')
      useTabStore.getState().addTab(tab1)
      useTabStore.getState().addTab(tab2)

      useTabStore.getState().updateSessionCache('test-host', 'dev001', 'new-name')

      for (const tabId of useTabStore.getState().tabOrder) {
        const content = getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content
        expect(content.kind).toBe('tmux-session')
        if (content.kind === 'tmux-session') {
          expect(content.cachedName).toBe('new-name')
        }
      }
    })
  })

  describe('markTerminated', () => {
    it('marks matching pane as terminated', () => {
      const tab = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab)
      useTabStore.getState().markTerminated('test-host', 'dev001', 'session-closed')
      const content = getPrimaryPane(useTabStore.getState().tabs[tab.id].layout).content
      expect(content.kind).toBe('tmux-session')
      if (content.kind === 'tmux-session') {
        expect(content.terminated).toBe('session-closed')
      }
    })

    it('does not mark pane with different sessionCode', () => {
      const tab = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab)
      useTabStore.getState().markTerminated('test-host', 'dev999', 'session-closed')
      const content = getPrimaryPane(useTabStore.getState().tabs[tab.id].layout).content
      if (content.kind === 'tmux-session') {
        expect(content.terminated).toBeUndefined()
      }
    })

    it('does not mark pane with different hostId', () => {
      const tab = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab)
      useTabStore.getState().markTerminated('other-host', 'dev001', 'session-closed')
      const content = getPrimaryPane(useTabStore.getState().tabs[tab.id].layout).content
      if (content.kind === 'tmux-session') {
        expect(content.terminated).toBeUndefined()
      }
    })

    it('does not double-mark already-terminated panes', () => {
      const tab = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab)
      useTabStore.getState().markTerminated('test-host', 'dev001', 'session-closed')
      const before = useTabStore.getState().tabs[tab.id]
      // Mark again with different reason — should be no-op (already terminated)
      useTabStore.getState().markTerminated('test-host', 'dev001', 'tmux-restarted')
      const after = useTabStore.getState().tabs[tab.id]
      expect(after).toBe(before) // same reference — no update
      const content = getPrimaryPane(after.layout).content
      if (content.kind === 'tmux-session') {
        expect(content.terminated).toBe('session-closed') // original reason preserved
      }
    })

    it('marks multiple matching tabs', () => {
      const tab1 = makeSessionTab('dev001')
      const tab2 = makeSessionTab('dev001', 'stream')
      useTabStore.getState().addTab(tab1)
      useTabStore.getState().addTab(tab2)
      useTabStore.getState().markTerminated('test-host', 'dev001', 'session-closed')
      for (const tabId of useTabStore.getState().tabOrder) {
        const content = getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content
        if (content.kind === 'tmux-session') {
          expect(content.terminated).toBe('session-closed')
        }
      }
    })

    it('is no-op when no panes match (returns same state)', () => {
      const tab = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab)
      const before = useTabStore.getState().tabs
      useTabStore.getState().markTerminated('test-host', 'no-match', 'session-closed')
      const after = useTabStore.getState().tabs
      expect(after).toBe(before)
    })
  })

  describe('markHostTerminated', () => {
    it('marks all panes for a host', () => {
      const tab1 = makeSessionTab('dev001')
      const tab2 = makeSessionTab('dev002', 'stream')
      useTabStore.getState().addTab(tab1)
      useTabStore.getState().addTab(tab2)
      useTabStore.getState().markHostTerminated('test-host', 'tmux-restarted')
      for (const tabId of useTabStore.getState().tabOrder) {
        const content = getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content
        if (content.kind === 'tmux-session') {
          expect(content.terminated).toBe('tmux-restarted')
        }
      }
    })

    it('does not mark panes for different host', () => {
      const tab = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab)
      useTabStore.getState().markHostTerminated('other-host', 'host-removed')
      const content = getPrimaryPane(useTabStore.getState().tabs[tab.id].layout).content
      if (content.kind === 'tmux-session') {
        expect(content.terminated).toBeUndefined()
      }
    })

    it('does not double-mark already-terminated panes', () => {
      const tab = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab)
      useTabStore.getState().markHostTerminated('test-host', 'session-closed')
      const before = useTabStore.getState().tabs[tab.id]
      useTabStore.getState().markHostTerminated('test-host', 'tmux-restarted')
      const after = useTabStore.getState().tabs[tab.id]
      expect(after).toBe(before)
      const content = getPrimaryPane(after.layout).content
      if (content.kind === 'tmux-session') {
        expect(content.terminated).toBe('session-closed')
      }
    })
  })

  describe('visitHistory', () => {
    beforeEach(() => {
      useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    })

    it('records previous tab when switching', () => {
      const tab1 = makeSessionTab('dev001')
      const tab2 = makeSessionTab('dev002')
      useTabStore.getState().addTab(tab1)
      useTabStore.getState().addTab(tab2)
      // tab1 is active (first tab added), switch to tab2
      useTabStore.getState().setActiveTab(tab2.id)
      expect(useTabStore.getState().visitHistory).toContain(tab1.id)
    })

    it('does not record when switching to same tab', () => {
      const tab1 = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab1)
      useTabStore.getState().setActiveTab(tab1.id)
      // switching to the same tab should not push to history
      useTabStore.getState().setActiveTab(tab1.id)
      expect(useTabStore.getState().visitHistory).toHaveLength(0)
    })

    it('does not record null activeTabId', () => {
      const tab1 = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab1)
      // set null active, then switch to tab1 — null should not be recorded
      useTabStore.setState({ activeTabId: null })
      useTabStore.getState().setActiveTab(tab1.id)
      expect(useTabStore.getState().visitHistory).toHaveLength(0)
    })

    it('closeTab removes closed tab id from visitHistory', () => {
      const tab1 = makeSessionTab('dev001')
      const tab2 = makeSessionTab('dev002')
      const tab3 = makeSessionTab('dev003')
      useTabStore.getState().addTab(tab1)
      useTabStore.getState().addTab(tab2)
      useTabStore.getState().addTab(tab3)
      // Build up history with tab1 in it
      useTabStore.getState().setActiveTab(tab2.id) // history: [tab1]
      useTabStore.getState().setActiveTab(tab3.id) // history: [tab1, tab2]
      // Close tab1 (not active) — should remove tab1 from history
      useTabStore.getState().closeTab(tab1.id)
      expect(useTabStore.getState().visitHistory).not.toContain(tab1.id)
    })
  })

  describe('persist migration', () => {
    it('migrates kind "session" to "tmux-session" in version 2', () => {
      const v1State = {
        tabs: {
          tab1: {
            id: 'tab1', pinned: false, locked: false, createdAt: 1000,
            layout: {
              type: 'leaf' as const,
              pane: {
                id: 'pane1',
                content: { kind: 'session', hostId: 'h1', sessionCode: 'abc123', mode: 'terminal', cachedName: 'test', tmuxInstance: '123:456' },
              },
            },
          },
        },
        tabOrder: ['tab1'],
        activeTabId: 'tab1',
      }
      const migrated = migrateTabStore(v1State, 1)
      const pane = migrated.tabs.tab1.layout.pane
      expect(pane.content.kind).toBe('tmux-session')
    })

    it('migrates kind "session" inside split layouts', () => {
      const v1State = {
        tabs: {
          tab1: {
            id: 'tab1', pinned: false, locked: false, createdAt: 1000,
            layout: {
              type: 'split' as const, id: 'split1', direction: 'h' as const,
              children: [
                { type: 'leaf' as const, pane: { id: 'p1', content: { kind: 'session', hostId: 'h1', sessionCode: 'a', mode: 'terminal', cachedName: 'A', tmuxInstance: '1:2' } } },
                { type: 'leaf' as const, pane: { id: 'p2', content: { kind: 'dashboard' } } },
              ],
              sizes: [50, 50],
            },
          },
        },
        tabOrder: ['tab1'],
        activeTabId: 'tab1',
      }
      const migrated = migrateTabStore(v1State, 1)
      const children = migrated.tabs.tab1.layout.children
      expect(children[0].pane.content.kind).toBe('tmux-session')
      expect(children[1].pane.content.kind).toBe('dashboard')
    })

    it('preserves non-session tabs during migration', () => {
      const v1State = {
        tabs: {
          tab1: {
            id: 'tab1', pinned: false, locked: false, createdAt: 1000,
            layout: { type: 'leaf' as const, pane: { id: 'pane1', content: { kind: 'dashboard' } } },
          },
        },
        tabOrder: ['tab1'],
        activeTabId: 'tab1',
      }
      const migrated = migrateTabStore(v1State, 1)
      const pane = migrated.tabs.tab1.layout.pane
      expect(pane.content.kind).toBe('dashboard')
    })
  })
})
