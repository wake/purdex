import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { createTab } from '../types/tab'
import { useShortcuts } from './useShortcuts'

function mockElectronAPI() {
  let shortcutCallback: ((payload: { action: string }) => void) | null = null
  const cleanup = vi.fn()
  ;(window as unknown as Record<string, unknown>).electronAPI = {
    onShortcut: (cb: (payload: { action: string }) => void) => {
      shortcutCallback = cb
      return cleanup
    },
    signalReady: () => {},
  }
  return {
    fire: (action: string) => shortcutCallback?.({ action }),
    cleanup,
  }
}

function seedTabs(count: number) {
  const store = useTabStore.getState()
  const tabs = Array.from({ length: count }, () =>
    createTab({ kind: 'new-tab' }),
  )
  tabs.forEach((t) => store.addTab(t))
  store.setActiveTab(tabs[0].id)
  return tabs
}

describe('useShortcuts', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
  })

  afterEach(() => {
    delete (window as unknown as Record<string, unknown>).electronAPI
  })

  it('does nothing when electronAPI is not available', () => {
    const { unmount } = renderHook(() => useShortcuts())
    unmount()
  })

  it('cleans up listener on unmount', () => {
    const { cleanup } = mockElectronAPI()
    const { unmount } = renderHook(() => useShortcuts())
    unmount()
    expect(cleanup).toHaveBeenCalled()
  })

  describe('switch-tab-{n}', () => {
    it('switches to tab by index', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(5)
      renderHook(() => useShortcuts())

      fire('switch-tab-3')
      expect(useTabStore.getState().activeTabId).toBe(tabs[2].id)
    })

    it('ignores out-of-range index', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(2)
      useTabStore.getState().setActiveTab(tabs[0].id)
      renderHook(() => useShortcuts())

      fire('switch-tab-5')
      expect(useTabStore.getState().activeTabId).toBe(tabs[0].id)
    })
  })

  describe('switch-tab-last', () => {
    it('switches to the last tab', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(4)
      renderHook(() => useShortcuts())

      fire('switch-tab-last')
      expect(useTabStore.getState().activeTabId).toBe(tabs[3].id)
    })
  })

  describe('prev-tab / next-tab', () => {
    it('cycles to previous tab', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(3)
      useTabStore.getState().setActiveTab(tabs[1].id)
      renderHook(() => useShortcuts())

      fire('prev-tab')
      expect(useTabStore.getState().activeTabId).toBe(tabs[0].id)
    })

    it('wraps around from first to last', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(3)
      useTabStore.getState().setActiveTab(tabs[0].id)
      renderHook(() => useShortcuts())

      fire('prev-tab')
      expect(useTabStore.getState().activeTabId).toBe(tabs[2].id)
    })

    it('cycles to next tab', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(3)
      useTabStore.getState().setActiveTab(tabs[0].id)
      renderHook(() => useShortcuts())

      fire('next-tab')
      expect(useTabStore.getState().activeTabId).toBe(tabs[1].id)
    })

    it('wraps around from last to first', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(3)
      useTabStore.getState().setActiveTab(tabs[2].id)
      renderHook(() => useShortcuts())

      fire('next-tab')
      expect(useTabStore.getState().activeTabId).toBe(tabs[0].id)
    })
  })

  describe('open-settings', () => {
    it('opens a settings singleton tab', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      renderHook(() => useShortcuts())

      fire('open-settings')
      const state = useTabStore.getState()
      const settingsTab = Object.values(state.tabs).find((t) => {
        const pane = t.layout
        return pane.type === 'leaf' && pane.pane.content.kind === 'settings'
      })
      expect(settingsTab).toBeDefined()
      expect(state.activeTabId).toBe(settingsTab!.id)
    })
  })

  describe('open-history', () => {
    it('opens a history singleton tab', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1)
      renderHook(() => useShortcuts())

      fire('open-history')
      const state = useTabStore.getState()
      const historyTab = Object.values(state.tabs).find((t) => {
        const pane = t.layout
        return pane.type === 'leaf' && pane.pane.content.kind === 'history'
      })
      expect(historyTab).toBeDefined()
      expect(state.activeTabId).toBe(historyTab!.id)
    })
  })

  describe('reopen-closed-tab', () => {
    it('reopens the last closed tab', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(2)
      const closedTab = tabs[1]
      useHistoryStore.getState().recordClose(closedTab)
      useTabStore.getState().closeTab(closedTab.id)
      renderHook(() => useShortcuts())

      fire('reopen-closed-tab')
      expect(useTabStore.getState().tabs[closedTab.id]).toBeDefined()
      expect(useTabStore.getState().activeTabId).toBe(closedTab.id)
    })
  })
})
