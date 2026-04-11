import { useCallback } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'

/**
 * Workspace tear-off / merge handlers for Electron multi-window.
 * Returns no-op-safe handlers — callers don't need to check electronAPI.
 */
export function useWorkspaceWindowActions() {
  const workspaces = useWorkspaceStore((s) => s.workspaces)
  const tabs = useTabStore((s) => s.tabs)

  const prepareWorkspacePayload = useCallback((wsId: string) => {
    const ws = workspaces.find(w => w.id === wsId)
    if (!ws || ws.tabs.length === 0) return null
    const tabData = ws.tabs.map(id => tabs[id]).filter(Boolean)
    if (tabData.length === 0) return null
    return { ws, payload: JSON.stringify({ workspace: ws, tabData }) }
  }, [workspaces, tabs])

  const removeWorkspaceFromStore = useCallback((tabIds: string[], wsId: string) => {
    // Read fresh state via getState() to avoid stale closure after async IPC.
    // The IPC await in handleWsTearOff/handleWsMergeTo may take long enough for
    // store state to change; using closure values would cause lost updates.
    const { tabs: currentTabs, tabOrder: currentTabOrder } = useTabStore.getState()
    const newTabs = { ...currentTabs }
    const newTabOrder = currentTabOrder.filter(id => !tabIds.includes(id))
    for (const id of tabIds) delete newTabs[id]
    useTabStore.setState({ tabs: newTabs, tabOrder: newTabOrder, activeTabId: null })
    useWorkspaceStore.getState().removeWorkspace(wsId)
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

  return { handleWsTearOff, handleWsMergeTo }
}
