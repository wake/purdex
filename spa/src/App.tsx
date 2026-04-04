// spa/src/App.tsx — v2 重構：wouter Router + Tab/Pane model
import { useEffect, useRef } from 'react'
import { Router } from 'wouter'
import { ActivityBar } from './components/ActivityBar'
import { TabBar } from './components/TabBar'
import { TabContent } from './components/TabContent'
import { StatusBar } from './components/StatusBar'
import { useConfigStore } from './stores/useConfigStore'
import { useTabStore } from './stores/useTabStore'
import { useWorkspaceStore } from './stores/useWorkspaceStore'
import { useHostStore } from './stores/useHostStore'
import { useRelayWsManager } from './hooks/useRelayWsManager'
import { useMultiHostEventWs } from './hooks/useMultiHostEventWs'
import { useRouteSync } from './hooks/useRouteSync'
import { useShortcuts } from './hooks/useShortcuts'
import { useNotificationDispatcher } from './hooks/useNotificationDispatcher'
import { useAgentStore } from './stores/useAgentStore'
import { useUndoToast } from './stores/useUndoToast'
import { useTabWorkspaceActions } from './hooks/useTabWorkspaceActions'
import { isStandaloneTab } from './types/tab'
import { TabContextMenu } from './components/TabContextMenu'
import { ThemeInjector } from './components/ThemeInjector'
import { ErrorBoundary } from './components/ErrorBoundary'
import { getPlatformCapabilities } from './lib/platform'
import { useI18nStore } from './stores/useI18nStore'
import type { Tab } from './types/tab'

function GlobalUndoToast() {
  const toast = useUndoToast((s) => s.toast)
  const dismiss = useUndoToast((s) => s.dismiss)
  const t = useI18nStore((s) => s.t)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    if (!toast) return
    timerRef.current = setTimeout(() => dismiss(), 5000)
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [toast, dismiss])

  if (!toast) return null

  return (
    <div className="fixed bottom-4 left-1/2 -translate-x-1/2 bg-zinc-800 border border-zinc-700 rounded-lg px-4 py-3 flex items-center gap-3 shadow-lg z-50">
      <span className="text-sm text-zinc-300">
        {toast.message}
      </span>
      <button
        className="text-sm text-blue-400 hover:text-blue-300 font-medium cursor-pointer"
        onClick={() => {
          toast.restore()
          dismiss()
        }}
      >
        {t('hosts.undo')}
      </button>
    </div>
  )
}

export default function App() {
  const isElectron = getPlatformCapabilities().isElectron

  // Host store (replaces hardcoded daemonBase)
  const getDaemonBase = useHostStore((s) => s.getDaemonBase)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const firstHostId = hostOrder[0] ?? ''
  const daemonBase = getDaemonBase(firstHostId)

  // Existing stores
  const fetchConfig = useConfigStore((s) => s.fetch)

  // Tab store
  const tabs = useTabStore((s) => s.tabs)
  const tabOrder = useTabStore((s) => s.tabOrder)
  const activeTabId = useTabStore((s) => s.activeTabId)

  // Workspace store
  const workspaces = useWorkspaceStore((s) => s.workspaces)
  const activeWorkspaceId = useWorkspaceStore((s) => s.activeWorkspaceId)

  // --- Extracted hooks ---
  useRelayWsManager()
  useMultiHostEventWs()
  useRouteSync()
  useShortcuts()
  useNotificationDispatcher()

  // --- Fetch hook installation status on mount ---
  // Sets global hooksInstalled flag (used by SortableTab for idle fallback).
  useEffect(() => {
    fetch(`${daemonBase}/api/agent/hook-status`)
      .then((r) => {
        if (!r.ok) return
        return r.json()
      })
      .then((data: { installed?: boolean } | undefined) => {
        if (data) useAgentStore.getState().setHooksInstalled(!!data.installed)
      })
      .catch(() => { /* daemon unreachable — hooksInstalled stays false */ })
  }, [daemonBase])

  // --- Electron: signal SPA ready (replaces 500ms setTimeout) ---
  useEffect(() => {
    window.electronAPI?.signalReady()
  }, [])

  // --- Electron IPC: receive tab from tear-off/merge ---
  useEffect(() => {
    if (!window.electronAPI) return
    return window.electronAPI.onTabReceived((tabJson: string, replace: boolean) => {
      try {
        const tab = JSON.parse(tabJson)
        if (tab && tab.id && tab.layout) {
          if (replace) {
            // Tear-off: new window — clear persisted tabs, keep only the received one
            useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
          }
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

  // --- Bootstrap: fetch config (sessions fetched by multi-host WS onOpen) ---
  useEffect(() => {
    fetchConfig(daemonBase)
  }, [fetchConfig, daemonBase])

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
    <ErrorBoundary>
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
          onOpenHosts={() => {
            const tabId = useTabStore.getState().openSingletonTab({ kind: 'hosts' })
            if (activeWorkspaceId) {
              useWorkspaceStore.getState().addTabToWorkspace(activeWorkspaceId, tabId)
              useWorkspaceStore.getState().setWorkspaceActiveTab(activeWorkspaceId, tabId)
            }
            handleSelectTab(tabId)
          }}
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
      <GlobalUndoToast />
    </Router>
    </ErrorBoundary>
  )
}
