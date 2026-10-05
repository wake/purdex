// spa/src/lib/nex/pasted-text.ts — text pasted in the terminal (worker
// prelude U3, spec §5.3 "Pasted text"). Claude Code (CLI 2.1.289) keeps a
// paste inside the prompt as
//   <pasted_content id="hhhh">\n…body…\n</pasted_content id="hhhh">
// and this mirrors its own parser; the prelude shows each pasted body as its
// own block, without the wrapper. Pure.
import type { ContentBlock } from './message-types'
import { splitLines, utf8Length } from './fold'

const OPEN = '<pasted_content id="'
const ID = /^[0-9a-f]{4}$/
/** Up to this many newlines right before an opener / right after a closer belong to the wrapper. */
const WRAPPER_NEWLINES = 2

/**
 * A `text` block split into its typed parts (as they are once the wrapper's
 * newlines are taken; empty ones dropped) and its pasted bodies
 * (`pasted: { lines, cut }`), in order. An opener is exactly
 * `<pasted_content id="hhhh">` + `\n` (four lowercase hex digits); its closer
 * is `\n</pasted_content id="hhhh">` with the same id, so a literal
 * `</pasted_content…>` or another paste's tags inside a body never end it.
 * An opener with no closer runs to the end as a `cut` paste only when the
 * daemon cut the block (`truncated`); otherwise the rest stays literal, as
 * the CLI keeps it. The block's `truncated` / `total_bytes` and its shown
 * size (`shown_bytes`) move to the last block produced, so the one
 * truncation hint follows the cut and reports the whole block. `[block]`,
 * the same object, when no paste was found.
 */
export function splitPasted(block: ContentBlock): ContentBlock[] {
  if (block.type !== 'text' || typeof block.text !== 'string') return [block]
  const t = block.text
  const out: ContentBlock[] = []
  let found = false
  let done = 0 // `t` before this is split off
  let from = 0 // where the next opener search starts
  for (;;) {
    const at = t.indexOf(OPEN, from)
    if (at < 0) break
    const idAt = at + OPEN.length
    const id = t.slice(idAt, idAt + 4)
    if (!ID.test(id) || !t.startsWith('">\n', idAt + 4)) { from = idAt; continue }
    const bodyAt = idAt + 7
    const closer = `\n</pasted_content id="${id}">`
    // From the opener's own newline: an empty body has one newline between the tags.
    const close = t.indexOf(closer, bodyAt - 1)
    if (close < 0 && !block.truncated) break
    let typedEnd = at
    for (let k = 0; k < WRAPPER_NEWLINES && typedEnd > done && t[typedEnd - 1] === '\n'; k++) typedEnd--
    if (typedEnd > done) out.push({ type: 'text', text: t.slice(done, typedEnd) })
    found = true
    const cut = close < 0
    const body = cut ? withoutCloserStart(t.slice(bodyAt), closer) : t.slice(bodyAt, Math.max(bodyAt, close))
    out.push({ type: 'text', text: body, pasted: { lines: splitLines(body).length, cut } })
    if (cut) { done = t.length; break }
    done = close + closer.length
    for (let k = 0; k < WRAPPER_NEWLINES && t[done] === '\n'; k++) done++
    from = done
  }
  if (!found) return [block]
  if (done < t.length) out.push({ type: 'text', text: t.slice(done) })
  const last = out[out.length - 1]
  if (block.truncated !== undefined) last.truncated = block.truncated
  if (block.total_bytes !== undefined) last.total_bytes = block.total_bytes
  if (block.truncated) last.shown_bytes = utf8Length(t)
  return out
}

/**
 * A cut body loses the longest start of its own closer it ends with: the
 * daemon's cut fell inside the closer. A lone trailing `\n` goes too — it may
 * be the body's own, but a trailing newline never changes the line count.
 */
function withoutCloserStart(body: string, closer: string): string {
  for (let k = Math.min(closer.length - 1, body.length); k > 0; k--) {
    if (body.endsWith(closer.slice(0, k))) return body.slice(0, -k)
  }
  return body
}
