// spa/src/lib/open-session-tab.ts — the two ways a tmux session gets onto the screen, shared by every caller so they
// cannot drift (lead-team plan v3 P9b-1):
//   openSessionTab  — Hosts › Sessions "open" (moved from SessionsSection's `handleOpen`): a NEW tab on the session;
//   activateTabPane — the notification click's existing-tab block (moved from useNotificationDispatcher): show a tab
//                     that already holds the session and move the keyboard to its pane.
// The approval dialog's switch to the requester (U22, lib/team/approval-goto.ts) uses both.
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { usePaneFocusStore } from '../stores/usePaneFocusStore'
import { isRefShownNow } from './shown-hosts'
import type { Session } from './host-api'

/**
 * Open a new tab attached to `session` on `hostId`, insert it into a workspace (the active one, else the first, else
 * Unsorted) and activate it. `tmux-session` is never a singleton (`lib/pane-utils.ts`), so a second call adds a second
 * tab. A host hidden in this workbench opens nothing (plan H2d-2, §0.21 user rules 1 / 5) → `null`.
 */
export function openSessionTab(hostId: string, session: Session): string | null {
  if (!isRefShownNow(hostId)) return null
  const tabId = useTabStore.getState().openSingletonTab({
    kind: 'tmux-session',
    hostId,
    sessionCode: session.code,
    mode: 'terminal',
    cachedName: session.name,
    // Generation from the session payload we are opening, never from
    // ambient host state (spec §4.5).
    tmuxInstance: session.tmux_instance ?? '',
  })
  useWorkspaceStore.getState().insertTab(tabId)
  useTabStore.getState().setActiveTab(tabId)
  return tabId
}

/**
 * Show `tabId` and record it as its workspace's active tab — the workspace on screen is not switched (the keyboard
 * shortcuts only move among the tabs the screen already shows; `activateTabPane` is the one that crosses workspaces).
 */
export function activateTab(tabId: string): void {
  useTabStore.getState().setActiveTab(tabId)
  const ws = useWorkspaceStore.getState().findWorkspaceByTab(tabId)
  if (ws && ws.activeTabId !== tabId) useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tabId)
}

/** Show `tabId` with `paneId` as the pane that takes the keyboard, and the tab's workspace. */
export function activateTabPane(tabId: string, paneId: string): void {
  // The pane becomes its tab's most recently focused pane (usePaneFocusStore, rule F) and is asked to take focus,
  // both before the tab is shown. A tab already on screen has no activation, so the one-shot request is what
  // moves the keyboard there (#1840 A1) — the user must not type the reply into the pane they were in. A tab
  // being shown serves the request with its activation's focus (useActivationFocus), so it focuses once.
  usePaneFocusStore.getState().requestFocus(tabId, paneId)
  useTabStore.getState().setActiveTab(tabId)
  const ws = useWorkspaceStore.getState().findWorkspaceByTab(tabId)
  // No workspace = nobody has adopted the tab yet (features/workspace/lib/adopt-standalone.ts waits before
  // it believes that). There is no "Home" view to switch to; like a click on the tab (`handleSelectTab`),
  // the workspace on screen stays.
  if (ws) {
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)
    useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tabId)
  }
}
