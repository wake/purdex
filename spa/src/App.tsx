// spa/src/App.tsx — v2 重構：wouter Router + Tab/Pane model
import { useCallback, useEffect, useRef, useState } from 'react'
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
import { openBrowserTab } from './lib/open-browser-tab'
import { useNotificationDispatcher } from './hooks/useNotificationDispatcher'
import { useUndoToast } from './stores/useUndoToast'
import { useTabWorkspaceActions } from './hooks/useTabWorkspaceActions'
import { isStandaloneTab } from './types/tab'
import {
  getVisibleTabIds,
  WorkspaceContextMenu,
  MigrateTabsDialog,
  WorkspaceEmptyState,
} from './features/workspace'
import { TabContextMenu } from './components/TabContextMenu'
import { RenamePopover } from './components/RenamePopover'
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

  // Host store
  const hostOrder = useHostStore((s) => s.hostOrder)
  const firstHostId = hostOrder[0] ?? ''

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
          useWorkspaceStore.getState().insertTab(tab.id)
        }
      } catch { /* ignore malformed tab JSON */ }
    })
  }, [])

  // --- Electron IPC: receive workspace from tear-off/merge ---
  useEffect(() => {
    if (!window.electronAPI?.onWorkspaceReceived) return
    return window.electronAPI.onWorkspaceReceived((payload: string, replace: boolean) => {
      try {
        const { workspace, tabData } = JSON.parse(payload)
        if (!workspace?.id || !Array.isArray(tabData)) return

        // 校驗 tab ids
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
        // Only force-switch workspace on tear-off (replace); merge adds silently
        if (replace) {
          useWorkspaceStore.getState().setActiveWorkspace(workspace.id)
        }
        const activeTab = (workspace.activeTabId && tabMap.has(workspace.activeTabId))
          ? workspace.activeTabId
          : workspace.tabs[0]
        if (activeTab) useTabStore.getState().setActiveTab(activeTab)
      } catch { /* ignore malformed payload */ }
    })
  }, [])

  // --- Electron IPC: open browser tab from mini browser / WebContentsView link click ---
  useEffect(() => {
    if (!window.electronAPI?.onBrowserViewOpenInTab) return
    return window.electronAPI.onBrowserViewOpenInTab((url: string) => {
      openBrowserTab(url)
    })
  }, [])

  // --- Derived state ---
  const activeTab = activeTabId ? tabs[activeTabId] : undefined

  // --- Bootstrap: fetch config (sessions fetched by multi-host WS onOpen) ---
  useEffect(() => {
    if (firstHostId) fetchConfig(firstHostId)
  }, [fetchConfig, firstHostId])

  // --- Derive visible tabs for display ---
  const visibleTabIds = getVisibleTabIds({
    tabs,
    tabOrder,
    activeTabId,
    workspaces,
    activeWorkspaceId,
  })
  const displayTabs: Tab[] = visibleTabIds.map((id) => tabs[id]).filter(Boolean)

  const standaloneTabs = tabOrder
    .filter((id) => isStandaloneTab(id, workspaces))
    .map((id) => tabs[id])
    .filter(Boolean)

  const activeStandaloneTabId = activeTabId && isStandaloneTab(activeTabId, workspaces) ? activeTabId : null

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
    renameTarget,
    renameError,
    handleRenameConfirm,
    handleRenameCancel,
    handleClearRenameError,
  } = useTabWorkspaceActions(displayTabs)

  const openWsSettings = useCallback((wsId: string) => {
    const tabId = useTabStore.getState().openSingletonTab({ kind: 'settings', scope: { workspaceId: wsId } })
    useWorkspaceStore.getState().insertTab(tabId, wsId)
    handleSelectTab(tabId)
  }, [handleSelectTab])

  // --- Workspace UI state ---
  const [wsContextMenu, setWsContextMenu] = useState<{ wsId: string; position: { x: number; y: number } } | null>(null)
  const [migrateDialog, setMigrateDialog] = useState<{ wsId: string; wsName: string } | null>(null)

  const handleWsContextMenu = (e: React.MouseEvent, wsId: string) => {
    setWsContextMenu({ wsId, position: { x: e.clientX, y: e.clientY } })
  }

  const prepareWorkspacePayload = useCallback((wsId: string) => {
    const ws = workspaces.find(w => w.id === wsId)
    if (!ws || ws.tabs.length === 0) return null
    const tabData = ws.tabs.map(id => tabs[id]).filter(Boolean)
    if (tabData.length === 0) return null
    return { ws, payload: JSON.stringify({ workspace: ws, tabData }) }
  }, [workspaces, tabs])

  const removeWorkspaceFromStore = useCallback((tabIds: string[], wsId: string) => {
    // Read fresh state to avoid stale closure after async IPC
    const { tabs: currentTabs, tabOrder: currentTabOrder } = useTabStore.getState()
    const newTabs = { ...currentTabs }
    const newTabOrder = currentTabOrder.filter(id => !tabIds.includes(id))
    for (const id of tabIds) delete newTabs[id]
    useTabStore.setState({ tabs: newTabs, tabOrder: newTabOrder, activeTabId: null })
    useWorkspaceStore.getState().removeWorkspace(wsId)
    // Sync activeTabId with the new active workspace's activeTabId
    const wsState = useWorkspaceStore.getState()
    const newActiveWs = wsState.activeWorkspaceId
      ? wsState.workspaces.find(w => w.id === wsState.activeWorkspaceId)
      : null
    const syncedTabId = newActiveWs?.activeTabId ?? newActiveWs?.tabs[0] ?? newTabOrder[0] ?? null
    if (syncedTabId) useTabStore.getState().setActiveTab(syncedTabId)
  }, [])

  const handleWsTearOff = useCallback(async (wsId: string) => {
    if (!window.electronAPI) return
    const prepared = prepareWorkspacePayload(wsId)
    if (!prepared) return
    try {
      await window.electronAPI.tearOffWorkspace(prepared.payload)
      removeWorkspaceFromStore(prepared.ws.tabs, wsId)
    } catch { /* IPC failed — keep data intact */ }
  }, [prepareWorkspacePayload, removeWorkspaceFromStore])

  const handleWsMergeTo = useCallback(async (wsId: string, targetWindowId: string) => {
    if (!window.electronAPI) return
    const prepared = prepareWorkspacePayload(wsId)
    if (!prepared) return
    try {
      await window.electronAPI.mergeWorkspace(prepared.payload, targetWindowId)
      removeWorkspaceFromStore(prepared.ws.tabs, wsId)
    } catch { /* IPC failed — keep data intact */ }
  }, [prepareWorkspacePayload, removeWorkspaceFromStore])

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
            <div className="flex items-center" style={{ WebkitAppRegion: 'no-drag' } as React.CSSProperties}>
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
          activeWorkspaceId={activeStandaloneTabId ? null : activeWorkspaceId}
          activeStandaloneTabId={activeStandaloneTabId}
          onSelectWorkspace={handleSelectWorkspace}
          onSelectHome={() => {
            useWorkspaceStore.getState().setActiveWorkspace(null)
            const firstStandalone = standaloneTabs[0]
            if (firstStandalone) handleSelectTab(firstStandalone.id)
          }}
          standaloneTabCount={standaloneTabs.length}
          onAddWorkspace={() => {
            if (workspaces.length === 0 && tabOrder.length > 0) {
              const ws = useWorkspaceStore.getState().addWorkspace('Workspace 1')
              setMigrateDialog({ wsId: ws.id, wsName: ws.name })
            } else {
              const count = workspaces.length + 1
              const ws = useWorkspaceStore.getState().addWorkspace(`Workspace ${count}`)
              openWsSettings(ws.id)
            }
          }}
          onContextMenuWorkspace={handleWsContextMenu}
          onOpenHosts={() => {
            const tabId = useTabStore.getState().openSingletonTab({ kind: 'hosts' })
            useWorkspaceStore.getState().insertTab(tabId)
            handleSelectTab(tabId)
          }}
          onOpenSettings={() => {
            const tabId = useTabStore.getState().openSingletonTab({ kind: 'settings', scope: 'global' })
            useWorkspaceStore.getState().insertTab(tabId)
            handleSelectTab(tabId)
          }}
        />
        <div className="flex-1 flex flex-col min-w-0">
          {/* SPA: TabBar in normal position */}
          {!isElectron && (
            <div className="flex items-center bg-surface-secondary border-b border-border-subtle">
              <div className="flex-1 min-w-0">
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
              </div>
            </div>
          )}
          <div className="flex-1 flex overflow-hidden">
            {visibleTabIds.length === 0 && activeWorkspaceId !== null ? (
              <WorkspaceEmptyState />
            ) : (
              <TabContent
                activeTab={activeTab ?? null}
                allTabs={tabOrder.map((id) => tabs[id]).filter(Boolean)}
              />
            )}
          </div>
          <StatusBar
            activeTab={activeTab ?? null}
            onViewModeChange={(tabId, paneId, mode) => {
              useTabStore.getState().setViewMode(tabId, paneId, mode)
            }}
            onNavigateToHost={(hostId) => {
              const tabId = useTabStore.getState().openSingletonTab({ kind: 'hosts' })
              useWorkspaceStore.getState().insertTab(tabId)
              handleSelectTab(tabId)
              useHostStore.getState().setActiveHost(hostId)
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
        {renameTarget && (
          <RenamePopover
            anchorRect={renameTarget.anchorRect}
            currentName={renameTarget.currentName}
            onConfirm={handleRenameConfirm}
            onCancel={handleRenameCancel}
            error={renameError}
            onClearError={handleClearRenameError}
          />
        )}
        {wsContextMenu && (
          <WorkspaceContextMenu
            position={wsContextMenu.position}
            onSettings={() => openWsSettings(wsContextMenu.wsId)}
            onTearOff={window.electronAPI ? () => handleWsTearOff(wsContextMenu.wsId) : undefined}
            onMergeTo={window.electronAPI ? (targetWindowId) => handleWsMergeTo(wsContextMenu.wsId, targetWindowId) : undefined}
            onClose={() => setWsContextMenu(null)}
          />
        )}
        {migrateDialog && (
          <MigrateTabsDialog
            tabCount={tabOrder.length}
            workspaceName={migrateDialog.wsName}
            onMigrate={() => {
              tabOrder.forEach((tabId) => {
                useWorkspaceStore.getState().insertTab(tabId, migrateDialog.wsId)
              })
              setMigrateDialog(null)
              openWsSettings(migrateDialog.wsId)
            }}
            onSkip={() => {
              setMigrateDialog(null)
              useWorkspaceStore.getState().setActiveWorkspace(null)
            }}
          />
        )}
        </div>
      </div>
      <GlobalUndoToast />
    </Router>
    </ErrorBoundary>
  )
}
