// spa/src/lib/host-lifecycle.ts — Cascade delete logic for host removal with undo support
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore, type NormalizedEvent, type AgentStatus } from '../stores/useAgentStore'
import { useExecutionStore, splitExecutionKey } from '../stores/useExecutionStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { releaseLease } from './nex/nex-api'
import { usePeerStore } from '../stores/usePeerStore'
import { useSessionCwdStore } from '../stores/useSessionCwdStore'
import { useUndoToast } from '../stores/useUndoToast'
import { wireIdOfHost } from './profile/host-identity'
import { rewriteHostRefs, scheduleHostReresolve } from './host-reresolve'
import type { Session } from './host-api'

// === Deleting a host affects only this device (host ownership spec §3.4, decision 9) ===
//
// The host list is this device's; the tabs, the host settings and the New Tab columns are the workbench's, and they
// name a host by its WIRE id (`d1_…` of its daemon, else its local id) on every device. So a deletion takes nothing
// from the workbench: every reference to the host — on screen, in every parked world (the master's and each local
// profile's), the `purdex-host-settings` key and the New Tab `sessions:` / `headless:` columns — is rewritten from its
// local id to its wire id, and only THEN is the host removed. The build mapped the local id to that same wire id
// before and passes it through after, so no section but `hosts` changes (pre-H3) and nothing is pushed. The panes
// render "this device has no host" (`MissingHostPane`); no `terminated` mark is written, no tab is closed, nothing
// is pinned (a legacy hostless execution pane keeps resolving to the first host — spec §3.4). What IS cleared is this
// device's own per-host state: sessions, agent state, execution views, caches, runtime.
//
// No lock: the deletion is synchronous and takes none (plan §0.1) — the UI path as ever, and the hosts apply calls it
// under its own grant.

/**
 * Remove a host from this device: its references become its wire id, its device-local state is cleared, the row
 * goes. Returns the undo. A host that does not exist, or the last one (`removeHost` refuses it), is left alone —
 * with a no-op undo. Throws, having changed nothing, when the rewrite cannot be written.
 */
export function deleteHostCascade(hostId: string): () => void {
  const hostStore = useHostStore.getState()
  const sessionStore = useSessionStore.getState()
  const agentStore = useAgentStore.getState()

  // Mirror the veto in `useHostStore.removeHost()` up front: if the host
  // doesn't exist or is the last remaining host (store refuses to delete it),
  // abort the cascade entirely rather than clearing per-host state that
  // won't ever be matched by a real removal.
  const host = hostStore.hosts[hostId]
  if (!host || Object.keys(hostStore.hosts).length <= 1) {
    return () => {}
  }

  // Its wire id — also under an identity conflict, where `identity.toWire` leaves both duplicates out: the sync id
  // of its daemon, which names the survivor once the conflict clears (plan §0.7).
  const wireId = wireIdOfHost(host)
  if (wireId !== hostId) {
    const rewritten = rewriteHostRefs({ [hostId]: wireId })
    if (rewritten !== 'ok') {
      // Nothing else has been touched. A partial rewrite is rolled back by the next pass: the host is still here.
      if (rewritten === 'rollback-failed') scheduleHostReresolve()
      throw new Error(`host ${hostId}: its references could not be rewritten (${rewritten}); nothing was deleted`)
    }
  }

  const prefix = `${hostId}:`
  // --- Snapshot for undo (serializable data only) ---
  const snapshot = {
    host,
    hostOrder: [...hostStore.hostOrder],
    sessions: sessionStore.sessions[hostId] as Session[] | undefined,
    activeHostId: hostStore.activeHostId,
    // AgentStore data (exclude transient activeSubagents)
    agentEvents: {} as Record<string, NormalizedEvent>,
    agentStatuses: {} as Record<string, AgentStatus>,
    agentUnread: {} as Record<string, boolean>,
    agentModels: {} as Record<string, string>,
  }
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

  // A held lease: released best-effort now, while the host row — and its auth — is still here, so other devices are
  // not blocked until it expires (plan §0.8). The execution store is cleared below; a pane's own release would find
  // nothing, and its host would be gone.
  for (const [key, execution] of Object.entries(useExecutionStore.getState().executions)) {
    const { hostId: execHostId, executionId } = splitExecutionKey(key)
    if (execHostId !== hostId || !execution.lease) continue
    try {
      void releaseLease(hostId, executionId, execution.lease.leaseId).catch(() => {})
    } catch {
      // best-effort: a release that throws synchronously does not stop the deletion
    }
  }

  sessionStore.removeHost(hostId)
  agentStore.removeHost(hostId)
  // Nexen execution view state for this host: its panes stay, and render "no host here" from now on.
  useExecutionStore.getState().clearHost(hostId)
  useExecutionListStore.getState().clearHost(hostId)
  useNexHostStore.getState().clearHost(hostId)
  // Peer rows are a cache of a daemon that is no longer configured. Nothing to
  // snapshot: undo restores the host, and the first render that needs its peers
  // fetches them again (`usePeerInfo`'s predicate fires on an absent entry).
  // The cwd readings go the same way and for the same reason — a directory read
  // from one daemon says nothing about another.
  usePeerStore.getState().forgetHost(hostId)
  useSessionCwdStore.getState().forgetHost(hostId)
  hostStore.removeHost(hostId)

  return () => {
    // Guard against host-recreation race: if another code path (import,
    // cross-window BroadcastChannel sync, user re-add) re-created a host
    // with the same id during the undo window, the current entry is a
    // different entity than the one we snapshotted. Skip every restore that
    // would overwrite the user's freshly written state.
    if (useHostStore.getState().hosts[hostId] !== undefined) return

    // --- Restore host + hostOrder position ---
    useHostStore.getState().addHost(snapshot.host)
    useHostStore.getState().reorderHosts(snapshot.hostOrder)
    if (snapshot.activeHostId === hostId) useHostStore.getState().setActiveHost(hostId)

    // --- Restore sessions ---
    if (snapshot.sessions) useSessionStore.getState().replaceHost(hostId, snapshot.sessions)

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
  }
}

/** `deleteHostCascade` behind the app's undo toast; `deleted` is the toast's text (the caller's `t`). */
export function deleteHostWithUndoToast(hostId: string, deleted: string): void {
  const undo = deleteHostCascade(hostId)
  useUndoToast.getState().show(deleted, undo)
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
