// spa/src/lib/host-lifecycle.ts — Cascade delete logic for host removal with undo support
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore, type NormalizedEvent, type AgentStatus } from '../stores/useAgentStore'
import { useExecutionStore, splitExecutionKey } from '../stores/useExecutionStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { releaseLease } from './nex/nex-api'
import { useHostSettingsStore } from '../stores/useHostSettingsStore'
import { usePeerStore } from '../stores/usePeerStore'
import { useSessionCwdStore } from '../stores/useSessionCwdStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { useLocalProfilesStore, type ParkedWorld } from '../stores/useLocalProfilesStore'
import { scanPaneTree } from './pane-tree'
import type { Session } from './host-api'
import type { PaneContent, PaneLayout, Tab } from '../types/tab'

// === Parked worlds (Profile Sync P3b) ===
//
// The tab store holds ONE tab world — the profile on screen. The master's world
// and every other local profile's are parked in `useLocalProfilesStore`, and they
// all use the same hosts (a local profile borrows them). A host that is removed is
// gone for all of them, so the cascade reaches the parked worlds too: otherwise a
// world that comes back on screen would show live-looking panes on a host that no
// longer exists, and the parked MASTER — which keeps syncing while parked — would
// never tell the other devices what the device that removed the host always tells
// them (`terminated: 'host-removed'` is a synced field).
//
// A PARKED WORLD IS MARKED, NEVER CLOSED — also under `closeTabs: true`. That flag
// is the user's answer about the tabs they are looking at; tabs of a profile that
// is out of sight are not theirs to lose to it (a close records no history, so
// after the undo toast it could not be taken back). Marked, they say what
// happened the next time that profile is opened, like every `host-removed` pane.

interface PaneRef {
  tabId: string
  paneId: string
}

/** `layout` with `fn` applied to every pane's content; the same object when `fn` changed nothing. */
function mapPaneContents(layout: PaneLayout, fn: (content: PaneContent, paneId: string) => PaneContent): PaneLayout {
  if (layout.type === 'leaf') {
    const content = fn(layout.pane.content, layout.pane.id)
    return content === layout.pane.content ? layout : { ...layout, pane: { ...layout.pane, content } }
  }
  const children = layout.children.map((child) => mapPaneContents(child, fn))
  return children.some((c, i) => c !== layout.children[i]) ? { ...layout, children } : layout
}

function mapWorldPanes(world: ParkedWorld, fn: (content: PaneContent, tabId: string, paneId: string) => PaneContent): ParkedWorld {
  let changed = false
  const tabs: Record<string, Tab> = {}
  for (const [tabId, tab] of Object.entries(world.tabs)) {
    const layout = mapPaneContents(tab.layout, (content, paneId) => fn(content, tabId, paneId))
    tabs[tabId] = layout === tab.layout ? tab : { ...tab, layout }
    if (layout !== tab.layout) changed = true
  }
  return changed ? { ...world, tabs } : world
}

/**
 * What keep-tabs mode does to the world on screen, for every parked one: live
 * `tmux-session` panes on `hostId` marked `host-removed`, hostless execution
 * panes pinned when the host was the fallback. Returns the panes it MARKED.
 */
function removeHostFromParkedWorlds(hostId: string, pinHostless: boolean): PaneRef[] {
  const marked: PaneRef[] = []
  useLocalProfilesStore.getState().updateParkedWorlds((world) =>
    mapWorldPanes(world, (content, tabId, paneId) => {
      if (content.kind === 'tmux-session' && content.hostId === hostId && !content.terminated) {
        marked.push({ tabId, paneId })
        return { ...content, terminated: 'host-removed' }
      }
      if (content.kind === 'execution' && !content.host && pinHostless) return { ...content, host: hostId }
      return content
    }),
  )
  return marked
}

/** Takes `host-removed` back off exactly `refs` — in whatever parked world each pane is by now. */
function unmarkInParkedWorlds(refs: readonly PaneRef[]): void {
  const wanted = new Set(refs.map((r) => `${r.tabId}\u0000${r.paneId}`))
  useLocalProfilesStore.getState().updateParkedWorlds((world) =>
    mapWorldPanes(world, (content, tabId, paneId) => {
      if (content.kind !== 'tmux-session' || content.terminated !== 'host-removed' || !wanted.has(`${tabId}\u0000${paneId}`)) return content
      const { terminated: _, ...rest } = content
      return rest as typeof content
    }),
  )
}

/**
 * Execute cascade delete for a host: tabs -> sessions -> agent -> host.
 * Returns an undo function that restores all snapshot data.
 */
