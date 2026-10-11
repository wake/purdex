// spa/src/hooks/useLaunchPlanCandidate.ts — is this pane worth asking the daemon for a launch plan? (spec 2026-10-12 §1, §3).
// ONE rule for the status bar (deck / chat buttons must be reachable) and the pane (what it renders), so they cannot
// drift. The conversation gate stays conversation-only; this builds on its closed reasons.
//
// `not_agent` / `no_conversations` already imply a live tmux session on a shown host: `useConversationViewGate` returns
// `not_session`, `terminated` and `host_hidden` before it ever looks at the agent or the capability.
import { useNexHostStore, selectSessionLaunchV1 } from '../stores/useNexHostStore'
import type { PaneContent } from '../types/tab'
import type { ConversationViewGate } from './useConversationViewGate'

const absent = () => false

/** Pure rule: the conversation gate is closed because there is nothing to read, and the host can launch one — worth asking
 *  the daemon for its plan. It does NOT say the panel shows: the plan may say an agent is running, or fail. */
export function launchPlanCandidate(gate: ConversationViewGate, launchServed: boolean): boolean {
  return !gate.ok && (gate.reason === 'not_agent' || gate.reason === 'no_conversations') && launchServed
}

/** Whether the pane's host serves `session.launch.v1` (false for a non-session pane). `selectSessionLaunchV1` reads
 *  `byHost[hostId]?.daemonCapabilities`, which is undefined (so false) for prototype-member ids like `toString`. */
export function useLaunchServed(content: PaneContent | null | undefined): boolean {
  const hostId = content?.kind === 'tmux-session' ? content.hostId : ''
  return useNexHostStore(hostId ? selectSessionLaunchV1(hostId) : absent)
}

export function useLaunchPlanCandidate(content: PaneContent | null | undefined, gate: ConversationViewGate): boolean {
  return launchPlanCandidate(gate, useLaunchServed(content))
}
