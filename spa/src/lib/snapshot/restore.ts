import { createSession, listSessions } from '../host-api'
import type { Session } from '../host-api'
import { scanPaneTree, updatePaneInLayout } from '../pane-tree'
import type { PaneContent, PaneLayout, Tab } from '../../types/tab'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import type { EnsureReport, Remap, RemapEntry, SessionMeta, WorkspaceSnapshot } from './types'

/**
 * Reconcile persisted per-host session metadata against each host's live
 * session list, reattaching survivors and (optionally) rebuilding restorable
 * dead sessions.
 *
 * Contract highlights (see plan §8.1):
 * - Exactly one `listSessions` per host. If it throws (host offline), every
 *   entry for that host is `failed` and `createSession` is never called there.
 * - Rebuilt entries ALWAYS trust the returned Session object for `newCode` /
 *   `session` — the daemon may auto-rename or assign a different code.
 * - Per-session failure isolation: a single `createSession` rejection marks
 *   only that entry `failed` and never aborts the rest.
 * - `remap` is nested by hostId then oldCode, so identical code values under
 *   different hosts never collide.
 */
export async function ensureSessions(
  sessionMeta: Record<string, Record<string, SessionMeta>>,
  opts?: { rebuild?: boolean },
): Promise<{ remap: Remap; report: EnsureReport }> {
  const rebuild = opts?.rebuild !== false
  const remap: Remap = {}
  const report: EnsureReport = { reattached: 0, rebuilt: 0, failed: 0 }

  for (const [hostId, perHost] of Object.entries(sessionMeta)) {
    const perHostRemap: Record<string, RemapEntry> = {}
    remap[hostId] = perHostRemap

    let live: Session[] | null
    try {
      live = await listSessions(hostId)
    } catch {
      live = null // host offline — every entry fails, no createSession
    }

    for (const [oldCode, meta] of Object.entries(perHost)) {
      if (live === null) {
        perHostRemap[oldCode] = { status: 'failed' }
        report.failed++
        continue
      }

      const alive = live.find((s) => s.code === oldCode)
      if (alive) {
        perHostRemap[oldCode] = { status: 'reattached', newCode: oldCode, session: alive }
        report.reattached++
        continue
      }

      if (!rebuild || !meta.restorable || !meta.cwd) {
        perHostRemap[oldCode] = { status: 'failed' }
        report.failed++
        continue
      }

      try {
        const created = await createSession(hostId, meta.name, meta.cwd, meta.mode)
        // §8.1: trust the returned object, never the request values.
        perHostRemap[oldCode] = { status: 'rebuilt', newCode: created.code, session: created }
        report.rebuilt++
      } catch {
        perHostRemap[oldCode] = { status: 'failed' }
        report.failed++
      }
    }
  }

  return { remap, report }
}

/**
 * Rewrite every `tmux-session` pane in a layout tree against a {@link Remap}
 * produced by {@link ensureSessions}, returning a NEW layout (input untouched).
 *
 * Per-pane behaviour (keyed by the composite `[hostId][sessionCode]`):
 * - `reattached` / `rebuilt` → adopt `entry.newCode`, refresh `cachedName` from
 *   `entry.session.name`, and clear any `terminated` marker (the session is now
 *   attachable again).
 * - `failed` → keep the pane's code but mark `terminated: 'tmux-restarted'`.
 *   The reason is FIXED for the restore path (codex plan-review): restore never
 *   guesses `'session-closed'` / `'host-removed'`.
 * - no matching entry → pane left exactly as-is.
 *
 * `opts.onlyTerminated` guards the "rebuild all sessions" action (spec §3.5):
 * when true, only panes that ALREADY carry a `terminated` marker are touched;
 * live panes are never rewritten even if their key matches a remap entry.
 */
export function remapLayoutSessions(
  layout: PaneLayout,
  remap: Remap,
  opts?: { onlyTerminated?: boolean },
): PaneLayout {
  const onlyTerminated = opts?.onlyTerminated === true

  // Collect the intended content updates first (scan does not mutate), then fold
  // them into fresh layouts via updatePaneInLayout so the input is never touched.
  const updates: Array<{ paneId: string; content: PaneContent }> = []

  scanPaneTree(layout, (pane) => {
    const content = pane.content
    if (content.kind !== 'tmux-session') return
    if (onlyTerminated && content.terminated === undefined) return

    const entry = remap[content.hostId]?.[content.sessionCode]
    if (!entry) return

    if (entry.status === 'failed') {
      updates.push({
        paneId: pane.id,
        content: { ...content, terminated: 'tmux-restarted' },
      })
      return
    }

    // reattached | rebuilt — adopt new code/name, clear terminated marker.
    const { terminated: _cleared, ...rest } = content
    void _cleared
    updates.push({
      paneId: pane.id,
      content: { ...rest, sessionCode: entry.newCode, cachedName: entry.session.name },
    })
  })

  let result = layout
  for (const { paneId, content } of updates) {
    result = updatePaneInLayout(result, paneId, content)
  }
  return result
}

