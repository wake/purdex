// spa/src/hooks/usePending.ts — what the end of the stream draws for messages waiting to be read (PendingTail): the pane's send queue, live, and
// Claude Code's own queue from the header. Shared by the deck and the chat; also what their follow effect depends on.
import { useSyncExternalStore } from 'react'
import { draftKey } from '../lib/conversations/draft-memory'
import { unboundSendPort } from '../lib/conversations/send'
import { sendQueueFor, type QueueEntry } from '../lib/conversations/send-queue'
import type { HeaderQueued } from '../lib/conversations/types'

/** The pane's send queue entries, live (the queue outlives the pane's components; see send-queue). */
export function usePendingEntries(paneKey: string, hostId: string, sessionId: string): readonly QueueEntry[] {
  const queue = sendQueueFor(draftKey(paneKey, hostId, sessionId), () => unboundSendPort(hostId, sessionId))
  return useSyncExternalStore(queue.subscribe, queue.entries)
}

const NONE: readonly HeaderQueued[] = [] // one array, so a render with nothing to show is no change for an effect that depends on it
const MAX_SHOWN = 20 // a daemon that lists more than this is not asked to flood the stream

/**
 * Claude Code's queue as the header reports it, only while the conversation is working; none for an older daemon. The header is a wire value: anything
 * that is not a list of objects is ignored, an entry without text is dropped, and the rest is coerced to what the lines draw.
 */
export function shownHeaderQueue(queued: unknown, status: string | undefined): readonly HeaderQueued[] {
  if ((status !== 'running' && status !== 'waiting') || !Array.isArray(queued)) return NONE
  const out: HeaderQueued[] = []
  for (const q of queued) {
    if (out.length >= MAX_SHOWN) break
    if (q === null || typeof q !== 'object') continue
    const r = q as Record<string, unknown>
    if (typeof r.text !== 'string' || r.text === '') continue
    out.push({ text: r.text, at: typeof r.at === 'number' ? r.at : 0, source: typeof r.source === 'string' ? r.source : 'user', ...(typeof r.from === 'string' ? { from: r.from } : {}) })
  }
  return out.length === 0 ? NONE : out
}

/** What the end of the stream's follow effect depends on: the lines themselves, not the identity of a header that is a new object on every frame. */
export const queuedSignature = (queued: readonly HeaderQueued[]): string => `${queued.length}:${queued.map((q) => q.text).join('\u0000')}`
