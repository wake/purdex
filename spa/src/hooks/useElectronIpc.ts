import { useEffect } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { openBrowserTab } from '../lib/open-browser-tab'
import type { Tab } from '../types/tab'

/**
 * Registers all Electron IPC listeners as React effects.
 * No-op when window.electronAPI is absent (SPA-only mode).
 */
export function useElectronIpc() {
  // Signal SPA ready
  useEffect(() => {
    window.electronAPI?.signalReady()
  }, [])

  // Receive single tab from tear-off/merge
  useEffect(() => {
    if (!window.electronAPI) return
    return window.electronAPI.onTabReceived((tabJson: string, replace: boolean) => {
      try {
        const tab = JSON.parse(tabJson)
        if (tab && tab.id && tab.layout) {
          if (replace) {
            useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
          }
          useTabStore.getState().addTab(tab)
          useTabStore.getState().setActiveTab(tab.id)
          useWorkspaceStore.getState().insertTab(tab.id)
        }
      } catch { /* ignore malformed tab JSON */ }
    })
  }, [])

  // Receive workspace from tear-off/merge
  // Fix #231: narrow catch to JSON.parse only
  useEffect(() => {
    if (!window.electronAPI?.onWorkspaceReceived) return
    return window.electronAPI.onWorkspaceReceived((payload: string, replace: boolean) => {
      let parsed: { workspace: { id: string; tabs: string[]; activeTabId?: string }; tabData: Tab[] }
      try {
        parsed = JSON.parse(payload)
      } catch {
        return // malformed JSON — discard
      }

      const { workspace, tabData } = parsed
      if (!workspace?.id || !Array.isArray(tabData)) return

      const tabMap = new Map(tabData.map((t: Tab) => [t.id, t]))
      workspace.tabs = workspace.tabs.filter((id: string) => tabMap.has(id))

      if (replace) {
        useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
        useWorkspaceStore.getState().reset()
      }

      for (const tab of tabData) {
        if (tab?.id && tab?.layout) useTabStore.getState().addTab(tab)
      }

      useWorkspaceStore.getState().importWorkspace(workspace)
      if (replace) {
        useWorkspaceStore.getState().setActiveWorkspace(workspace.id)
      }
      const activeTab = (workspace.activeTabId && tabMap.has(workspace.activeTabId))
        ? workspace.activeTabId
        : workspace.tabs[0]
      if (activeTab) useTabStore.getState().setActiveTab(activeTab)
    })
  }, [])

  // Open browser tab from mini browser / WebContentsView link click
  useEffect(() => {
    if (!window.electronAPI?.onBrowserViewOpenInTab) return
    return window.electronAPI.onBrowserViewOpenInTab((url: string) => {
      openBrowserTab(url)
    })
  }, [])
}
