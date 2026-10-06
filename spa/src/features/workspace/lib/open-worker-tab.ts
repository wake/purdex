// spa/src/features/workspace/lib/open-worker-tab.ts — opening a worker (shell cleanup spec §4.4, rule B.2).
//
// A tab that already shows this worker is selected where it is, and its workspace comes on screen with it: it is
// NOT moved. Otherwise the new tab goes into the workspace on screen (or `insertTab`'s fallback when none is),
// right now — not into `Unsorted` when standalone adoption (adopt-standalone.ts) finds it ownerless later.
//
// `insertTab` is never called for a tab that already has a workspace: its singleton dedup would MOVE that tab
// out of its own workspace into the target (../store.ts, `insertTab`).
//
// `focusTabInWorkspace` is that workspace half on its own, for openers that find or make their tab themselves
// (the conversation rebuild tab, lib/nex/open-conversation-rebuild.ts).
import type { PaneContent } from '../../../types/tab'
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../store'

export function openWorkerTab(content: Extract<PaneContent, { kind: 'execution' }>): string {
  // Finds the leaf showing this worker in any tab, or makes a standalone tab; either way it becomes the active tab.
  const tabId = useTabStore.getState().openSingletonTab(content)
  focusTabInWorkspace(tabId)
  return tabId
}

/**
 * Bring `tabId` on screen: it becomes the active tab, and its workspace the active workspace. A tab with a workspace
 * stays where it is; one without goes into the workspace on screen (or `insertTab`'s fallback when none is).
 */
export function focusTabInWorkspace(tabId: string): void {
  useTabStore.getState().setActiveTab(tabId)
  const ws = useWorkspaceStore.getState()
  let owner = ws.findWorkspaceByTab(tabId)
  if (!owner) {
    ws.insertTab(tabId)
    owner = useWorkspaceStore.getState().findWorkspaceByTab(tabId)
  }
  // The same calls as selecting a tab in the tab bar (`handleSelectTab`, ../hooks.ts).
  if (owner) {
    ws.setActiveWorkspace(owner.id)
    ws.setWorkspaceActiveTab(owner.id, tabId)
  }
}
