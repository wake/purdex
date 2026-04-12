import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'
import { getPrimaryPane } from '../lib/pane-tree'

function makeSessionTab(code: string, mode: 'terminal' | 'stream' = 'terminal') {
  return createTab({ kind: 'tmux-session', hostId: 'test-host', sessionCode: code, mode, cachedName: '', tmuxInstance: '' })
}

describe('useTabStore — markTerminated / markHostTerminated', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
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
})