/**
 * Structural navigation-integrity guard for a {@link WorkspaceSnapshot}, run
 * before restore adopts a snapshot. Validates ONLY tab/workspace navigation
 * references — the ids the UI dereferences to render the tab bar and workspace
 * switcher. Returns on the FIRST failing check with a short human-readable
 * reason.
 *
 * Five checks (all must pass for `ok:true`):
 * 1. Every id in each `workspace.tabs` exists as a key in `snap.tabs`.
 * 2. `snap.activeTabId` is `null` or a key in `snap.tabs`.
 * 3. Each `workspace.activeTabId` is `null` or belongs to THAT workspace's `tabs`.
 * 4. `snap.activeWorkspaceId` is `null` or the id of some workspace.
 * 5. Every id in `snap.tabOrder` exists as a key in `snap.tabs`.
 *
 * Deliberately NOT validated (scope decision R3 C): pane-content semantic
 * references such as `settings.scope.workspaceId`. Those are content, not
 * navigation, and a dangling one must never block a restore.
 */
export function validateSnapshotConsistency(
  snap: WorkspaceSnapshot,
): { ok: true } | { ok: false; reason: string } {
  const tabExists = (id: string): boolean =>
    Object.prototype.hasOwnProperty.call(snap.tabs, id)

  // 1. Every workspace tab id must resolve to a real tab.
  for (const ws of snap.workspaces) {
    for (const tabId of ws.tabs) {
      if (!tabExists(tabId)) {
        return { ok: false, reason: `workspace "${ws.id}" references unknown tab "${tabId}"` }
      }
    }
  }

  // 2. activeTabId (nullable) must resolve to a real tab.
  if (snap.activeTabId !== null && !tabExists(snap.activeTabId)) {
    return { ok: false, reason: `activeTabId "${snap.activeTabId}" is not a known tab` }
  }

  // 3. Each workspace.activeTabId (nullable) must belong to that workspace.
  for (const ws of snap.workspaces) {
    if (ws.activeTabId !== null && !ws.tabs.includes(ws.activeTabId)) {
      return {
        ok: false,
        reason: `workspace "${ws.id}" activeTabId "${ws.activeTabId}" is not in its tabs`,
      }
    }
  }

  // 4. activeWorkspaceId (nullable) must name an existing workspace.
  if (
    snap.activeWorkspaceId !== null &&
    !snap.workspaces.some((ws) => ws.id === snap.activeWorkspaceId)
  ) {
    return {
      ok: false,
      reason: `activeWorkspaceId "${snap.activeWorkspaceId}" is not a known workspace`,
    }
  }

  // 5. Every tabOrder id must resolve to a real tab.
  for (const tabId of snap.tabOrder) {
    if (!tabExists(tabId)) {
      return { ok: false, reason: `tabOrder references unknown tab "${tabId}"` }
    }
  }

  return { ok: true }
}

/**
 * Replace ONLY the tab store's navigation state (does NOT touch the workspace
 * store). `visitHistory` is NOT persisted, so it is not part of the snapshot;
 * instead the CURRENT history is filtered to the ids that still exist in the
 * new `tabOrder` (a subset of the new tab set), so closing the active tab
 * afterwards still resolves to a live tab rather than a ghost.
 */
export function replaceTabState(
  tabs: Record<string, Tab>,
  tabOrder: string[],
  activeTabId: string | null,
): void {
  const order = new Set(tabOrder)
  const visitHistory = useTabStore.getState().visitHistory.filter((id) => order.has(id))
  useTabStore.setState({ tabs, tabOrder, activeTabId, visitHistory })
}

/**
 * Adopt a {@link WorkspaceSnapshot} into BOTH the tab store and the workspace
 * store with best-effort atomicity.
 *
 * Sequence:
 * 1. `validateSnapshotConsistency` — on failure THROW before touching any
 *    store (navigation refs must be sound before the UI dereferences them).
 * 2. Snapshot the OLD values of both stores for rollback.
 * 3. Replace the tab store, THEN the workspace store.
 * 4. If ANY setState throws, roll BOTH stores back to the captured old values
 *    and rethrow — never leave a half-applied state. If the FIRST (tab)
 *    setState throws, the workspace store was never given new values, so its
 *    rollback is a no-op restoring the untouched old world.
 *
 * `visitHistory` is filtered against `snap.tabOrder` exactly as in
 * {@link replaceTabState} (it is not carried in the snapshot).
 */
export function replaceTabSnapshot(snap: WorkspaceSnapshot): void {
  const result = validateSnapshotConsistency(snap)
  if (!result.ok) {
    throw new Error(`snapshot consistency check failed: ${result.reason}`)
  }

  const tabState = useTabStore.getState()
  const oldTab = {
    tabs: tabState.tabs,
    tabOrder: tabState.tabOrder,
    activeTabId: tabState.activeTabId,
    visitHistory: tabState.visitHistory,
  }
  const wsState = useWorkspaceStore.getState()
  const oldWs = {
    workspaces: wsState.workspaces,
    activeWorkspaceId: wsState.activeWorkspaceId,
  }

  const order = new Set(snap.tabOrder)
  const visitHistory = tabState.visitHistory.filter((id) => order.has(id))

  try {
    useTabStore.setState({
      tabs: snap.tabs,
      tabOrder: snap.tabOrder,
      activeTabId: snap.activeTabId,
      visitHistory,
    })
    useWorkspaceStore.setState({
      workspaces: snap.workspaces,
      activeWorkspaceId: snap.activeWorkspaceId,
    })
  } catch (err) {
    // Roll BOTH stores back. If the tab setState threw, this restores it; the
    // workspace rollback restores the (untouched) old world either way.
    useTabStore.setState(oldTab)
    useWorkspaceStore.setState(oldWs)
    throw err
  }
}
