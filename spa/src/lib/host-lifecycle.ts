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
import { MASTER_PROFILE_ID, relabelCountInStorage, useLocalProfilesStore, type ParkedWorld } from '../stores/useLocalProfilesStore'
import { readMasterWorld, recoverUnsettledWorld } from './profile/master-world'
import { useUndoToast } from '../stores/useUndoToast'
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

//
// THE UNDO KNOWS WHOSE WORLD EACH ENTRY IS FROM. Tab, pane and workspace ids are
// unique inside ONE world only: a demoted master keeps its ids, and a master
// pulled later brings the very same ones (switch-active.ts, `deleteSlave`). And
// the user can switch profiles while the undo toast is up. So everything the
// snapshot holds — a closed tab, a marked pane — carries its WORLD OWNER
// (`'master'` or a slave id: whoever was on screen, or the parked world it was
// found in), and the undo puts it back into THAT world, wherever it is by then:
//
//   on screen → the live stores          parked → its `ParkedWorld`
//   gone (the slave was deleted)         → that entry is skipped
//
// Without it a tab closed in a slave and undone after a switch landed in the
// MASTER's live stores — in its workspace, when the ids matched — and the
// collector pushed a slave's tab to the SOT; and a mark was cleared by bare ids,
// taking `host-removed` off a pane of another world that an earlier delete had
// marked. "On screen" is asked of all three world stores (both tags and the
// pointer): half-way through another window's switch nobody can say whose tabs
// the screen holds (lib/profile/master-world.ts), and then the entry is skipped
// as well — an undo that loses a tab in a window of milliseconds, against one
// that files it under the wrong profile.
//   A PROMOTE (`promoteToMaster`) RELABELS THE WORLDS: the old master becomes a
// slave under a NEW id and `'master'` names what was a slave — an owner recorded
// before it points at another world afterwards (and the old master's new id is
// not something this file could look up: it is minted by the promote). The
// parking lot counts promotes (`relabelCount`); the snapshot keeps the count, and
// an undo that finds it moved restores THE HOST AND NOTHING OF ANY WORLD — no
// closed tab, no workspace membership, no mark — and says so
// (`{ worldSkipped: true }`; the caller tells the user). An undo that loses tabs,
// against one that files them under the wrong profile.
//   AND THE PROMOTE MAY BE ANOTHER WINDOW'S, NOT HEARD OF HERE YET: this window's
// memory still holds the old labels and the old count. So the undo asks storage,
// not memory, twice over — the door (`readMasterWorld`: ANY unsettled answer,
// `behind-fence` included, means nobody can name the worlds right now; the stores
// are asked to catch up), and the persisted count (`relabelCountInStorage`, for
// the instant in which the other window's pointer is visible and its fence is
// not). Without them the restore went into the old world, was dropped by the
// fence — or, worse, was not — and the undo answered "nothing skipped".
//   (No import cycle: master-world.ts imports stores and pure profile modules;
// the edge that would close one is apply-to-stores.ts → this file.)

/** `'master'` or a slave id. */
type WorldOwner = string

interface PaneRef {
  tabId: string
  paneId: string
  owner: WorldOwner
}

/** Whose world the live stores hold — the tab store's tag says it of the tabs; the pointer when the tag is junk. */
function screenOwner(): WorldOwner {
  const tag = useTabStore.getState().worldId as unknown
  return typeof tag === 'string' ? tag : useLocalProfilesStore.getState().activeProfileId
}

/** Where `owner`'s world is right now (see THE UNDO KNOWS WHOSE WORLD EACH ENTRY IS FROM). */
function locateWorld(owner: WorldOwner): { where: 'screen' } | { where: 'parked'; world: ParkedWorld } | { where: 'gone' } {
  const local = useLocalProfilesStore.getState()
  if (local.activeProfileId === owner) {
    const agreed = useTabStore.getState().worldId === owner && useWorkspaceStore.getState().worldId === owner
    return agreed ? { where: 'screen' } : { where: 'gone' }
  }
  const world = owner === MASTER_PROFILE_ID ? local.parkedMaster : Object.hasOwn(local.slaves, owner) ? local.slaves[owner].world : null
  return world === null ? { where: 'gone' } : { where: 'parked', world }
}

/** `world` with `tabs` back — those it does not hold by now — and each one back in its workspace, if that still exists. */
function restoreTabsInWorld(world: ParkedWorld, tabs: readonly Tab[], workspaceOf: Record<string, string>): ParkedWorld {
  const nextTabs = { ...world.tabs }
  for (const tab of tabs) {
    if (!Object.hasOwn(nextTabs, tab.id)) nextTabs[tab.id] = tab
  }
  const workspaces = world.workspaces.map((ws) => {
    const back = tabs.map((t) => t.id).filter((id) => workspaceOf[id] === ws.id && !ws.tabs.includes(id))
    return back.length === 0 ? ws : { ...ws, tabs: [...ws.tabs, ...back] }
  })
  return { ...world, tabs: nextTabs, workspaces }
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
  useLocalProfilesStore.getState().updateParkedWorlds((world, owner) =>
    mapWorldPanes(world, (content, tabId, paneId) => {
      if (content.kind === 'tmux-session' && content.hostId === hostId && !content.terminated) {
        marked.push({ tabId, paneId, owner })
        return { ...content, terminated: 'host-removed' }
      }
      if (content.kind === 'execution' && !content.host && pinHostless) return { ...content, host: hostId }
      return content
    }),
  )
  return marked
}

