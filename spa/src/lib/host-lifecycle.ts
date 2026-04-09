// spa/src/lib/host-lifecycle.ts — Cascade delete logic for host removal with undo support
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore, type NormalizedEvent, type AgentStatus } from '../stores/useAgentStore'
import { useStreamStore, type PerSessionState } from '../stores/useStreamStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { scanPaneTree } from './pane-tree'
import type { Session } from './host-api'
import type { Tab } from '../types/tab'

/**
 * Execute cascade delete for a host: tabs -> sessions -> agent -> stream -> host.
 * Returns an undo function that restores all snapshot data.
 */
export function deleteHostCascade(hostId: string, closeTabs: boolean): () => void {
  const hostStore = useHostStore.getState()
  const tabStore = useTabStore.getState()
  const sessionStore = useSessionStore.getState()
  const agentStore = useAgentStore.getState()
  const streamStore = useStreamStore.getState()

  const prefix = `${hostId}:`

  // --- Snapshot for undo (serializable data only) ---
  const snapshot: {
    host: HostConfig | undefined
    hostOrder: string[]
    sessions: Session[] | undefined
    activeHostId: string | null
    // AgentStore data (exclude transient activeSubagents)
    agentEvents: Record<string, NormalizedEvent>
    agentStatuses: Record<string, AgentStatus>
    agentUnread: Record<string, boolean>
    agentModels: Record<string, string>
    // StreamStore data (exclude non-serializable conn)
    streamSessions: Record<string, Omit<PerSessionState, 'conn'>>
    // Tab data for undo
    closedTabs: Tab[]
    tabWorkspaces: Record<string, string>  // tabId -> workspaceId
    terminatedTabPaneIds: { tabId: string; paneId: string }[]
  } = {
    host: hostStore.hosts[hostId],
    hostOrder: [...hostStore.hostOrder],
    sessions: sessionStore.sessions[hostId],
    activeHostId: hostStore.activeHostId,
    agentEvents: {},
    agentStatuses: {},
    agentUnread: {},
    agentModels: {},
    streamSessions: {},
    closedTabs: [],
    tabWorkspaces: {},
    terminatedTabPaneIds: [],
  }

  // Snapshot AgentStore entries for this host
  for (const [k, v] of Object.entries(agentStore.lastEvents)) {
    if (k.startsWith(prefix)) snapshot.agentEvents[k] = v
  }
  for (const [k, v] of Object.entries(agentStore.statuses)) {
    if (k.startsWith(prefix)) snapshot.agentStatuses[k] = v
  }
  for (const [k, v] of Object.entries(agentStore.unread)) {
    if (k.startsWith(prefix)) snapshot.agentUnread[k] = v
  }
  for (const [k, v] of Object.entries(agentStore.models)) {
    if (k.startsWith(prefix)) snapshot.agentModels[k] = v
  }

  // Snapshot StreamStore entries for this host (exclude conn)
  for (const [k, v] of Object.entries(streamStore.sessions)) {
    if (k.startsWith(prefix)) {
      const { conn: _, ...serializable } = v // eslint-disable-line @typescript-eslint/no-unused-vars
      snapshot.streamSessions[k] = serializable
    }
  }

  // Execute cascade: tabs -> sessions -> agent -> stream -> host
  if (closeTabs) {
    const wsStore = useWorkspaceStore.getState()
    // Close all tmux-session tabs for this host (scan ALL panes, not just primary)
    for (const [tabId, tab] of Object.entries(tabStore.tabs)) {
      let hasHostPane = false
      scanPaneTree(tab.layout, (pane) => {
        if (pane.content.kind === 'tmux-session' && pane.content.hostId === hostId) {
          hasHostPane = true
        }
      })
      if (hasHostPane) {
        snapshot.closedTabs.push(tab)
        const tabWs = wsStore.findWorkspaceByTab(tabId)
        if (tabWs) snapshot.tabWorkspaces[tabId] = tabWs.id
        wsStore.closeTabInWorkspace(tabId, { skipHistory: true })
      }
    }
  } else {
    // Track which panes will be marked terminated (for undo)
    for (const [tabId, tab] of Object.entries(tabStore.tabs)) {
      scanPaneTree(tab.layout, (pane) => {
        if (pane.content.kind === 'tmux-session' && pane.content.hostId === hostId && !pane.content.terminated) {
          snapshot.terminatedTabPaneIds.push({ tabId, paneId: pane.id })
        }
      })
    }
    // Mark all tmux-session tabs as terminated
    tabStore.markHostTerminated(hostId, 'host-removed')
  }

  sessionStore.removeHost(hostId)
  agentStore.removeHost(hostId)
  streamStore.clearHost(hostId)
  hostStore.removeHost(hostId)

  // Return undo function
  return () => {
    // --- Restore host + hostOrder position ---
    if (snapshot.host) {
      useHostStore.getState().addHost(snapshot.host)
      // Restore original hostOrder position
      useHostStore.getState().reorderHosts(snapshot.hostOrder)
      if (snapshot.activeHostId === hostId) {
        useHostStore.getState().setActiveHost(hostId)
      }
    }

    // --- Restore sessions ---
    if (snapshot.sessions) {
      useSessionStore.getState().replaceHost(hostId, snapshot.sessions)
    }

    // --- Restore AgentStore data ---
    const ag = useAgentStore.getState()
    if (Object.keys(snapshot.agentEvents).length > 0) {
      useAgentStore.setState({
        lastEvents: { ...ag.lastEvents, ...snapshot.agentEvents },
        statuses: { ...ag.statuses, ...snapshot.agentStatuses },
        unread: { ...ag.unread, ...snapshot.agentUnread },
        models: { ...ag.models, ...snapshot.agentModels },
      })
    }

    // --- Restore StreamStore data (conn set to null) ---
    if (Object.keys(snapshot.streamSessions).length > 0) {
      const st = useStreamStore.getState()
      const restored: Record<string, PerSessionState> = {}
      for (const [k, v] of Object.entries(snapshot.streamSessions)) {
        restored[k] = { ...v, conn: null }
      }
      useStreamStore.setState({
        sessions: { ...st.sessions, ...restored },
      })
    }

    // --- Restore tabs ---
    if (closeTabs && snapshot.closedTabs.length > 0) {
      const ts = useTabStore.getState()
      for (const tab of snapshot.closedTabs) {
        // Only restore if tab wasn't re-created by user during undo window
        if (!ts.tabs[tab.id]) {
          useTabStore.getState().addTab(tab)
        }
      }
      // Restore workspace membership
      const currentWsStore = useWorkspaceStore.getState()
      for (const [tabId, wsId] of Object.entries(snapshot.tabWorkspaces)) {
        const wsExists = currentWsStore.workspaces.some((w) => w.id === wsId)
        if (wsExists && useTabStore.getState().tabs[tabId]) {
          useWorkspaceStore.getState().addTabToWorkspace(wsId, tabId)
        }
      }
    } else if (!closeTabs && snapshot.terminatedTabPaneIds.length > 0) {
      // Clear terminated marking on panes that were marked by this delete
      for (const { tabId, paneId } of snapshot.terminatedTabPaneIds) {
        const currentTab = useTabStore.getState().tabs[tabId]
        if (!currentTab) continue
        // Find the pane and clear its terminated field
        let found = false
        scanPaneTree(currentTab.layout, (pane) => {
          if (pane.id === paneId && pane.content.kind === 'tmux-session' && pane.content.terminated === 'host-removed') {
            found = true
          }
        })
        if (found) {
          // Re-read to get current content and remove terminated
          scanPaneTree(useTabStore.getState().tabs[tabId].layout, (pane) => {
            if (pane.id === paneId && pane.content.kind === 'tmux-session' && pane.content.terminated === 'host-removed') {
              const { terminated: _, ...contentWithoutTerminated } = pane.content // eslint-disable-line @typescript-eslint/no-unused-vars
              useTabStore.getState().setPaneContent(tabId, paneId, contentWithoutTerminated as typeof pane.content)
            }
          })
        }
      }
    }
  }
}
