// spa/src/lib/notification-click.ts — what a click on a desktop notification does, per action kind (open-session /
// open-host / open-approval); moved from hooks/useNotificationDispatcher.ts (#1690).
import { useAgentStore } from '../stores/useAgentStore'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useSessionStore } from '../stores/useSessionStore'
import { findTabAndPaneBySessionCode } from './pane-tree'
import { activateTabPane } from './open-session-tab'
import { isExecAgentCode } from './nex/worker-agent-status'
import { isNonTmuxAgentCode } from './non-tmux-agent'
import { useHostStore } from '../stores/useHostStore'
import { approvalKey, useApprovalStore } from '../stores/useApprovalStore'
import { landOnHostsPageIfHidden } from './shown-hosts'
import { createTab } from '../types/tab'

export type NotificationAction =
  | { kind: 'open-session'; hostId: string; sessionCode: string }
  | { kind: 'open-host'; hostId: string }
  /** An approval request (lead-team spec §6.3): the dialog is already on screen; the click focuses the window and
   *  restores the dialog when it was minimized and this request is still open (U22 (b)). No `requestId` (a payload
   *  from before it was carried) reads as ended. */
  | { kind: 'open-approval'; hostId: string; requestId?: string }

export function handleNotificationClick(action: NotificationAction): void {
  switch (action.kind) {
    case 'open-session': {
      const { hostId, sessionCode } = action
      const tabs = useTabStore.getState().tabs
      // Any pane of any tab, a primary pane preferred (#1840).
      const hit = findTabAndPaneBySessionCode(tabs, hostId, sessionCode)
      const ck = `${hostId}:${sessionCode}`
      const event = useAgentStore.getState().lastEvents[ck]
      const agentSettings = useNotificationSettingsStore.getState().getSettingsForAgent(event?.agent_type || '')

      let handled = false
      if (landOnHostsPageIfHidden(hostId)) {
        // Host ownership H2d-3: the notification of a host hidden in this workbench still fired; its click lands on
        // the Hosts page — no tab created, and none focused even when a tab of that session exists (its pane is gated).
        handled = true
      } else if (hit) {
        // The tab, its workspace and the pane's keyboard focus (#1840 A1) — shared with the approval switch (U22).
        activateTabPane(hit.tabId, hit.paneId)
        handled = true
      } else if (isNonTmuxAgentCode(sessionCode)) {
        // A session outside tmux has no tab and never gets one (its code is not a tmux code): the click only clears
        // the unread mark and brings the app to the front.
        useAgentStore.getState().markRead(hostId, sessionCode)
        window.electronAPI?.focusMyWindow?.()
      } else if (isExecAgentCode(sessionCode)) {
        // A worker with no open tab: never reopen it as a tmux tab (an exec key is not a tmux code). Nothing to
        // focus, so only the unread mark is cleared (worker-pane theme spec §8.2).
        useAgentStore.getState().markRead(hostId, sessionCode)
      } else if (agentSettings.reopenTabOnClick) {
        const session = useSessionStore.getState().sessions[hostId]?.find(s => s.code === sessionCode)
        const sessionName = session?.name ?? ''
        // Generation from the session payload we are reopening (spec §4.5);
        // '' when the session is not in the cache = unknown, never a match.
        const newTab = createTab({ kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: sessionName, tmuxInstance: session?.tmux_instance ?? '' })
        useTabStore.getState().addTab(newTab)
        useTabStore.getState().setActiveTab(newTab.id)
        useWorkspaceStore.getState().insertTab(newTab.id)
        // `insertTab` with no target always finds or makes a workspace (active → first → Unsorted).
        const ws = useWorkspaceStore.getState().findWorkspaceByTab(newTab.id)
        if (ws) useWorkspaceStore.getState().setActiveWorkspace(ws.id)
        handled = true
      }

      if (handled) {
        useAgentStore.getState().markRead(hostId, sessionCode)
      }
      if (handled && window.electronAPI?.focusMyWindow) {
        window.electronAPI.focusMyWindow()
      }
      break
    }
    case 'open-host': {
      useTabStore.getState().openSingletonTab({ kind: 'hosts' })
      useHostStore.getState().setActiveHost(action.hostId)
      if (window.electronAPI?.focusMyWindow) {
        window.electronAPI.focusMyWindow()
      }
      break
    }
    case 'open-approval': {
      // The dialog is global and already shows the oldest open request; there is no tab to open or host to switch.
      // Minimized in this window (U22 (b)): the click is the person's own action, so it restores the dialog (plan P9
      // open question 4) — never an automatic expansion. Only while the clicked request is still open: a notification
      // outlives its request, and a stale one must not expand another request's dialog (P9b-2 review).
      const approvals = useApprovalStore.getState()
      if (action.requestId !== undefined && Object.hasOwn(approvals.entries, approvalKey(action.hostId, action.requestId))) {
        approvals.setMinimized(false)
      }
      if (window.electronAPI?.focusMyWindow) {
        window.electronAPI.focusMyWindow()
      }
      break
    }
  }
}
