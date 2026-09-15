// spa/src/lib/deeplink/deeplinkResolver.ts — resolves a purdex:// execution
// deeplink into a navigation (P-B). A Nexen execution has no tmux session, so
// the only landing is the read-only execution detail pane, keyed by
// (host, executionId) so the same id on two hosts opens two panes.
//
// Observe-only invariant: this never opens a live interactive session view or
// attaches a stdin write path. The detail page it opens is strictly read-only.
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { findTabBySessionCode } from '../pane-tree'
import { resolveExecutionHostId } from '../nex/resolve-host'

/** The deeplink payload broadcast by the electron main process (P.11 contract). */
export interface DeeplinkPayload {
  executionId: string
  host?: string
}

/**
 * Observe-only focus of an ALREADY-OPEN session tab. Returns true when a tab
 * matching hostId + sessionCode existed and was activated. Never creates a tab
 * and never wires stdin — it only activates a view the user already opened, so
 * the observe-only guarantee holds.
 *
 * Kept for ExecutionDetailPage's "focus open session" affordance; no longer
 * called from the deeplink resolution path itself (a Nexen execution has no
 * tmux session to focus).
 */
export function focusExistingSessionTab(hostId: string, sessionCode: string): boolean {
  const tabs = useTabStore.getState().tabs
  const tabId = findTabBySessionCode(tabs, hostId, sessionCode)
  if (!tabId) return false
  useTabStore.getState().setActiveTab(tabId)
  const ws = useWorkspaceStore.getState().findWorkspaceByTab(tabId)
  if (ws) {
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)
    useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tabId)
  } else {
    useWorkspaceStore.getState().setActiveWorkspace(null)
  }
  window.electronAPI?.focusMyWindow?.()
  return true
}

/**
 * Open (or focus, if already open) the read-only execution detail tab.
 * Singleton per (host, executionId), so repeated deeplinks reuse the same tab.
 */
export function openExecutionDetailTab(executionId: string, host: string): void {
  useTabStore.getState().openSingletonTab({ kind: 'execution', executionId, host })
  window.electronAPI?.focusMyWindow?.()
}

/** Injectable seams so resolveDeeplink is testable without stores or network. */
export interface ResolveDeeplinkDeps {
  resolveHostId: (host?: string) => string
  openDetail: (executionId: string, host: string) => void
}

const defaultDeps: ResolveDeeplinkDeps = {
  resolveHostId: resolveExecutionHostId,
  openDetail: openExecutionDetailTab,
}

/**
 * Resolve one deeplink to a landing: the read-only execution detail pane,
 * keyed by the resolved host. An empty executionId is ignored.
 */
export async function resolveDeeplink(
  payload: DeeplinkPayload,
  deps: ResolveDeeplinkDeps = defaultDeps,
): Promise<void> {
  const { executionId, host } = payload
  if (!executionId) return
  deps.openDetail(executionId, deps.resolveHostId(host))
}

/**
 * Subscribe to deeplink broadcasts and resolve each one. Returns an unsubscribe
 * cleanup (no-op when not running under electron).
 *
 * MUST be registered early in app mount — the electron main process buffers a
 * cold-start deeplink and flushes it on the first `spa:ready`, so a listener
 * attached after `signalReady()` would miss it. App calls this before
 * useElectronIpc (which sends `spa:ready`).
 */
export function registerDeeplinkResolver(deps: ResolveDeeplinkDeps = defaultDeps): () => void {
  const api = window.electronAPI
  if (!api?.onDeeplinkNavigate) return () => {}
  return api.onDeeplinkNavigate((payload) => {
    void resolveDeeplink(payload, deps)
  })
}
