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
import { useNotificationDispatcher } from './hooks/useNotificationDispatcher'
import { useUndoToast } from './stores/useUndoToast'
import { useTabWorkspaceActions } from './hooks/useTabWorkspaceActions'
import { isStandaloneTab } from './types/tab'
import {
  getVisibleTabIds,
  WorkspaceChip,
  WorkspaceContextMenu,
  WorkspaceDeleteDialog,
  WorkspaceRenameDialog,
  WorkspaceColorPicker,
  WorkspaceIconPicker,
  MigrateTabsDialog,
} from './features/workspace'
import { TabContextMenu } from './components/TabContextMenu'
import { ThemeInjector } from './components/ThemeInjector'
import { ErrorBoundary } from './components/ErrorBoundary'
import { getPlatformCapabilities } from './lib/platform'
import { getPrimaryPane } from './lib/pane-tree'
import { getPaneLabel } from './lib/pane-labels'
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
  } = useTabWorkspaceActions(displayTabs)

  const openWsSettings = useCallback((wsId: string) => {
    const tabId = useTabStore.getState().openSingletonTab({ kind: 'settings', scope: { workspaceId: wsId } })
    useWorkspaceStore.getState().insertTab(tabId, wsId)
    handleSelectTab(tabId)
  }, [handleSelectTab])

  // --- Workspace UI state ---
  const [wsContextMenu, setWsContextMenu] = useState<{ wsId: string; position: { x: number; y: number } } | null>(null)
  const [wsDeleteTarget, setWsDeleteTarget] = useState<string | null>(null)
  const [wsRenameTarget, setWsRenameTarget] = useState<string | null>(null)
  const [wsColorTarget, setWsColorTarget] = useState<string | null>(null)
  const [wsIconTarget, setWsIconTarget] = useState<string | null>(null)
  const [migrateDialog, setMigrateDialog] = useState<{ wsId: string; wsName: string } | null>(null)

  const activeWs = workspaces.find((w) => w.id === activeWorkspaceId)

  const handleWsContextMenu = (e: React.MouseEvent, wsId: string) => {
    setWsContextMenu({ wsId, position: { x: e.clientX, y: e.clientY } })
  }

  const syncActiveTabAfterWsRemove = () => {
    const { activeWorkspaceId: newWsId, workspaces: remaining } = useWorkspaceStore.getState()
    const newWs = remaining.find((w) => w.id === newWsId)
    const nextTab = newWs?.activeTabId ?? newWs?.tabs[0]
    if (nextTab) useTabStore.getState().setActiveTab(nextTab)
  }

  const handleWsDelete = (wsId: string) => {
    const ws = workspaces.find((w) => w.id === wsId)
    if (!ws) return
    if (ws.tabs.length === 0) {
      useWorkspaceStore.getState().removeWorkspace(wsId)
      syncActiveTabAfterWsRemove()
    } else {
      setWsDeleteTarget(wsId)
    }
  }

  const handleWsDeleteConfirm = (closedTabIds: string[]) => {
    if (!wsDeleteTarget) return
    const ws = workspaces.find((w) => w.id === wsDeleteTarget)
    const hasPreservedTabs = ws && closedTabIds.length < ws.tabs.length
    closedTabIds.forEach((tabId) => handleCloseTab(tabId))
    useWorkspaceStore.getState().removeWorkspace(wsDeleteTarget)
    if (hasPreservedTabs) {
      useWorkspaceStore.getState().setActiveWorkspace(null)
    } else {
      syncActiveTabAfterWsRemove()
    }
    setWsDeleteTarget(null)
  }

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
              {activeWs && !activeStandaloneTabId && (
                <WorkspaceChip
                  name={activeWs.name}
                  color={activeWs.color}
                  icon={activeWs.icon}
                  onClick={() => openWsSettings(activeWs.id)}
                  onContextMenu={(e) => handleWsContextMenu(e, activeWs.id)}
                />
              )}
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
              {activeWs && !activeStandaloneTabId && (
                <WorkspaceChip
                  name={activeWs.name}
                  color={activeWs.color}
                  icon={activeWs.icon}
                  onClick={() => openWsSettings(activeWs.id)}
                  onContextMenu={(e) => handleWsContextMenu(e, activeWs.id)}
                />
              )}
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
        {wsContextMenu && (
          <WorkspaceContextMenu
            position={wsContextMenu.position}
            onRename={() => { setWsRenameTarget(wsContextMenu.wsId); setWsContextMenu(null) }}
            onChangeColor={() => { setWsColorTarget(wsContextMenu.wsId); setWsContextMenu(null) }}
            onChangeIcon={() => { setWsIconTarget(wsContextMenu.wsId); setWsContextMenu(null) }}
            onSettings={() => openWsSettings(wsContextMenu.wsId)}
            onDelete={() => { handleWsDelete(wsContextMenu.wsId); setWsContextMenu(null) }}
            onClose={() => setWsContextMenu(null)}
          />
        )}
        {wsDeleteTarget && (() => {
          const ws = workspaces.find((w) => w.id === wsDeleteTarget)
          if (!ws) return null
          const t = useI18nStore.getState().t
          const tabItems = ws.tabs
            .map((tabId) => {
              const tab = tabs[tabId]
              if (!tab) return null
              const content = getPrimaryPane(tab.layout).content
              const label = getPaneLabel(content, { getByCode: () => undefined }, { getById: () => undefined }, t)
              return { id: tabId, label }
            })
            .filter(Boolean) as { id: string; label: string }[]
          return (
            <WorkspaceDeleteDialog
              workspaceName={ws.name}
              tabs={tabItems}
              onConfirm={handleWsDeleteConfirm}
              onCancel={() => setWsDeleteTarget(null)}
            />
          )
        })()}
        {wsRenameTarget && (
          <WorkspaceRenameDialog
            currentName={workspaces.find((w) => w.id === wsRenameTarget)?.name ?? ''}
            onConfirm={(name) => { useWorkspaceStore.getState().renameWorkspace(wsRenameTarget, name); setWsRenameTarget(null) }}
            onCancel={() => setWsRenameTarget(null)}
          />
        )}
        {wsColorTarget && (
          <WorkspaceColorPicker
            currentColor={workspaces.find((w) => w.id === wsColorTarget)?.color ?? '#888'}
            onSelect={(color) => { useWorkspaceStore.getState().setWorkspaceColor(wsColorTarget, color); setWsColorTarget(null) }}
            onCancel={() => setWsColorTarget(null)}
          />
        )}
        {wsIconTarget && (
          <WorkspaceIconPicker
            currentIcon={workspaces.find((w) => w.id === wsIconTarget)?.icon}
            onSelect={(icon) => { useWorkspaceStore.getState().setWorkspaceIcon(wsIconTarget, icon); setWsIconTarget(null) }}
            onCancel={() => setWsIconTarget(null)}
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