/** Takes `host-removed` back off exactly `refs` — each in ITS OWNER's world, if that world is parked by now. */
function unmarkInParkedWorlds(refs: readonly PaneRef[]): void {
  const key = (owner: WorldOwner, tabId: string, paneId: string): string => `${owner}\u0000${tabId}\u0000${paneId}`
  const wanted = new Set(refs.map((r) => key(r.owner, r.tabId, r.paneId)))
  useLocalProfilesStore.getState().updateParkedWorlds((world, owner) =>
    mapWorldPanes(world, (content, tabId, paneId) => {
      if (content.kind !== 'tmux-session' || content.terminated !== 'host-removed' || !wanted.has(key(owner, tabId, paneId))) return content
      const { terminated: _, ...rest } = content
      return rest as typeof content
    }),
  )
}

/** What an undo did NOT do: `worldSkipped` — the worlds were relabelled since the delete (a promote), so no tab,
 *  workspace membership or mark was restored; the host and everything else was. */
export interface HostDeleteUndoResult {
  worldSkipped: boolean
}

/**
 * Execute cascade delete for a host: tabs -> sessions -> agent -> host.
 * Returns an undo function that restores all snapshot data.
 */
export function deleteHostCascade(hostId: string, closeTabs: boolean): () => HostDeleteUndoResult {
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
    return () => ({ worldSkipped: false })
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
    /** Whose world was on screen: the owner of `closedTabs` / `tabWorkspaces` and of every on-screen mark. */
    owner: WorldOwner
    /** `useLocalProfilesStore.relabelCount` at delete time: moved by undo time ⇒ no owner names its world any more. */
    relabelCount: number
    closedTabs: Tab[]
    tabWorkspaces: Record<string, string>  // tabId -> workspaceId
    terminatedTabPaneIds: PaneRef[]
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
    owner: screenOwner(),
    relabelCount: useLocalProfilesStore.getState().relabelCount,
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
          snapshot.terminatedTabPaneIds.push({ tabId, paneId: pane.id, owner: snapshot.owner })
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
    // They go back into the world they were closed in (`snapshot.owner`), wherever
    // that is by now — never into whatever happens to be on screen.
    // …unless the worlds were relabelled meanwhile (see A PROMOTE RELABELS THE WORLDS): then nothing of any world.
    const door = readMasterWorld()
    recoverUnsettledWorld(door, true) // the user asked: catch up, whatever the background has tried
    const onDisk = relabelCountInStorage()
    const relabelled = !door.settled || useLocalProfilesStore.getState().relabelCount !== snapshot.relabelCount || (onDisk !== null && onDisk !== snapshot.relabelCount)
    const home: ReturnType<typeof locateWorld> = relabelled ? { where: 'gone' } : locateWorld(snapshot.owner)
    if (closeTabs && !hostWasRecreated && snapshot.closedTabs.length > 0 && home.where === 'screen') {
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
    } else if (closeTabs && !hostWasRecreated && snapshot.closedTabs.length > 0 && home.where === 'parked') {
      // A refusal (`bad-world`) leaves the parked world as it was: the undo loses these tabs, and nothing else.
      useLocalProfilesStore.getState().replaceParkedWorld(snapshot.owner, restoreTabsInWorld(home.world, snapshot.closedTabs, snapshot.tabWorkspaces))
    }

    // --- Clear the `host-removed` marks this delete made ---
    // On screen (keep-tabs mode) and in the parked worlds (both modes). The user may
    // have switched profiles inside the undo window, so a pane marked on screen can
    // be parked by now and the other way round: each mark is looked for where ITS
    // OWNER's world is now, and nowhere else — the same ids in another world are
    // another pane. Same gate as the tabs above: a recreated host owns these panes
    // now, and what it made of them is not this undo's to touch.
    const marks = [...snapshot.terminatedTabPaneIds, ...parkedMarks]
    if (!hostWasRecreated && !relabelled && marks.length > 0) {
      for (const { tabId, paneId, owner } of marks) {
        if (locateWorld(owner).where !== 'screen') continue
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

    // Said only when something WAS left out: a relabelling with no tab closed and no pane marked cost the user nothing.
    return { worldSkipped: relabelled && !hostWasRecreated && (snapshot.closedTabs.length > 0 || marks.length > 0) }
  }
}

/**
 * `deleteHostCascade` behind the app's undo toast. The toast dismisses itself
 * right after running its action (GlobalUndoToast), so an undo that had to leave
 * the worlds alone says so a microtask later, in a toast of its own — without an
 * action: there is nothing left to take back. The texts are the caller's (`t`).
 */
export function deleteHostWithUndoToast(hostId: string, closeTabs: boolean, messages: { deleted: string; worldSkipped: string }): void {
  const undo = deleteHostCascade(hostId, closeTabs)
  useUndoToast.getState().show(messages.deleted, () => {
    if (undo().worldSkipped) queueMicrotask(() => useUndoToast.getState().show(messages.worldSkipped))
  })
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
