// spa/src/hooks/useConversationOfPane.ts — which conversation does this pane show, and hold it (U3 plan D3, D4).
// A pane knows its tmux session code, not Claude Code's session id: the id comes from the daemon's provenance for the code
// (`found`, `session_id`, `agent_type === 'cc'`) and is read again when the pane's agent reports a different session
// (`/clear`, a relay: the rebuild record's session id moves). The conversation is then held in `useConversationStore`
// for as long as the pane is mounted, so every view of the pane (terminal, deck, chat) shares one stream.
import { useCallback, useEffect, useState } from 'react'
import { fetchSessionProvenance } from '../lib/host-api'
import { selectConversation, useConversationStore, type ConversationEntry } from '../stores/useConversationStore'
import type { PaneContent } from '../types/tab'

export type PaneConversationUnreadable =
  /** The daemon has no Claude Code session for this pane (nothing found, or the pane runs another agent). */
  | 'no_session'
  /** The provenance could not be read (host down): retry. */
  | 'unreachable'
  /** The conversation API says so (`not_found` | `provider_unsupported`). */
  | 'not_found' | 'provider_unsupported'

export type PaneConversation =
  | { state: 'off' }
  | { state: 'resolving' }
  | { state: 'unreadable'; reason: PaneConversationUnreadable; retry: () => void }
  | { state: 'ready'; hostId: string; sessionId: string; entry: ConversationEntry | undefined }

type Resolved = { sessionId: string } | { none: 'no_session' | 'unreachable' }

/** `enabled` false (the pane is not a Claude Code terminal on a host that serves conversations) holds nothing. */
export function useConversationOfPane(content: PaneContent | null | undefined, enabled: boolean): PaneConversation {
  const tmux = content?.kind === 'tmux-session' ? content : null
  const hostId = tmux?.hostId ?? ''
  const code = tmux?.sessionCode ?? ''
  const active = enabled && tmux !== null && !tmux.terminated
  // The pane's own record of which session its agent runs: it changes when the agent reports a new one, which is the cue
  // to read the provenance again.
  const reportedId = tmux?.rebuild?.agent?.sessionId ?? ''
  const [tick, setTick] = useState(0)
  const retry = useCallback(() => setTick((n) => n + 1), [])
  const [resolved, setResolved] = useState<{ for: string; value: Resolved } | null>(null)
  const askKey = `${hostId}\0${code}\0${reportedId}\0${tick}`

  useEffect(() => {
    if (!active) return
    const ac = new AbortController()
    fetchSessionProvenance(hostId, code, ac.signal)
      .then((p): Resolved => (p.found && p.agentType === 'cc' && p.sessionId ? { sessionId: p.sessionId } : { none: 'no_session' }))
      .catch((): Resolved => ({ none: 'unreachable' }))
      .then((value) => { if (!ac.signal.aborted) setResolved({ for: askKey, value }) })
    return () => ac.abort()
  }, [active, hostId, code, askKey])

  // Only an answer to THIS question counts: after a session change the old id is not held while the new one is asked for.
  const answer = active && resolved?.for === askKey ? resolved.value : null
  const sessionId = answer && 'sessionId' in answer ? answer.sessionId : ''

  useEffect(() => {
    if (!sessionId) return
    return useConversationStore.getState().acquire(hostId, sessionId)
  }, [hostId, sessionId])

  const entry = useConversationStore(sessionId ? selectConversation(hostId, sessionId) : () => undefined)

  if (!active) return { state: 'off' }
  if (!answer) return { state: 'resolving' }
  if ('none' in answer) return { state: 'unreadable', reason: answer.none, retry }
  if (entry?.status === 'unreadable') {
    return { state: 'unreadable', reason: entry.reason === 'provider_unsupported' ? 'provider_unsupported' : 'not_found', retry }
  }
  return { state: 'ready', hostId, sessionId, entry }
}