export function deleteHostCascade(hostId: string, closeTabs: boolean): () => void {
  const hostStore = useHostStore.getState()
  const tabStore = useTabStore.getState()
  const sessionStore = useSessionStore.getState()
  const agentStore = useAgentStore.getState()

  // Mirror the veto in `useHostStore.removeHost()` up front: if the host
  // doesn't exist or is the last remaining host (store refuses to delete it),
  // abort the cascade entirely rather than clearing per-host state that
  // won't ever be matched by a real removal. Without this guard the cascade
  // would wipe sessions/agent/settings, then `removeHost()` would
  // no-op, and the undo callback's recreation guard would treat the still-
  // present host row as a recreation and skip every restore — permanent
  // data loss on the last host.
  if (!hostStore.hosts[hostId] || Object.keys(hostStore.hosts).length <= 1) {
    return () => {}
  }

  const prefix = `${hostId}:`
  // Captured before any removal: a hostless execution pane resolves to the
  // first host (resolveExecutionHostId), so that is its effective host.
  const fallbackHost = hostStore.hostOrder[0]
  const effectiveExecutionHost = (host: string | undefined) => host || fallbackHost

  // --- Snapshot for undo (serializable data only) ---
  const snapshot: {
    host: HostConfig | undefined
    hostOrder: string[]
    sessions: Session[] | undefined
    hostSettings: Record<string, Record<string, unknown>> | undefined
    activeHostId: string | null
    // AgentStore data (exclude transient activeSubagents)
    agentEvents: Record<string, NormalizedEvent>
    agentStatuses: Record<string, AgentStatus>
    agentUnread: Record<string, boolean>
    agentModels: Record<string, string>
    // Tab data for undo
    closedTabs: Tab[]
    tabWorkspaces: Record<string, string>  // tabId -> workspaceId
    terminatedTabPaneIds: { tabId: string; paneId: string }[]
  } = {
    host: hostStore.hosts[hostId],
    hostOrder: [...hostStore.hostOrder],
    sessions: sessionStore.sessions[hostId],
    hostSettings: useHostSettingsStore.getState().hosts[hostId],
    activeHostId: hostStore.activeHostId,
    agentEvents: {},
    agentStatuses: {},
    agentUnread: {},
    agentModels: {},
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

  // Execute cascade: tabs -> sessions -> agent -> host
  if (closeTabs) {
    const wsStore = useWorkspaceStore.getState()
    // Close all tmux-session tabs for this host (scan ALL panes, not just primary)
    for (const [tabId, tab] of Object.entries(tabStore.tabs)) {
      let hasHostPane = false
      scanPaneTree(tab.layout, (pane) => {
        if (pane.content.kind === 'tmux-session' && pane.content.hostId === hostId) {
          hasHostPane = true
        }
        // Execution panes (Nexen, spec §4.3.4) match on their resolved host,
        // so a legacy hostless pane bound to the removed first host closes too.
        if (pane.content.kind === 'execution' && effectiveExecutionHost(pane.content.host) === hostId) {
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
    // A hostless execution pane renders against the first host; once that
    // host is gone it would silently rebind to the next one (spec §4.3.2
    // step 5 forbids that). Pin it to the removed host so it renders
    // `host_removed`. Undo leaves the pin: with the host restored,
    // host === hostId is exactly what the pane was showing.
    for (const [tabId, tab] of Object.entries(useTabStore.getState().tabs)) {
      scanPaneTree(tab.layout, (pane) => {
        if (pane.content.kind === 'execution' && !pane.content.host && fallbackHost === hostId) {
          useTabStore.getState().setPaneContent(tabId, pane.id, { ...pane.content, host: hostId })
        }
      })
    }
  }

  // Every parked world, in both modes (see "Parked worlds" above).
  const parkedMarks = removeHostFromParkedWorlds(hostId, fallbackHost === hostId)

  sessionStore.removeHost(hostId)
  agentStore.removeHost(hostId)
  // Nexen execution view state for this host. Runs after the tab-close loop
  // above so no execution pane's hook observes a half-cleared store (spec
  // §4.3.4); undo restores the tabs, whose hooks re-subscribe from scratch,
  // so nothing here needs snapshotting.
  //
  // A held lease: closeTabs unmounts the panes via the tab-close loop
  // above, but the pane's own release() (useExecutionLease's unmount
  // effect) races clearHost below — by the time React actually tears the
  // component down, clearHost may already have wiped the lease out from
  // under it, so release() finds nothing to release. Best-effort release
  // every held lease on this host here instead, before the store is
  // cleared — but only in closeTabs mode (spec §4.3.4: closeTabs releases
  // a held lease; keep-tabs mode drops the local lease without a release
  // call, since the host — and its auth — is gone).
  if (closeTabs) {
    for (const [key, execution] of Object.entries(useExecutionStore.getState().executions)) {
      const { hostId: execHostId, executionId } = splitExecutionKey(key)
      if (execHostId === hostId && execution.lease) {
        void releaseLease(hostId, executionId, execution.lease.leaseId).catch(() => {})
      }
    }
  }
  useExecutionStore.getState().clearHost(hostId)
  useExecutionListStore.getState().clearHost(hostId)
  useNexHostStore.getState().clearHost(hostId)
  useHostSettingsStore.getState().clearHost(hostId)
  // Peer rows are a cache of a daemon that is no longer configured. Nothing to
  // snapshot: undo restores the host, and the first render that needs its peers
  // fetches them again (`usePeerInfo`'s predicate fires on an absent entry).
  // The cwd readings go the same way and for the same reason — a directory read
  // from one daemon says nothing about another.
  usePeerStore.getState().forgetHost(hostId)
  useSessionCwdStore.getState().forgetHost(hostId)
  hostStore.removeHost(hostId)

  // Return undo function
  return () => {
    // Guard against host-recreation race: if another code path (import,
    // cross-window BroadcastChannel sync, user re-add) re-created a host
    // with the same id during the undo window, the current entry is a
    // different entity than the one we snapshotted. Skip any restore that
    // would overwrite the user's freshly written state (settings, sessions,
    // agent/stream data). addHost itself already dedupes, but reorderHosts
    // and setActiveHost would otherwise silently mutate the new host's
    // position / activation.
    const hostWasRecreated = useHostStore.getState().hosts[hostId] !== undefined

    // --- Restore host + hostOrder position ---
    if (snapshot.host && !hostWasRecreated) {
      useHostStore.getState().addHost(snapshot.host)
      // Restore original hostOrder position
      useHostStore.getState().reorderHosts(snapshot.hostOrder)
      if (snapshot.activeHostId === hostId) {
        useHostStore.getState().setActiveHost(hostId)
      }
    }

    // --- Restore sessions ---
    if (snapshot.sessions && !hostWasRecreated) {
      useSessionStore.getState().replaceHost(hostId, snapshot.sessions)
    }

    if (snapshot.hostSettings && !hostWasRecreated) {
      useHostSettingsStore.setState((state) => ({
        hosts: {
          ...state.hosts,
          [hostId]: snapshot.hostSettings!,
        },
      }))
    }

    // --- Restore AgentStore data ---
    const ag = useAgentStore.getState()
    if (!hostWasRecreated && Object.keys(snapshot.agentEvents).length > 0) {
      useAgentStore.setState({
        lastEvents: { ...ag.lastEvents, ...snapshot.agentEvents },
        statuses: { ...ag.statuses, ...snapshot.agentStatuses },
        unread: { ...ag.unread, ...snapshot.agentUnread },
        models: { ...ag.models, ...snapshot.agentModels },
      })
    }

    // --- Restore tabs ---
    // Tabs carry the deleted host's hostId/sessionCode; if a different entity
    // now owns the same hostId (recreated during undo window), we must not
    // re-bind stale panes to it — gate tab restore on !hostWasRecreated too.
    if (closeTabs && !hostWasRecreated && snapshot.closedTabs.length > 0) {
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
    }

    // --- Clear the `host-removed` marks this delete made ---
    // On screen (keep-tabs mode) and in the parked worlds (both modes) — and looked
    // for in BOTH places whichever it was: the user may have switched profiles
    // inside the undo window, so a pane marked on screen can be parked by now and
    // the other way round. Same gate as the tabs above: a recreated host owns
    // these panes now, and what it made of them is not this undo's to touch.
    const marks = [...snapshot.terminatedTabPaneIds, ...parkedMarks]
    if (!hostWasRecreated && marks.length > 0) {
      for (const { tabId, paneId } of marks) {
        const currentTab = useTabStore.getState().tabs[tabId]
        if (!currentTab) continue
        scanPaneTree(currentTab.layout, (pane) => {
          if (pane.id === paneId && pane.content.kind === 'tmux-session' && pane.content.terminated === 'host-removed') {
            const { terminated: _, ...contentWithoutTerminated } = pane.content
            useTabStore.getState().setPaneContent(tabId, paneId, contentWithoutTerminated as typeof pane.content)
          }
        })
      }
      unmarkInParkedWorlds(marks)
    }
  }
}

/** Endpoint identity of a host: what makes a cached answer still that host's. */
function hostIdentity(h: HostConfig | undefined): string {
  return h ? `${h.ip}:${h.port}:${h.token ?? ''}` : ''
}

/**
 * Drop a host's cached peer rows and cwd readings whenever its daemon identity
 * changes (peer-info-panel spec §3.1).
 *
 * A peer address names a process on one machine, so keeping the cache across a
 * re-point would show one daemon's peers under another's name — and the address
 * is copyable, so the wrong one gets pasted into `pdx msg send`.
 *
 * This is a subscription rather than a line inside `useHostStore.updateHost`
 * because `useHostStore` sits at the bottom of the import graph on purpose
 * (`host-api` imports it, and `usePeerStore` imports `host-api`); the same
 * shape already covers the host-config cache in `host-config-loader.ts`. It
 * also catches removals that never go through `deleteHostCascade`, such as a
 * sync full-replace. Started once from `main.tsx`; returns its unsubscribe.
 */
export function startPeerCacheInvalidation(): () => void {
  return useHostStore.subscribe((next, prev) => {
    if (next.hosts === prev.hosts) return
    for (const hostId of Object.keys(prev.hosts)) {
      const after = next.hosts[hostId]
      // A removed host and a re-pointed one are the same problem: whatever is
      // cached belongs to a daemon this id no longer names.
      if (after && hostIdentity(prev.hosts[hostId]) === hostIdentity(after)) continue
      usePeerStore.getState().forgetHost(hostId)
      useSessionCwdStore.getState().forgetHost(hostId)
    }
  })
}
