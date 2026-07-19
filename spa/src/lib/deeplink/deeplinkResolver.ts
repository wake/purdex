// spa/src/lib/deeplink/deeplinkResolver.ts — resolves a purdex:// execution
// deeplink into a navigation (M0 dispatch, Task P.12). Two landing points
// (spec §9):
//   ① the execution's session tab is already open → focus it (observe-only);
//   ② otherwise → open the read-only execution detail page (never dead-ends).
//
// Observe-only invariant: neither branch ever opens a live interactive session
// view or attaches a stdin write path (SubscriberToRelay). Branch ① only
// *focuses* a tab the user already opened; branch ② is a strictly read-only
// page. This side-steps the multi-writer stdin race (spec §9, P.7) without a
// full controller/observer arbiter (deferred to M1).
import { findTabBySessionCode } from '../pane-tree'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { fetchExecutionView, resolveExecutionHostId, type ExecutionView } from '../execution-api'

/** The deeplink payload broadcast by the electron main process (P.11 contract). */
export interface DeeplinkPayload {
  executionId: string
  host?: string
}

/**
 * Observe-only focus of an ALREADY-OPEN session tab. Returns true when a tab
 * matching sessionCode existed and was activated. Never creates a tab and never
 * wires stdin — it only activates a view the user already opened, so the
 * observe-only guarantee holds.
 */
export function focusExistingSessionTab(sessionCode: string): boolean {
  const tabs = useTabStore.getState().tabs
  const tabId = findTabBySessionCode(tabs, sessionCode)
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
 * Open (or focus, if already open) the read-only execution detail tab — the
 * fallback landing. Singleton per execution id, so repeated deeplinks reuse the
 * same tab.
 */
export function openExecutionDetailTab(executionId: string, host?: string): void {
  useTabStore.getState().openSingletonTab({ kind: 'execution', executionId, host })
  window.electronAPI?.focusMyWindow?.()
}

/** Injectable seams so resolveDeeplink is testable without stores or network. */
export interface ResolveDeeplinkDeps {
  resolveHostId: (host?: string) => string
  fetchExecution: (hostId: string, executionId: string) => Promise<ExecutionView | null>
  focusSession: (sessionCode: string) => boolean
  openDetail: (executionId: string, host?: string) => void
}

const defaultDeps: ResolveDeeplinkDeps = {
  resolveHostId: resolveExecutionHostId,
  fetchExecution: fetchExecutionView,
  focusSession: focusExistingSessionTab,
  openDetail: openExecutionDetailTab,
}

/**
 * Resolve one deeplink to a landing. Fetches the projection to learn the live
 * session_code, then picks branch ① (focus open session) or ② (detail page). A
 * fetch/daemon error falls through to the detail page so a deeplink never
 * dead-ends on a blank view.
 */
export async function resolveDeeplink(
  payload: DeeplinkPayload,
  deps: ResolveDeeplinkDeps = defaultDeps,
): Promise<void> {
  const { executionId, host } = payload
  if (!executionId) return

  const hostId = deps.resolveHostId(host)
  let view: ExecutionView | null = null
  try {
    view = await deps.fetchExecution(hostId, executionId)
  } catch {
    // Network/daemon error → fall through to the detail page (never dead-ends).
  }

  // ① Live session already has an open tab → focus it (observe-only).
  if (view?.session_code && deps.focusSession(view.session_code)) return

  // ② Fallback → read-only execution detail page.
  deps.openDetail(executionId, host)
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
