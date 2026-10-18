// spa/src/lib/conversations/insertion-anchor.ts — where the text of a running upload goes into the draft (#3393). A file dropped or
// pasted into the input remembers the caret it came in at; the draft keeps changing while the upload runs, so the point follows the
// edits (draft-memory reports every write here) and the text lands where the reader meant it. Kept OUTSIDE the component, keyed like the
// draft (the tab-hosted rule: the input may be unmounted when the upload lands). A point that cannot be found falls back to the end.

export type InsertMode = 'inline' | 'line'

interface Anchor { id: string; group: string; order: number; pos: number }

const anchors = new Map<string, Anchor[]>()
let seq = 0
/** Set while `landAnchor` writes its own insertion: the anchors have been moved by hand then. */
let landing = false

/**
 * Where `pos` is after `[p, p + oldMid)` of the text was replaced by `newMid` characters. Before the edit: unchanged. At the start of
 * it, or inside the replaced part: the end of what is there now (typing at the point goes before whatever lands at it). After it:
 * carried by the change in length.
 */
export function shiftOffset(pos: number, p: number, oldMid: number, newMid: number): number {
  if (pos < p) return pos
  if (pos <= p + oldMid) return p + newMid
  return pos + newMid - oldMid
}

/** Opens one point per file of a drop, all at `pos`, in the files' order. Returns their ids. */
export function openAnchors(key: string, pos: number, count: number): string[] {
  const group = `grp-${++seq}`
  const made = Array.from({ length: count }, (_, order): Anchor => ({ id: `anc-${++seq}`, group, order, pos }))
  anchors.set(key, [...(anchors.get(key) ?? []), ...made])
  return made.map((a) => a.id)
}

export function closeAnchor(key: string, id: string): void {
  const rest = (anchors.get(key) ?? []).filter((a) => a.id !== id)
  if (rest.length === 0) anchors.delete(key)
  else anchors.set(key, rest)
}

export const hasAnchor = (key: string, id: string): boolean => (anchors.get(key) ?? []).some((a) => a.id === id)

/** The draft of `key` went from `from` to `to`: every open point moves with the one edit between them (common head and tail cut away). */
export function rebaseAnchors(key: string, from: string, to: string): void {
  const list = anchors.get(key)
  if (!list || landing || from === to) return
  let head = 0
  while (head < from.length && head < to.length && from[head] === to[head]) head++
  let tail = 0
  while (tail < from.length - head && tail < to.length - head && from[from.length - 1 - tail] === to[to.length - 1 - tail]) tail++
  const oldMid = from.length - head - tail
  const newMid = to.length - head - tail
  for (const a of list) a.pos = shiftOffset(a.pos, head, oldMid, newMid)
}

/** `text` put into `draft` at `pos` (clamped): inline with a space kept on each side, or on a line of its own. */
export function insertAt(draft: string, pos: number, text: string, mode: InsertMode): { draft: string; at: number; length: number } {
  const at = Math.max(0, Math.min(pos, draft.length))
  const before = draft.slice(0, at)
  const after = draft.slice(at)
  const sep = mode === 'inline' ? ' ' : '\n'
  const pre = before !== '' && (mode === 'inline' ? !/\s$/.test(before) : !before.endsWith('\n')) ? sep : ''
  const post = after !== '' && (mode === 'inline' ? !/^\s/.test(after) : !after.startsWith('\n')) ? sep : ''
  const put = `${pre}${text}${post}`
  return { draft: `${before}${put}${after}`, at, length: put.length }
}

/**
 * Puts `text` where the point `id` is and closes it; `write` stores the new draft. The other points move with the insertion, except
 * that a file of the same drop that comes earlier stays in front (so the files end up in their order whichever lands first).
 * An unknown point (the pane was released, or the file came without a caret) appends, as an upload without a caret always did.
 */
export function landAnchor(key: string, id: string, draft: string, text: string, mode: InsertMode, write: (next: string) => void): void {
  const list = anchors.get(key) ?? []
  const me = list.find((a) => a.id === id)
  const placed = insertAt(draft, me ? me.pos : draft.length, text, mode)
  if (me) {
    for (const a of list) {
      if (a === me) continue
      const before = a.group === me.group && a.order < me.order && a.pos <= placed.at
      if (!before) a.pos = shiftOffset(a.pos, placed.at, 0, placed.length)
    }
    closeAnchor(key, id)
  }
  landing = true
  try { write(placed.draft) } finally { landing = false }
}

export function forgetAnchorsWhere(match: (key: string) => boolean): void {
  for (const key of [...anchors.keys()]) if (match(key)) anchors.delete(key)
}

/** Tests only: module state outlives a test. */
export function clearAllAnchors(): void { anchors.clear() }
