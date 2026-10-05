// spa/src/lib/nex/pasted-text.ts — text pasted in the terminal (worker
// prelude U3, spec §5.3 "Pasted text"). Claude Code keeps a paste inside the
// prompt as `<pasted_content id="…">\n…\n</pasted_content>`; the prelude
// shows each pasted body as its own block, without the wrapper. Pure.
import type { ContentBlock } from './message-types'
import { splitLines } from './fold'

const OPEN = /<pasted_content id="[^"]*">/g
const CLOSE = '</pasted_content>'

/**
 * A `text` block split into its typed parts (as they are; empty ones
 * dropped) and its pasted bodies (`pasted: { lines, cut }`), in order. One
 * `\n` after the opening tag and one before the closing tag belong to the
 * wrapper. An opening tag with no closing tag — the daemon cut the block —
 * runs to the end and is `cut`. The block's `truncated` / `total_bytes` move
 * to the last block produced, so the one truncation hint still follows the
 * cut. Anything else stays literal: `[block]`, the same object, when there
 * is no well-formed opening tag (a stray closing tag included).
 */
export function splitPasted(block: ContentBlock): ContentBlock[] {
  if (block.type !== 'text' || typeof block.text !== 'string') return [block]
  const text = block.text
  const out: ContentBlock[] = []
  const typed = (s: string) => { if (s) out.push({ type: 'text', text: s }) }
  const open = new RegExp(OPEN)
  let at = 0
  let m: RegExpExecArray | null
  while ((m = open.exec(text)) !== null) {
    typed(text.slice(at, m.index))
    let start = m.index + m[0].length
    if (text[start] === '\n') start++
    const close = text.indexOf(CLOSE, start)
    const cut = close < 0
    let end = cut ? text.length : close
    if (!cut && end > start && text[end - 1] === '\n') end--
    const body = text.slice(start, end)
    out.push({ type: 'text', text: body, pasted: { lines: splitLines(body).length, cut } })
    at = cut ? text.length : close + CLOSE.length
    open.lastIndex = at
  }
  if (out.length === 0) return [block]
  typed(text.slice(at))
  const last = out[out.length - 1]
  if (block.truncated !== undefined) last.truncated = block.truncated
  if (block.total_bytes !== undefined) last.total_bytes = block.total_bytes
  return out
}
