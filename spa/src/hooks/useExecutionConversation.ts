// spa/src/hooks/useExecutionConversation.ts — the Claude Code conversation a worker (an Nexen execution) runs, for the deck (U4-1b).
// The session id is the execution row's own (`session_id`, which the first turn reports) or, before that, the one it resumes
// (`resume_session_id`); a queued worker has neither yet, and the answer is `resolving` (the pane draws the lifecycle placeholder).
// The conversation is held for as long as the caller is mounted, like a terminal pane's.
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { useHeldConversation } from './useHeldConversation'
import type { PaneConversation } from './useConversationOfPane'

const noRetry = () => {}

export function useExecutionConversation(hostId: string, executionId: string): PaneConversation {
  const summary = useExecutionStore((s) => s.executions[executionKey(hostId, executionId)]?.summary)
  const sessionId = summary?.session_id || summary?.resume_session_id || ''
  const entry = useHeldConversation(hostId, sessionId)
  if (!sessionId) return { state: 'resolving' }
  if (entry?.status === 'unreadable') {
    return { state: 'unreadable', reason: entry.reason === 'provider_unsupported' ? 'provider_unsupported' : 'not_found', retry: noRetry }
  }
  return { state: 'ready', hostId, sessionId, entry }
}
