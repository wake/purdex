// spa/src/App.tsx — v2 重構：wouter Router + Tab/Pane model
import { useEffect } from 'react'
import { Router } from 'wouter'
import { ActivityBar } from './components/ActivityBar'
import { TabBar } from './components/TabBar'
import { TabContent } from './components/TabContent'
import { StatusBar } from './components/StatusBar'
import { useSessionStore } from './stores/useSessionStore'
import { useConfigStore } from './stores/useConfigStore'
import { useTabStore } from './stores/useTabStore'
import { useWorkspaceStore } from './stores/useWorkspaceStore'
import { useHostStore } from './stores/useHostStore'
import { useHistoryStore } from './stores/useHistoryStore'
import { useRelayWsManager } from './hooks/useRelayWsManager'
import { useSessionEventWs } from './hooks/useSessionEventWs'
import { useRouteSync } from './hooks/useRouteSync'
import { useTabWorkspaceActions } from './hooks/useTabWorkspaceActions'
import { isStandaloneTab } from './types/tab'
import { TabContextMenu } from './components/TabContextMenu'
import { ThemeInjector } from './components/ThemeInjector'
import type { Tab } from './types/tab'

export default function App() {
  // Host store (replaces hardcoded daemonBase)
  const getDaemonBase = useHostStore((s) => s.getDaemonBase)
  const getWsBase = useHostStore((s) => s.getWsBase)
  const daemonBase = getDaemonBase('local')
  const wsBase = getWsBase('local')

  // Existing stores
  const fetchSessions = useSessionStore((s) => s.fetch)
  const fetchConfig = useConfigStore((s) => s.fetch)

  // Tab store
  const tabs = useTabStore((s) => s.tabs)
  const tabOrder = useTabStore((s) => s.tabOrder)
  const activeTabId = useTabStore((s) => s.activeTabId)

  // Workspace store
  const workspaces = useWorkspaceStore((s) => s.workspaces)
  const activeWorkspaceId = useWorkspaceStore((s) => s.activeWorkspaceId)

  // --- Extracted hooks ---
  useRelayWsManager(wsBase)
  useSessionEventWs(wsBase, daemonBase)
  useRouteSync()

  // --- Electron IPC: receive tab from tear-off/merge ---
  useEffect(() => {
    if (!window.electronAPI) return
    return window.electronAPI.onTabReceived((tabJson: string) => {
      try {
        const tab = JSON.parse(tabJson)
        if (tab && tab.id && tab.layout) {
          useTabStore.getState().addTab(tab)
          useTabStore.getState().setActiveTab(tab.id)
          // Restore workspace membership if receiving window has an active workspace
          const wsId = useWorkspaceStore.getState().activeWorkspaceId
          if (wsId) {
            useWorkspaceStore.getState().addTabToWorkspace(wsId, tab.id)
            useWorkspaceStore.getState().setWorkspaceActiveTab(wsId, tab.id)
          }
        }
      } catch { /* ignore malformed tab JSON */ }
    })
  }, [])

  // --- Keybinding: ⌘+Shift+T / Ctrl+Shift+T — reopen last closed tab ---
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key === 'T') {
        e.preventDefault()
        const tab = useHistoryStore.getState().reopenLast()
        if (tab) {
          useTabStore.getState().addTab(tab)
          useTabStore.getState().setActiveTab(tab.id)
        }
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [])

  // --- Derived state ---
  const activeTab = activeTabId ? tabs[activeTabId] : undefined

  // --- Bootstrap: fetch sessions + config ---
  useEffect(() => {
    fetchSessions(daemonBase)
    fetchConfig(daemonBase)
  }, [fetchSessions, fetchConfig, daemonBase])

  // --- Derive visible tabs for display ---
  const activeWs = workspaces.find((w) => w.id === activeWorkspaceId)
  const visibleTabs: Tab[] = activeWs
    ? activeWs.tabs.map((id) => tabs[id]).filter(Boolean)
    : []

  const standaloneTabs = tabOrder
    .filter((id) => isStandaloneTab(id, workspaces))
    .map((id) => tabs[id])
    .filter(Boolean)

  const activeStandaloneTabId = activeTabId && isStandaloneTab(activeTabId, workspaces) ? activeTabId : null

  const displayTabs = activeStandaloneTabId
    ? [tabs[activeStandaloneTabId]].filter(Boolean)
    : visibleTabs

  // --- Tab/Workspace action handlers ---
  const {
    contextMenu,
    setContextMenu,
    contextMenuHasRightUnlocked,
    handleSelectWorkspace,
    handleSelectTab,
    handleCloseTab,
    handleAddTab,
    handleReorderTabs,
    handleContextMenu,
    handleMiddleClick,
    handleContextAction,
  } = useTabWorkspaceActions(displayTabs)

  return (
    <Router>
      <ThemeInjector />
      <div className="h-screen flex bg-surface-primary text-text-primary">
        <ActivityBar
          workspaces={workspaces}
          standaloneTabs={standaloneTabs}
          activeWorkspaceId={activeStandaloneTabId ? null : activeWorkspaceId}
          activeStandaloneTabId={activeStandaloneTabId}
          onSelectWorkspace={handleSelectWorkspace}
          onSelectStandaloneTab={handleSelectTab}
          onAddWorkspace={() => {}}
          onOpenSettings={() => {
            const tabId = useTabStore.getState().openSingletonTab({ kind: 'settings', scope: 'global' })
            if (activeWorkspaceId) {
              useWorkspaceStore.getState().addTabToWorkspace(activeWorkspaceId, tabId)
              useWorkspaceStore.getState().setWorkspaceActiveTab(activeWorkspaceId, tabId)
            }
            // Ensure we switch to workspace view (not standalone tab view)
            handleSelectTab(tabId)
          }}
        />
        <div className="flex-1 flex flex-col min-w-0">
          <TabBar
            tabs={displayTabs}
            activeTabId={activeTabId}
            onSelectTab={handleSelectTab}
            onCloseTab={handleCloseTab}
            onAddTab={handleAddTab}
            onReorderTabs={handleReorderTabs}
            onMiddleClick={handleMiddleClick}
            onContextMenu={handleContextMenu}
          />
          <div className="flex-1 flex overflow-hidden">
            <TabContent
              activeTab={activeTab ?? null}
              allTabs={tabOrder.map((id) => tabs[id]).filter(Boolean)}
            />
          </div>
          <StatusBar
            activeTab={activeTab ?? null}
            onViewModeChange={(tabId, paneId, mode) => {
              useTabStore.getState().setViewMode(tabId, paneId, mode)
            }}
          />
        </div>
        {contextMenu && (
          <TabContextMenu
            tab={contextMenu.tab}
            position={contextMenu.position}
            onClose={() => setContextMenu(null)}
            onAction={handleContextAction}
            hasOtherUnlocked={displayTabs.some((t) => t.id !== contextMenu.tab.id && !t.locked)}
            hasRightUnlocked={contextMenuHasRightUnlocked}
          />
        )}
      </div>
    </Router>
  )
}
