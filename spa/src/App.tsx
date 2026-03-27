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
import { useRelayWsManager } from './hooks/useRelayWsManager'
import { useSessionEventWs } from './hooks/useSessionEventWs'
import { useRouteSync } from './hooks/useRouteSync'
import { useShortcuts } from './hooks/useShortcuts'
import { useTabWorkspaceActions } from './hooks/useTabWorkspaceActions'
import { isStandaloneTab } from './types/tab'
import { TabContextMenu } from './components/TabContextMenu'
import { ThemeInjector } from './components/ThemeInjector'
import { getPlatformCapabilities } from './lib/platform'
import type { Tab } from './types/tab'

export default function App() {
  const isElectron = getPlatformCapabilities().isElectron

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
  useShortcuts()

  // --- Electron: signal SPA ready (replaces 500ms setTimeout) ---
  useEffect(() => {
    window.electronAPI?.signalReady()
  }, [])

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
      <div className="h-screen flex flex-col bg-surface-primary text-text-primary">
        {/* Electron: title bar row — traffic lights + tabs + drag fill */}
        {isElectron && (
          <div
            className="shrink-0 flex items-center bg-surface-secondary border-b border-border-subtle"
            style={{ height: 44, WebkitAppRegion: 'drag' } as React.CSSProperties}
          >
            {/* Traffic light safe zone (78px ≈ 3 buttons + padding) */}
            <div className="shrink-0" style={{ width: 78 }} />
            {/* Tabs — no-drag so clicks work */}
            <div style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}>
              <TabBar
                tabs={displayTabs}
                activeTabId={activeTabId}
                onSelectTab={handleSelectTab}
                onCloseTab={handleCloseTab}
                onAddTab={handleAddTab}
                onReorderTabs={handleReorderTabs}
                onMiddleClick={handleMiddleClick}
                onContextMenu={handleContextMenu}
                embedded
              />
            </div>
            {/* Remaining space — drag region inherited from parent */}
          </div>
        )}
        <div className="flex-1 flex min-h-0">
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
            handleSelectTab(tabId)
          }}
        />
        <div className="flex-1 flex flex-col min-w-0">
          {/* SPA: TabBar in normal position */}
          {!isElectron && (
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
          )}
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
      </div>
    </Router>
  )
}
