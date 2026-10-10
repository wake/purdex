// spa/src/lib/conversations/draft-clear.ts — Esc clears the input's draft, and a few seconds later it is really gone (c9d9c7-53).
// "Clear" takes the draft text and every chip (the hidden ones too, previews included) out of the memories, aborts the uploads still on
// their way, and keeps what it took for UNDO_MS: `undoClear` puts it all back — text, chips, `[Image #N]` placeholders (the chips keep their
// numbers: the count is global and never goes back) and the preview blob URLs, which are revoked only when the window closes without an
// undo (a revoked URL is dead). Kept outside the component like the draft: the input unmounts on a tab switch and the window runs on.
import { useCallback, useSyncExternalStore } from 'react'
import { addAttachment, forgetAttachmentsWhere, readAttachments, revokePreviewUrl, takeAttachments, type Attachment } from './attachment-memory'
import { forgetUploadsWhere, uploadsOf } from './attachment-upload'
import { readDraft, writeDraft } from './draft-memory'

/** How long a cleared draft can be brought back. */
export const UNDO_CLEAR_MS = 5000

interface Cleared { text: string; chips: readonly Attachment[]; timer: ReturnType<typeof setTimeout> }

const cleared = new Map<string, Cleared>()
const listeners = new Map<string, Set<() => void>>()
const notify = (key: string): void => { for (const l of [...(listeners.get(key) ?? [])]) l() }

export function subscribeCleared(key: string, cb: () => void): () => void {
  let set = listeners.get(key)
  if (!set) listeners.set(key, (set = new Set()))
  set.add(cb)
  return () => { set.delete(cb); if (set.size === 0 && listeners.get(key) === set) listeners.delete(key) }
}

export const hasCleared = (key: string): boolean => cleared.has(key)

/** Whether a cleared draft of `key` is waiting to be undone (live). */
export function useHasCleared(key: string): boolean {
  return useSyncExternalStore(useCallback((cb: () => void) => subscribeCleared(key, cb), [key]), () => hasCleared(key))
}

/** The window closed (or a new draft started): the previews can go now. */
function release(key: string, c: Cleared): void {
  clearTimeout(c.timer)
  for (const a of c.chips) revokePreviewUrl(a.previewUrl)
  if (cleared.get(key) === c) cleared.delete(key)
  notify(key)
}

/** Lets a cleared draft go for good (the reader typed something new, sent, or the pane was released). */
export function discardCleared(key: string): void {
  const c = cleared.get(key)
  if (c) release(key, c)
}

/** Whether there is anything to clear: text, a chip (hidden ones included) or an upload on its way. */
export function hasDraftContent(key: string): boolean {
  return (readDraft(key) ?? '') !== '' || readAttachments(key).length > 0 || uploadsOf(key).length > 0
}

/** Clears the draft of `key`; false (and nothing touched) when there was nothing. */
export function clearDraft(key: string): boolean {
  if (!hasDraftContent(key)) return false
  discardCleared(key) // an earlier window: its previews are not coming back
  const text = readDraft(key) ?? ''
  const chips = takeAttachments(key) // out of the memory, previews NOT revoked
  writeDraft(key, '')
  forgetAttachmentsWhere((k) => k === key) // aborts the uploads still on their way (the chips are already out)
  forgetUploadsWhere((k) => k === key) // and drops their progress entries and previews
  const c: Cleared = { text, chips, timer: setTimeout(() => release(key, c), UNDO_CLEAR_MS) }
  cleared.set(key, c)
  notify(key)
  return true
}

/** Puts the cleared draft back (text and chips); returns the text, or null when the window had closed. */
export function undoClear(key: string): string | null {
  const c = cleared.get(key)
  if (!c) return null
  clearTimeout(c.timer)
  cleared.delete(key)
  // anything typed since would be overwritten: the draft is restored only into an empty box (the input drops the window when the reader types)
  writeDraft(key, c.text)
  for (const a of c.chips) addAttachment(key, { ...a, hiddenSince: undefined })
  notify(key)
  return c.text
}

/** A released pane / session: its cleared draft goes with it. */
export function forgetClearedWhere(match: (key: string) => boolean): void {
  for (const key of [...cleared.keys()]) if (match(key)) discardCleared(key)
}

/** Tests only: module state outlives a test. */
export function clearAllCleared(): void {
  for (const [key, c] of [...cleared]) release(key, c)
  listeners.clear()
}
