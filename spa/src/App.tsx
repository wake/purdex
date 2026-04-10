// spa/src/App.tsx — v2 重構：wouter Router + Tab/Pane model
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Router } from 'wouter'
import { ActivityBar } from './components/ActivityBar'
import { TabBar } from './components/TabBar'
import { TabContent } from './components/TabContent'
import { StatusBar } from './components/StatusBar'
import { TitleBar } from './components/TitleBar'
import { SidebarRegion } from './components/SidebarRegion'
import { useConfigStore } from './stores/useConfigStore'
import { useTabStore } from './stores/useTabStore'
import { useWorkspaceStore } from './stores/useWorkspaceStore'
import { useHostStore } from './stores/useHostStore'
import { useRelayWsManager } from './hooks/useRelayWsManager'
import { useMultiHostEventWs } from './hooks/useMultiHostEventWs'
import { useRouteSync } from './hooks/useRouteSync'
import { useShortcuts } from './hooks/useShortcuts'
import './lib/browser-shortcuts'
import { useNotificationDispatcher } from './hooks/useNotificationDispatcher'
import { useElectronIpc } from './hooks/useElectronIpc'
import { useTabWorkspaceActions } from './hooks/useTabWorkspaceActions'
import { useWorkspaceWindowActions } from './hooks/useWorkspaceWindowActions'
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
import type { Tab } from './types/tab'
import { GlobalUndoToast } from './components/GlobalUndoToast'

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
  useElectronIpc()
  const { handleWsTearOff, handleWsMergeTo } = useWorkspaceWindowActions()

  // --- Derived state ---
  const activeTab = activeTabId ? tabs[activeTabId] : undefined
  const titleText = activeWorkspaceId ? (workspaces.find(w => w.id === activeWorkspaceId)?.name ?? 'tmux-box') : 'tmux-box'

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

  const standaloneTabIds = useMemo(
    () => tabOrder.filter((id) => isStandaloneTab(id, workspaces)),
    [tabOrder, workspaces],
  )
  const standaloneTabs = standaloneTabIds.map((id) => tabs[id]).filter(Boolean)

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

  return (
    <ErrorBoundary>
    <Router>
      <ThemeInjector />
      <div className="h-screen flex flex-col bg-surface-primary text-text-primary">
        {isElectron && <TitleBar title={titleText} />}
        <div className="flex-1 flex min-h-0">
          <ActivityBar
            workspaces={workspaces}
            activeWorkspaceId={activeStandaloneTabId ? null : activeWorkspaceId}
            activeStandaloneTabId={activeStandaloneTabId}
            onSelectWorkspace={handleSelectWorkspace}
            onSelectHome={() => {
              useWorkspaceStore.getState().setActiveWorkspace(null)
              const firstStandalone = standaloneTabs[0]
              if (firstStandalone) {
                handleSelectTab(firstStandalone.id)
              } else {
                useTabStore.getState().setActiveTab(null)
              }
            }}
            standaloneTabIds={standaloneTabIds}
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
            onReorderWorkspaces={(ids) => useWorkspaceStore.getState().reorderWorkspaces(ids)}
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
          <SidebarRegion region="primary-sidebar" resizeEdge="right" />
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
              <SidebarRegion region="primary-panel" resizeEdge="right" />
              {visibleTabIds.length === 0 && activeWorkspaceId !== null ? (
                <WorkspaceEmptyState />
              ) : (
                <TabContent
                  activeTab={activeTab ?? null}
                  allTabs={tabOrder.map((id) => tabs[id]).filter(Boolean)}
                />
              )}
              <SidebarRegion region="secondary-panel" resizeEdge="left" />
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
          <SidebarRegion region="secondary-sidebar" resizeEdge="left" />
        {contextMenu && (
          <TabContextMenu
            tab={contextMenu.tab}
            position={contextMenu.position}
            onClose={() => setContextMenu(null)}
            onAction={handleContextAction}
            hasOtherUnlocked={displayTabs.some((t) => t.id !== contextMenu.tab.id && !t.locked)}
            hasRightUnlocked={contextMenuHasRightUnlocked}
            targetTabs={displayTabs.filter((t) =>
              t.id !== contextMenu.tab.id && t.layout.type === 'split' && !t.locked
            )}
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
