// spa/src/hooks/usePending.ts — what the end of the stream draws for messages waiting to be read (PendingTail): the pane's send queue, live, and
// Claude Code's own queue from the header. Shared by the deck and the chat; also what their follow effect depends on.
import { useSyncExternalStore } from 'react'
import { draftKey } from '../lib/conversations/draft-memory'
import { hostSendPort } from '../lib/conversations/send'
import { sendQueueFor, type QueueEntry } from '../lib/conversations/send-queue'
import type { HeaderQueued } from '../lib/conversations/types'

/** The pane's send queue entries, live (the queue outlives the pane's components; see send-queue). */
export function usePendingEntries(paneKey: string, hostId: string, sessionId: string): readonly QueueEntry[] {
  const queue = sendQueueFor(draftKey(paneKey, hostId, sessionId), () => hostSendPort(hostId, sessionId))
  return useSyncExternalStore(queue.subscribe, queue.entries)
}

const NONE: readonly HeaderQueued[] = [] // one array, so a render with nothing to show is no change for an effect that depends on it

/** Claude Code's queue as the header reports it, only while the conversation is working; none for an older daemon. */
export function shownHeaderQueue(queued: readonly HeaderQueued[] | undefined, status: string | undefined): readonly HeaderQueued[] {
  return (status === 'running' || status === 'waiting') && queued ? queued : NONE
}
