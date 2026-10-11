// spa/src/hooks/useSendQueueDriver.ts — what keeps a pane's send queue moving: the transcript's user messages (an echo settles a queued one) and
// Claude Code's own queue line from the header (a message the daemon handed over that the line renders is hidden here, so it is not shown twice).
// Called by the PANE (SessionPaneContent), which stays mounted under every view, so it works under the terminal view too, where no input is
// mounted. The input only types and shows; it drives nothing. The queue does not wait for the agent to be idle (K-2: Claude Code queues it).
import { useEffect, useMemo } from 'react'
import { draftKey } from '../lib/conversations/draft-memory'
import { unboundSendPort } from '../lib/conversations/send'
import { sendQueueFor } from '../lib/conversations/send-queue'
import type { ConversationItem, HeaderQueued, UserItem } from '../lib/conversations/types'

const NO_LINES: readonly HeaderQueued[] = []

/**
 * `sessionId` '' (the conversation is not ready) drives nothing and creates no queue. `headerQueued` is what the deck actually renders of Claude
 * Code's queue (`shownHeaderQueue`: nothing while the conversation is idle), so an entry is hidden only while a line really stands for it.
 */
export function useSendQueueDriver(paneKey: string, hostId: string, sessionId: string, items: readonly ConversationItem[], headerQueued: readonly HeaderQueued[] = NO_LINES): void {
  const key = sessionId ? draftKey(paneKey, hostId, sessionId) : ''
  const queue = key ? sendQueueFor(key, () => unboundSendPort(hostId, sessionId)) : null
  const users = useMemo(() => items.filter((i): i is UserItem => i.type === 'user'), [items])
  // the header is a new object on every frame: depend on what the lines say
  const linesSig = JSON.stringify(headerQueued.filter((q) => q.source === 'user').map((q) => ({ text: q.text, at: q.at })))

  // Reported on mount and on change, and again whenever the queue itself changes (a message handed over may already be on the line).
  useEffect(() => {
    if (!queue) return
    const lines = JSON.parse(linesSig) as Array<{ text: string; at: number }>
    queue.setHeaderQueue(lines)
    return queue.subscribe(() => queue.setHeaderQueue(lines))
  }, [queue, linesSig])
  // Reconciled when the transcript changes and whenever the queue itself changes (a send going out may already be echoed).
  useEffect(() => {
    if (!queue) return
    queue.reconcile(users)
    return queue.subscribe(() => queue.reconcile(users))
  }, [queue, users])
}
