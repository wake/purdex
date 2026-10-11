// spa/src/lib/conversations/draft-recall.ts — putting messages the App still holds back into the box (c9d9c7-59, c9d9c7-62).
// The held messages are drawn at the end of the stream (PendingTail) while the box is the input's own state; so a recall goes through the memories
// (the draft, the chips) and tells the input to read them again. Used by the Up key (every held message) and by a message's Undo (that one).
import { addAttachment, reconcileAttachments } from './attachment-memory'
import { discardCleared } from './draft-clear'
import { readDraft, writeDraft } from './draft-memory'
import type { SendQueue } from './send-queue'

const listeners = new Map<string, Set<() => void>>()

/** The input of `key` listens: something put text (and chips) into its memory, read them again. Returns the way to stop. */
export function subscribeRecall(key: string, cb: () => void): () => void {
  let set = listeners.get(key)
  if (!set) listeners.set(key, (set = new Set()))
  set.add(cb)
  return () => { set.delete(cb); if (set.size === 0 && listeners.get(key) === set) listeners.delete(key) }
}

/**
 * Takes the held message `id` back (or, without an id, every held message, oldest first) and puts it into the box of `key` before what is typed
 * there: the box as it was when it was sent (placeholders and all), its chips and their previews with it. False when nothing was left to take
 * (the message had already been handed out, or no input is there to take it).
 */
export function recallHeld(key: string, queue: SendQueue, id?: string): boolean {
  // Nothing listens (no input is mounted for this pane): there is no box to put the text in, so the message stays where it is.
  if ((listeners.get(key)?.size ?? 0) === 0) return false
  const taken = id === undefined ? queue.takeAllHeld() : [queue.takeBack(id)].filter((x) => x !== undefined)
  if (taken.length === 0) return false
  for (const x of taken) for (const a of x.held?.chips ?? []) addAttachment(key, { ...a, hiddenSince: undefined })
  const back = taken.map((x) => x.held?.draft ?? x.text).join('\n')
  const current = readDraft(key) ?? ''
  const next = current ? `${back}\n${current}` : back
  writeDraft(key, next)
  reconcileAttachments(key, next) // a chip whose marker is not in the text hides
  discardCleared(key) // typing something new ends the Esc undo window
  for (const l of [...(listeners.get(key) ?? [])]) l()
  return true
}
