// spa/src/lib/conversations/cjk.ts — letter-spacing for Chinese text only (c9d9c7-52 follow-up): the deck and the chat space the CJK characters of a
// message 1px apart and leave Latin letters alone, so a run of CJK is wrapped in `<span class="cjk">` (index.css: `.cjk { letter-spacing: 1px }`).
// One splitter for both ways text reaches the screen: a rehype step for the agent's markdown (code blocks and the inline code chip are not
// touched), and `splitCjk` for the plain text of the person's / a peer's message. Wrapping adds no text, so a node's `textContent` is unchanged.

// CJK radicals / Kangxi, symbols and punctuation (U+3000–303F), kana, bopomofo, CJK unified ideographs (+ extension A and the astral B–G), compatibility
// ideographs, small/vertical forms and half/fullwidth forms (full-width punctuation and letters).
const CJK_RUN = new RegExp(
  '[\\u2E80-\\u2FDF\\u3000-\\u30FF\\u3100-\\u312F\\u3190-\\u31FF\\u3400-\\u4DBF\\u4E00-\\u9FFF\\uF900-\\uFAFF\\uFE30-\\uFE4F\\uFF00-\\uFFEF]|[\\u{20000}-\\u{323AF}]',
  'u',
)
const CJK_RUNS = new RegExp(`(?:${CJK_RUN.source})+`, 'gu')

export interface CjkPart { text: string; cjk: boolean }

/** The text as alternating runs: a CJK run is `cjk: true`; everything else (Latin, digits, spaces, emoji) is `cjk: false`. `''` has no parts. */
export function splitCjk(text: string): CjkPart[] {
  const parts: CjkPart[] = []
  let last = 0
  for (const m of text.matchAll(CJK_RUNS)) {
    const at = m.index ?? 0
    if (at > last) parts.push({ text: text.slice(last, at), cjk: false })
    parts.push({ text: m[0], cjk: true })
    last = at + m[0].length
  }
  if (last < text.length) parts.push({ text: text.slice(last), cjk: false })
  return parts
}

export const hasCjk = (text: string): boolean => CJK_RUN.test(text)

interface HastNode { type: string; value?: string; tagName?: string; properties?: Record<string, unknown>; children?: HastNode[] }

/** Elements whose text is code: never split (their glyphs and widths are the code's own). */
const SKIP = new Set(['pre', 'code', 'kbd', 'samp', 'script', 'style'])

function wrap(node: HastNode): void {
  if (!node.children) return
  const out: HastNode[] = []
  let changed = false
  for (const child of node.children) {
    if (child.type === 'text' && typeof child.value === 'string' && hasCjk(child.value)) {
      for (const part of splitCjk(child.value)) {
        out.push(part.cjk
          ? { type: 'element', tagName: 'span', properties: { className: ['cjk'] }, children: [{ type: 'text', value: part.text }] }
          : { type: 'text', value: part.text })
      }
      changed = true
    } else {
      if (child.type === 'element' && !(child.tagName && SKIP.has(child.tagName))) wrap(child)
      out.push(child)
    }
  }
  if (changed) node.children = out
}

/** The rehype plugin: wraps CJK runs in text outside code. */
export function rehypeCjk() {
  return (tree: HastNode) => { wrap(tree) }
}
