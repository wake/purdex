// spa/src/lib/nex/handoff-gate.ts — may this pane offer "Hand to nex"?
// (P-C.3 spec §4.4, plan Task 3). Pure: the renderer feeds it the pane's
// content and two store-derived facts; nothing here reads a store.
//
// Whether the pane runs Claude Code is answered by two sources of
// decreasing freshness, and the first one that speaks decides:
//
// 1. the live agent type the daemon last reported for the session
//    (`useAgentStore.agentTypes`) — if it names any agent at all, that is the
//    truth, so a pane whose user switched from CC to codex hides the item
//    even though the older rebuild record still says `cc`;
// 2. the pane's rebuild record (`rebuild.agent.type`, written at
//    SessionStart) — accepted even when `unverified`, because the daemon
//    re-checks the CC identity before it hands anything off; NOT accepted once
//    `rebuild.agentExited` is set (the recorded run ended — P6 re-review).
//
// The relay id the daemon used to publish on the session row was a third
// fallback until P-D.3b; the daemon stopped sending it in alpha.396.
import type { PaneContent, TmuxSessionContent } from '../../types/tab'

export interface HandoffGateDeps {
  /** `useAgentStore.agentTypes[compositeKey(hostId, sessionCode)]`. */
  agentType?: string
  /** `selectHandoffReady(hostId)` for the pane's host. */
  handoffReady: boolean
}

/**
 * Why the pane may not be handed to nex (shell cleanup spec §9.3: the status
 * bar's disabled worker / chat buttons say why).
 * - `not_session`: not a tmux pane at all;
 * - `terminated`: the pane's session is gone;
 * - `not_agent`: nothing says the pane runs Claude Code (the two sources above);
 * - `nex_not_ready`: it does, but the host cannot take a handoff now.
 * The agent is asked before the host: for a plain shell, a ready Nexen would
 * change nothing, so "not running Claude Code" is the answer that helps.
 */
export type HandoffBlockReason = 'not_session' | 'terminated' | 'not_agent' | 'nex_not_ready'

/**
 * Does the pane run Claude Code? An empty live type is no information and falls through to the record. The record counts
 * only while its agent has not exited: Claude Code's exit clears the live type (`clearSession`) and marks the record
 * `agentExited` (`writeExitRecord`), so an exited record is the one source left and must not keep saying `cc`
 * (P6 re-review). Shared with the conversation views (U3-1a).
 */
export function runsClaudeCode(content: TmuxSessionContent, liveAgentType: string | undefined): boolean {
  const recorded = content.rebuild?.agentExited ? undefined : content.rebuild?.agent?.type
  return (liveAgentType || recorded) === 'cc'
}

export function handoffBlockReason(content: PaneContent, deps: HandoffGateDeps): HandoffBlockReason | null {
  if (content.kind !== 'tmux-session') return 'not_session'
  if (content.terminated) return 'terminated'
  if (!runsClaudeCode(content, deps.agentType)) return 'not_agent'
  if (!deps.handoffReady) return 'nex_not_ready'
  return null
}

export function isHandoffCandidate(content: PaneContent, deps: HandoffGateDeps): boolean {
  return handoffBlockReason(content, deps) === null
}
