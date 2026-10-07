// spa/src/lib/team/approval-goto.ts — back to the requester after a decision made in this window (lead-team spec §2 "How
// this spec reads U22" (a), §15; plan v3 P9b-1). Only `submitDecision`'s 200 calls it (approval-decide.ts): a close
// made elsewhere, a 409, a timeout or a cancel never moves the tabs.
//
// The requester is `origin.tmux` on the deciding host: the CC registry's `<session name>:@<win>.%<pane>`. A tab binds a
// tmux session by CODE, not by name, so the name goes through this host's live session list first. Then the two paths
// the rest of the app already uses (lib/open-session-tab.ts): a tab whose live pane shows that session is activated as
// a notification click activates it; with none, one is opened as Hosts › Sessions opens one. The pane id is not used:
// a tab attaches to the whole tmux session, and moving tmux to `@win` is not part of U22.
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { findTabAndPaneBySessionCode } from '../pane-tree'
import { isRefShownNow } from '../shown-hosts'
import { activateTabPane, openSessionTab } from '../open-session-tab'
import type { Approval } from './types'

/**
 * `<session>:@<win>.%<pane>` → the session name (before the FIRST `:`, as `peers.Entry.TmuxSessionName` cuts it — tmux
 * refuses `:` and `.` in session names, so the cut is exact) and the pane (after the last `.`, '' when absent). `null`
 * when there is no `:` (an origin outside tmux has `tmux: ''`) or the name before it is empty.
 */
export function parseOriginTmux(tmux: string): { session: string; pane: string } | null {
  const colon = tmux.indexOf(':')
  if (colon <= 0) return null
  const dot = tmux.lastIndexOf('.')
  return { session: tmux.slice(0, colon), pane: dot > colon ? tmux.slice(dot + 1) : '' }
}

export type GotoOutcome = 'activated' | 'opened' | 'none'

/**
 * Show the requester's tmux session in this window: `'activated'` an existing tab, `'opened'` a new one, `'none'` when
 * there is nothing to switch to — no tmux on the origin, a host hidden in this workbench (a decision is not a request to
 * navigate; P9 addendum decision 1), or a session name this host's list does not hold (ended, renamed, or the list not
 * loaded: the dialog just closes, no toast; decision 2).
 */
export function gotoRequester(hostId: string, approval: Approval): GotoOutcome {
  const parsed = parseOriginTmux(approval.origin.tmux)
  if (parsed === null) return 'none'
  if (!isRefShownNow(hostId)) return 'none'
  const session = useSessionStore.getState().sessions[hostId]?.find((s) => s.name === parsed.session)
  if (!session) return 'none'
  // Live panes only (an ended pane's code may be a new session's after a tmux restart), a primary pane preferred.
  const hit = findTabAndPaneBySessionCode(useTabStore.getState().tabs, hostId, session.code)
  if (hit) {
    activateTabPane(hit.tabId, hit.paneId)
    return 'activated'
  }
  const tabId = openSessionTab(hostId, session)
  if (tabId === null) return 'none'
  // As the notification's new-tab branch: `insertTab` with no target always finds or makes a workspace.
  const ws = useWorkspaceStore.getState().findWorkspaceByTab(tabId)
  if (ws) useWorkspaceStore.getState().setActiveWorkspace(ws.id)
  return 'opened'
}
