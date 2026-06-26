import type { Node as ProsemirrorNode } from '@tiptap/pm/model'
import { Selection, TextSelection } from '@tiptap/pm/state'

const clamp = (n: number, lo: number, hi: number) => Math.min(Math.max(n, lo), hi)

/**
 * Resolve a saved {from,to} into a legal ProseMirror Selection (I2).
 *
 * NOTE: TextSelection.create does NOT throw on non-textblock positions — it
 * only console.warn's and returns an *illegal* selection (verified locally:
 * create(doc,0,0) -> TextSelection 0..0). So we MUST check inlineContent up
 * front rather than relying on try/catch. clamp first (doc.resolve throws
 * RangeError out of range); keep the range when both ends are inline content,
 * otherwise degrade to Selection.near (which never throws).
 */
export function resolveRestoreSelection(
  doc: ProsemirrorNode,
  saved: { from: number; to: number },
): Selection {
  const max = doc.content.size
  const from = clamp(saved.from, 0, max)
  const to = clamp(saved.to, 0, max)
  const $from = doc.resolve(from)
  const $to = doc.resolve(to)
  if ($from.parent.inlineContent && $to.parent.inlineContent) {
    return TextSelection.create(doc, from, to)
  }
  return Selection.near($from)
}
