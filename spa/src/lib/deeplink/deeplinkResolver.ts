// spa/src/lib/deeplink/deeplinkResolver.ts — resolves a purdex:// execution
// deeplink into a navigation (P-B). It opens the execution pane; a Nexen
// execution has no tmux session, so there is nothing else to focus. Keyed by
// (host, executionId) so the same id on two hosts opens two panes.
import { useTabStore } from '../../stores/useTabStore'
import { resolveExecutionHostId } from '../nex/resolve-host'
import { landOnHostsPageIfHidden } from '../shown-hosts'

/** The deeplink payload broadcast by the electron main process (P.11 contract). */
export interface DeeplinkPayload {
  executionId: string
  host?: string
}

/**
 * Open (or focus, if already open) the execution pane. Singleton per
 * (host, executionId), so repeated deeplinks reuse the same tab. A host not
 * shown in this workbench (hidden, or a ref neither local nor listed — host
 * ownership H2d-3, the one opener rule) lands on the Hosts page instead: no tab.
 */
export function openExecutionDetailTab(executionId: string, host: string): void {
  if (!landOnHostsPageIfHidden(host)) {
    useTabStore.getState().openSingletonTab({ kind: 'execution', executionId, host })
  }
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
 * Resolve one deeplink to a landing: the execution pane, keyed by the
 * resolved host. An empty executionId is ignored.
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
