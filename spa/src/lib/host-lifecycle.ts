// spa/src/lib/host-lifecycle.ts — Cascade delete logic for host removal with undo support
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useTabStore } from '../stores/useTabStore'
import { useLocalProfilesStore } from '../stores/useLocalProfilesStore'
import { useHostSettingsStore } from '../stores/useHostSettingsStore'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useAgentStore, type NormalizedEvent, type AgentStatus } from '../stores/useAgentStore'
import { useExecutionStore, splitExecutionKey } from '../stores/useExecutionStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { pinnedLeaseRelease } from './nex/nex-api'
import { usePeerStore } from '../stores/usePeerStore'
import { useSessionCwdStore } from '../stores/useSessionCwdStore'
import { useUndoToast } from '../stores/useUndoToast'
import { wireIdOfHost } from './profile/host-identity'
import { reresolveRestoredHost, rewriteHostRefs, scheduleHostReresolve } from './host-reresolve'
import { useRebuildStore, type OperationLockGrant } from '../stores/useRebuildStore'
import type { Session } from './host-api'

// === Deleting a host affects only this device (host ownership spec §3.4, decision 9) ===
//
// The host list is this device's; the tabs, the host settings and the New Tab columns are the workbench's, and they
// name a host by its WIRE id (`d1_…` of its daemon, else its local id) on every device. So a deletion takes nothing
// from the workbench: every reference to the host — on screen, in every parked world (the master's and each local
// profile's), the `purdex-host-settings` key and the New Tab `sessions:` / `headless:` columns — is rewritten from its
// local id to its wire id, and only THEN is the host removed. The build mapped the local id to that same wire id
// before and passes it through after, so no synced section changes and nothing is pushed. The panes
// render "this device has no host" (`MissingHostPane`); no `terminated` mark is written, no tab is closed, nothing
// is pinned (a legacy hostless execution pane keeps resolving to the first host — spec §3.4). What IS cleared is this
// device's own per-host state: sessions, agent state, execution views, caches, runtime.
//
// The lock: the deletion rewrites the tab tree, so it runs under the operation lock like everything that does — the
// cascade itself takes none; its callers hold it: the Hosts page's `deleteHostWithUndoToast` (owner `host-delete`,
// retried while busy — PR #1413 review).

/**
 * Remove a host from this device: its references become its wire id, its device-local state is cleared, the row
 * goes. Returns the undo. A host that does not exist, or the last one (`removeHost` refuses it), is left alone —
 * with a no-op undo. Throws, having changed nothing, when the rewrite cannot be written.
 *
 * `grant` and `afterCommit` are for a caller that deletes hosts inside its OWN transaction and may roll it back —
 * today none: the Hosts page passes neither. They were the `hosts` apply's, which host ownership H3 removed; they
 * are kept (H3 plan D2) for the replace-all receive of host transfer (spec §6.4 step 6, not built — #1395).
 *
 * `grant` — the operation-lock grant that caller holds, which the UNDO runs its re-resolve under: a caller that
 * rolls back through the undo with nothing awaited needs the undo to finish then and there.
 *
 * `afterCommit` — what may happen only once the caller's whole transaction has committed: the release of each lease
 * this device holds on the host, a daemon side effect no store rollback can take back (plan §0.8). Given, those
 * actions are pushed there for the caller to run after its commit, or to drop on its rollback; not given (the Hosts
 * page), they run as soon as this deletion has committed.
 */
