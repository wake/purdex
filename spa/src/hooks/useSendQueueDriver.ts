// spa/src/hooks/useSendQueueDriver.ts — what keeps a pane's send queue moving: the header's idle observation (a message the mod
// refused as busy is resent on the first idle) and the transcript's user messages (an echo settles a queued one). Called by
// the PANE (SessionPaneContent), which stays mounted under every view, so it works under the terminal view too, where no
// input is mounted. The input only types and shows; it drives nothing.
import { useEffect, useMemo } from 'react'
import { draftKey } from '../lib/conversations/draft-memory'
import { hostSendPort } from '../lib/conversations/send'
import { sendQueueFor } from '../lib/conversations/send-queue'
import type { ConversationItem, UserItem } from '../lib/conversations/types'

/** `sessionId` '' (the conversation is not ready) drives nothing and creates no queue. */
export function useSendQueueDriver(paneKey: string, hostId: string, sessionId: string, idle: boolean, items: readonly ConversationItem[]): void {
  const key = sessionId ? draftKey(paneKey, hostId, sessionId) : ''
  const queue = key ? sendQueueFor(key, () => hostSendPort(hostId, sessionId)) : null
  const users = useMemo(() => items.filter((i): i is UserItem => i.type === 'user'), [items])

  // Reported on mount and on change, as the queue expects (see SendQueue.setIdle).
  useEffect(() => { queue?.setIdle(idle) }, [queue, idle])
  // Reconciled when the transcript changes and whenever the queue itself changes (a send going out may already be echoed).
  useEffect(() => {
    if (!queue) return
    queue.reconcile(users)
    return queue.subscribe(() => queue.reconcile(users))
  }, [queue, users])
}
