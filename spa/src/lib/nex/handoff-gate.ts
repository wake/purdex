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
//    re-checks the CC identity before it hands anything off.
//
// The relay id the daemon used to publish on the session row was a third
// fallback until P-D.3b; the daemon stopped sending it in alpha.396.
import type { PaneContent } from '../../types/tab'

export interface HandoffGateDeps {
  /** `useAgentStore.agentTypes[compositeKey(hostId, sessionCode)]`. */
  agentType?: string
  /** `selectHandoffReady(hostId)` for the pane's host. */
  handoffReady: boolean
}

export function isHandoffCandidate(content: PaneContent, deps: HandoffGateDeps): boolean {
  if (content.kind !== 'tmux-session') return false
  if (content.terminated) return false
  if (!deps.handoffReady) return false
  if (deps.agentType) return deps.agentType === 'cc'
  const recorded = content.rebuild?.agent?.type
  return recorded === 'cc'
}