export function deleteHostCascade(hostId: string, grant: OperationLockGrant | null = null, afterCommit?: Array<() => void>): () => void {
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

  const prefix = `${hostId}:`
  // --- Snapshot for undo (serializable data only) ---
  const snapshot: UndoSnapshot = {
    host,
    hostOrder: [...hostStore.hostOrder],
    sessions: sessionStore.sessions[hostId],
    activeHostId: hostStore.activeHostId,
    devHostId: hostStore.devHostId,
    // AgentStore data (exclude transient activeSubagents)
    agentEvents: {},
    agentStatuses: {},
    agentUnread: {},
    agentModels: {},
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

  // ONE UNIT: every store the cascade writes, as it is now. A step that throws — a store action, or a persist's
  // `setItem` (quota, SecurityError) after zustand already changed memory, `removeHost`'s own included — puts every
  // one of them back, newest first, and the cascade throws: the caller gets no undo, because nothing happened.
  const releases = leaseReleasesOf(hostId)
  const before = CASCADE_STORES.map((store) => store.getState())
  try {
    cascadeSteps(hostId, wireId)
  } catch (err) {
    const unfinished: string[] = []
    for (let i = CASCADE_STORES.length - 1; i >= 0; i--) {
      if (CASCADE_STORES[i].getState() === before[i]) continue
      try {
        CASCADE_STORES[i].setState(before[i] as never, true)
      } catch (undoErr) {
        unfinished.push(messageOf(undoErr))
      }
    }
    if (unfinished.length === 0) throw err
    console.error(`[host-lifecycle] deleting host ${hostId} failed, and putting it back failed too: ${unfinished.join('; ')}`)
    throw new HostDeleteRollbackIncompleteError(`${messageOf(err)} (rollback incomplete — ${unfinished.join('; ')})`, { cause: err })
  }

  if (afterCommit) afterCommit.push(...releases)
  else for (const release of releases) release()
  return makeUndo(hostId, wireId, snapshot, grant)
}

/**
 * One action per lease this device holds on `hostId`: release it at the daemon, best-effort — to the endpoint and
 * auth pinned NOW, while the host is still configured (it is gone by the time these run). Sends nothing itself.
 */
function leaseReleasesOf(hostId: string): Array<() => void> {
  const held: Array<[executionId: string, leaseId: string]> = []
  for (const [key, execution] of Object.entries(useExecutionStore.getState().executions)) {
    const { hostId: execHostId, executionId } = splitExecutionKey(key)
    if (execHostId === hostId && execution.lease) held.push([executionId, execution.lease.leaseId])
  }
  if (held.length === 0) return []
  const release = pinnedLeaseRelease(hostId)
  if (release === null) return []
  return held.map(([executionId, leaseId]) => () => {
    try {
      void release(executionId, leaseId).catch(() => {})
    } catch {
      // best-effort: a release that throws synchronously stops nothing
    }
  })
}

/**
 * A deletion failed AND putting the stores back failed too: this device may hold part of the deletion (memory and
 * storage possibly apart). The UI says so until dismissed — reload, check the host settings.
 */
export class HostDeleteRollbackIncompleteError extends Error {
  override name = 'HostDeleteRollbackIncompleteError'
}

/** An error's message — a `DOMException` (a persist's quota error) included, which is not always an `Error` here. */
const messageOf = (err: unknown): string => {
  const message = (err as { message?: unknown } | null)?.message
  return typeof message === 'string' ? message : String(err)
}

/** A store as far as the cascade's all-or-nothing needs it: its whole state, read and put back. */
interface WholeStore {
  getState: () => unknown
  setState: (state: never, replace: true) => void
}

/** Every store `cascadeSteps` writes: those `rewriteHostRefs` rewrites, each per-host store it clears, the host store. */
const CASCADE_STORES: readonly WholeStore[] = [
  useTabStore,
  useLocalProfilesStore,
  useHostSettingsStore,
  useNewTabLayoutStore,
  useSessionStore,
  useAgentStore,
  useExecutionStore,
  useExecutionListStore,
  useNexHostStore,
  usePeerStore,
  useSessionCwdStore,
  useHostStore,
] as unknown as WholeStore[]

/** The cascade's writes, in order. Throws at the first that fails — `deleteHostCascade` puts everything back. */
function cascadeSteps(hostId: string, wireId: string): void {
  if (wireId !== hostId) {
    const rewritten = rewriteHostRefs({ [hostId]: wireId })
    if (rewritten !== 'ok') throw new Error(`host ${hostId}: its references could not be rewritten (${rewritten})`)
  }

  // A held lease is NOT released here: that is a daemon side effect, sent only once the deletion has committed
  // (`leaseReleasesOf`, `afterCommit`). The execution store is cleared below; a pane's own release would find nothing.
  useSessionStore.getState().removeHost(hostId)
  useAgentStore.getState().removeHost(hostId)
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
  useHostStore.getState().removeHost(hostId)
}

interface UndoSnapshot {
  host: HostConfig
  hostOrder: string[]
  sessions: Session[] | undefined
  activeHostId: string | null
  devHostId: string | null
  agentEvents: Record<string, NormalizedEvent>
  agentStatuses: Record<string, AgentStatus>
  agentUnread: Record<string, boolean>
  agentModels: Record<string, string>
}

function makeUndo(hostId: string, wireId: string, snapshot: UndoSnapshot, grant: OperationLockGrant | null): () => void {
  return () => {
    // Guard against host-recreation race: if another code path (import,
    // cross-window BroadcastChannel sync, user re-add) re-created a host
    // with the same id during the undo window, the current entry is a
    // different entity than the one we snapshotted. Skip every restore that
    // would overwrite the user's freshly written state.
    if (useHostStore.getState().hosts[hostId] !== undefined) return

    // --- Restore the host row, verbatim, at its place ---
    // Not `addHost`: it strips `daemonId` and `syncAliases` (#1396), and without them the host's wire id — and so
    // every reference the deletion rewrote to it — would not resolve back to it.
    useHostStore.setState((s) => restoredHostSlice(s, hostId, snapshot))

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

    // --- References back on the local id (plan §0.6) ---
    // The deletion's reverse: the re-resolve pass's body for this host, now — under the caller's grant, else the
    // lock taken here. Refused (a rebuild holds the lock, the world is mid-switch) or failed: the pass is scheduled,
    // and retries until it lands; the host is already back, so it finds every reference to resolve. A host whose wire
    // id is its local id had nothing rewritten, and nothing names it any other way: no pass.
    //   The fallback is the WHOLE pass, not one scoped to this host, on purpose (PR #1413 review): resolving every
    // resolvable reference is the steady state — any trigger (a host added, a hydration, an apply settling) does the
    // same — so it moves nothing that would not move anyway. And the scope exists only for a transactional caller's
    // rollback from a STAGED host list (the `hosts` apply's until H3; a replace-all's, #1395); the scheduled pass needs
    // the operation lock, which that caller holds until its rollback is over, so it can only run once the list is final.
    if (wireId !== hostId && reresolveRestoredHost(hostId, grant) !== 'done') scheduleHostReresolve()
  }
}

type HostSlice = Pick<ReturnType<typeof useHostStore.getState>, 'hosts' | 'hostOrder' | 'activeHostId' | 'devHostId'>

/**
 * The host store with `hostId`'s row back as it was: right after the hosts it followed that are still here (a host
 * added meanwhile keeps its place), every row's `order` renumbered as `reorderHosts` does, and focus / dev host back
 * on it when it had them.
 */
function restoredHostSlice(
  s: HostSlice,
  hostId: string,
  snap: { host: HostConfig; hostOrder: readonly string[]; activeHostId: string | null; devHostId: string | null },
): Partial<HostSlice> {
  const followed = snap.hostOrder.slice(0, snap.hostOrder.indexOf(hostId)).filter((id) => Object.hasOwn(s.hosts, id))
  const order = s.hostOrder.filter((id) => id !== hostId)
  const at = followed.length === 0 ? 0 : order.indexOf(followed[followed.length - 1]) + 1
  order.splice(at, 0, hostId)
  const hosts: Record<string, HostConfig> = { ...s.hosts, [hostId]: snap.host }
  order.forEach((id, i) => {
    if (Object.hasOwn(hosts, id) && hosts[id].order !== i) hosts[id] = { ...hosts[id], order: i }
  })
  return {
    hosts,
    hostOrder: order,
    ...(snap.activeHostId === hostId ? { activeHostId: hostId } : {}),
    ...(snap.devHostId === hostId ? { devHostId: hostId } : {}),
  }
}

export const HOST_DELETE_LOCK_OWNER = 'host-delete'
/** While the operation lock is held elsewhere, the Hosts page's deletion is retried this often… */
export const HOST_DELETE_BUSY_RETRY_MS = 250
/** …for this long from the click, then given up (as the profile switcher does with `busy`). */
export const HOST_DELETE_BUSY_TOTAL_MS = 4_000

/** What `deleteHostWithUndoToast` did: deleted (undo toast up); `busy` — the lock stayed held elsewhere; `stale` —
 *  the host confirmed is not the one there any more (gone, gone and back, re-pointed) or cannot be deleted. */
export type HostDeleteOutcome = 'deleted' | 'busy' | 'stale'

/**
 * `deleteHostCascade` behind the app's undo toast — the Hosts page's deletion. It rewrites the tab tree, so it runs
 * holding the operation lock (owner `host-delete`), as everything that rewrites the tab tree does: a rebuild in flight
 * never sees its pane moved to a wire id under it. Held elsewhere, the deletion is retried every
 * `HOST_DELETE_BUSY_RETRY_MS` up to `HOST_DELETE_BUSY_TOTAL_MS` after the click, then left undone and `messages.busy`
 * said. With the lock free it happens at once, synchronously.
 *
 * It deletes the host the user CONFIRMED: its endpoint identity (`hostIdentity` — ip, port, token — and its
 * daemonId) is fixed at the click and watched until the lock is taken. The row gone — even gone and back under the
 * same id, as another window's undo re-adds it — or re-pointed meanwhile: nothing is deleted and `messages.stale`
 * said, never "deleted" with an Undo. A rename is not another host. The texts are the caller's (`t`). Rejects when
 * the deletion itself fails (nothing was deleted then — the cascade put everything back).
 */
export function deleteHostWithUndoToast(hostId: string, messages: { deleted: string; busy: string; stale: string }): Promise<HostDeleteOutcome> {
  const startedAt = Date.now()
  const confirmed = useHostStore.getState().hosts[hostId]
  if (confirmed === undefined) {
    useUndoToast.getState().show(messages.stale)
    return Promise.resolve('stale')
  }
  const target = targetIdentity(confirmed)
  let stale = false
  const unwatch = useHostStore.subscribe((state) => {
    const row = state.hosts[hostId]
    if (row === undefined || targetIdentity(row) !== target) stale = true
  })
  return new Promise<HostDeleteOutcome>((resolve, reject) => {
    const settle = (outcome: HostDeleteOutcome, message: string, undo?: () => void): void => {
      unwatch()
      useUndoToast.getState().show(message, undo)
      resolve(outcome)
    }
    const attempt = (): void => {
      if (stale) return settle('stale', messages.stale)
      const grant = useRebuildStore.getState().acquireOperationLock(HOST_DELETE_LOCK_OWNER)
      if (grant === null) {
        if (Date.now() - startedAt < HOST_DELETE_BUSY_TOTAL_MS) setTimeout(attempt, HOST_DELETE_BUSY_RETRY_MS)
        else settle('busy', messages.busy)
        return
      }
      unwatch() // from here the host store is this deletion's own write
      let undo: () => void
      try {
        // No grant for the undo: it runs later, outside this lock, and takes the lock itself then.
        undo = deleteHostCascade(hostId)
      } catch (err) {
        reject(err)
        return
      } finally {
        useRebuildStore.getState().releaseOperationLock(grant)
      }
      // The cascade refuses the last host (a no-op): nothing was deleted, so nothing to undo.
      if (useHostStore.getState().hosts[hostId] !== undefined) return settle('stale', messages.stale)
      settle('deleted', messages.deleted, undo)
    }
    attempt()
  })
}

/** What makes a host row the one the user confirmed: its endpoint identity and its daemon. Not its name or look. */
function targetIdentity(h: HostConfig): string {
  return JSON.stringify([hostIdentity(h), h.daemonId ?? null])
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
