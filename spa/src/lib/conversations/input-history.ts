// spa/src/lib/conversations/input-history.ts — Up / Down in the deck's input walk this conversation's sent history (#2666), as Claude Code's
// terminal does once nothing is left to take back. The history is the person's own messages of the conversation (the same `user` items for a
// terminal session and a worker), newest first; the walk position lives here, per input key, so it survives the input's unmount on a tab switch.
// A sent image comes back as a chip: its `[Image: source: <path>]` marker becomes a fresh `[Image #N]` placeholder and a chip pointing at the path
// (the thumbnail is read from the host, as for any chip restored without a local preview). Other attached files are plain paths in the text and stay text.
import { addAttachment, nextAttachmentSeq, removeAttachment, reconcileAttachments } from './attachment-memory'
import { discardCleared } from './draft-clear'
import { writeDraft } from './draft-memory'
import { imagePlaceholder, splitImageRefs } from './image-placeholder'
import type { ConversationItem, UserItem } from './types'

/** The texts the person sent, oldest first: typed (`user`) or delivered from Claude Code's queue (`queued`); consecutive duplicates collapse. */
export function sentTexts(items: readonly ConversationItem[]): string[] {
  const out: string[] = []
  for (const i of items) {
    if (i.type !== 'user') continue
    const u = i as UserItem
    if ((u.source !== 'user' && u.source !== 'queued') || u.truncated || u.text.trim() === '') continue
    if (out[out.length - 1] !== u.text) out.push(u.text)
  }
  return out
}

interface Walk { pos: number; shown: string; chipIds: string[] }

const sources = new Map<string, readonly string[]>()
const walks = new Map<string, Walk>()

/** The conversation's items as the pane reads them now (called on every change). */
export function setHistorySource(key: string, items: readonly ConversationItem[]): void {
  sources.set(key, sentTexts(items))
}

/** Whether the box still shows exactly what the last step put there: an edit turns it into an ordinary draft. */
export function isWalking(key: string, draft: string): boolean {
  const w = walks.get(key)
  return !!w && w.shown === draft
}

const basename = (path: string): string => path.slice(path.lastIndexOf('/') + 1) || path

function restore(key: string, text: string): { draft: string; chipIds: string[] } {
  const chipIds: string[] = []
  let draft = ''
  for (const part of splitImageRefs(text)) {
    if (part.kind === 'text') { draft += part.text; continue }
    const seq = nextAttachmentSeq()
    const token = imagePlaceholder(seq)
    const id = `hist-${seq}`
    addAttachment(key, { id, name: basename(part.path), path: part.path, text: token, image: true, seq })
    chipIds.push(id)
    draft += token
  }
  return { draft, chipIds }
}

/**
 * One Up / Down. `canStart`: the box is empty and nothing else owns Up (it only matters for a walk that is not on). Returns the new box text
 * (the draft memory and the chips are already written), or null when the key is not a history step. Down past the newest ends in an empty box.
 */
export function stepHistory(key: string, dir: 'up' | 'down', draft: string, canStart: boolean): { draft: string } | null {
  const list = sources.get(key) ?? []
  const w = isWalking(key, draft) ? walks.get(key) : undefined
  let pos: number
  if (!w) {
    walks.delete(key)
    if (dir === 'down' || !canStart || list.length === 0) return null
    pos = list.length - 1
  } else if (dir === 'up') {
    if (w.pos <= 0) return null
    pos = Math.min(w.pos, list.length) - 1
  } else {
    pos = w.pos + 1
  }
  for (const id of w?.chipIds ?? []) removeAttachment(key, id)
  if (pos >= list.length) { // past the newest: the walk ends in an empty box
    walks.delete(key)
    writeDraft(key, '')
    return { draft: '' }
  }
  const next = restore(key, list[pos])
  walks.set(key, { pos, shown: next.draft, chipIds: next.chipIds })
  writeDraft(key, next.draft)
  reconcileAttachments(key, next.draft)
  discardCleared(key)
  return { draft: next.draft }
}

/** A pane that is gone for good, or a session it no longer shows. */
export function forgetInputHistoryWhere(match: (key: string) => boolean): void {
  for (const k of [...sources.keys()]) if (match(k)) sources.delete(k)
  for (const k of [...walks.keys()]) if (match(k)) walks.delete(k)
}

/** Tests only: module state outlives a test. */
export const clearAllInputHistory = (): void => { sources.clear(); walks.clear() }
