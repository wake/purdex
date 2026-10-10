// spa/src/components/deck/deck-anchor.ts — where the reader is looking, kept across a plain-text → markdown swap (#2469). Holding the
// top of the first showing item still is not enough when that item is itself the tall message being swapped: its top stays and
// the line under the reader moves. So the anchor is a few words of text at the top of the box, found again in the swapped DOM.
// The words are taken from the plain text (the markdown source, one text node) and searched in the rendered text (many nodes),
// so only runs of letters and digits are used: markdown syntax between them is the one thing the two do not share.

/** The text nodes of `root` in order, with where each starts in their concatenation. */
function textMap(root: Element): { nodes: Text[]; starts: number[]; text: string } {
  const nodes: Text[] = []
  const starts: number[] = []
  let text = ''
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT)
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    nodes.push(n as Text)
    starts.push(text.length)
    text += (n as Text).data
  }
  return { nodes, starts, text }
}

const RUN = /[\p{L}\p{N}]+(?: [\p{L}\p{N}]+){0,2}/gu
/** A run shorter than this is too common to find the same place again. */
const MIN_SNIPPET = 8
/** How far past the caret a snippet may start: about two lines. */
const REACH = 200

/** Letters-and-digits words (up to three, single-spaced) starting at or after `offset`, near enough to be on screen. Null if none is distinctive. */
export function pickSnippet(data: string, offset: number): { snippet: string; index: number } | null {
  // the caret may sit mid-word: that word is cut short, so start after it
  let start = offset
  if (start > 0 && /[\p{L}\p{N}]/u.test(data[start - 1])) while (start < data.length && /[\p{L}\p{N}]/u.test(data[start])) start++
  for (const m of data.slice(start, start + REACH).matchAll(RUN)) {
    if (m[0].length >= MIN_SNIPPET) return { snippet: m[0], index: start + m.index }
  }
  return null
}

/** Index of the `nth` (0-based) occurrence of `snippet` in `text`, or -1. */
function nthIndexOf(text: string, snippet: string, nth: number): number {
  let at = -1
  for (let i = 0; i <= nth; i++) {
    at = text.indexOf(snippet, at + 1)
    if (at < 0) return -1
  }
  return at
}

function rectTop(node: Text, offset: number): number {
  const range = document.createRange()
  range.setStart(node, offset)
  range.setEnd(node, Math.min(node.data.length, offset + 1))
  return range.getBoundingClientRect().top
}

export interface TextAnchor {
  item: Element
  snippet: string
  /** Which occurrence of the snippet in the item's text (so a repeated phrase is found at the same place). */
  nth: number
  /** Where its first character was, from the viewport's top. */
  top: number
}

type CaretFromPoint = (x: number, y: number) => Range | null

function caretRange(x: number, y: number): Range | null {
  const d = document as unknown as { caretRangeFromPoint?: CaretFromPoint; caretPositionFromPoint?: (x: number, y: number) => { offsetNode: Node; offset: number } | null }
  if (typeof d.caretRangeFromPoint === 'function') return d.caretRangeFromPoint(x, y)
  const pos = typeof d.caretPositionFromPoint === 'function' ? d.caretPositionFromPoint(x, y) : null
  if (!pos) return null
  const range = document.createRange()
  range.setStart(pos.offsetNode, pos.offset)
  return range
}

/** The words showing at `(x, y)` inside `item`, or null when the browser cannot say or nothing distinctive is there. */
export function captureTextAnchor(item: Element, x: number, y: number): TextAnchor | null {
  const caret = caretRange(x, y)
  if (!caret || caret.startContainer.nodeType !== Node.TEXT_NODE || !item.contains(caret.startContainer)) return null
  const node = caret.startContainer as Text
  const picked = pickSnippet(node.data, caret.startOffset)
  if (!picked) return null
  const { nodes, starts, text } = textMap(item)
  const abs = starts[nodes.indexOf(node)] + picked.index
  let nth = 0
  for (let at = text.indexOf(picked.snippet); at >= 0 && at < abs; at = text.indexOf(picked.snippet, at + 1)) nth++
  return { item, snippet: picked.snippet, nth, top: rectTop(node, picked.index) }
}

/** Where the anchor's words are now, from the viewport's top; null if they cannot be found again. */
export function currentTextTop(anchor: TextAnchor): number | null {
  if (!anchor.item.isConnected) return null
  const { nodes, starts, text } = textMap(anchor.item)
  const abs = nthIndexOf(text, anchor.snippet, anchor.nth)
  if (abs < 0) return null
  let i = nodes.length - 1
  while (i > 0 && starts[i] > abs) i--
  return rectTop(nodes[i], abs - starts[i])
}
