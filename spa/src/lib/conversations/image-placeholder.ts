// spa/src/lib/conversations/image-placeholder.ts — the short `[Image #N]` an attached image leaves in the draft (v3 pt 5), and the
// `[Image: source: <path>]` marker the agent reads. The draft holds the short one; sending expands it (the text that leaves is the
// text it always was). A sent message shows the long marker as a thumbnail tag (splitImageRefs).
import type { Attachment } from './attachment-memory'

export const imagePlaceholder = (seq: number): string => `[Image #${seq}]`

/** What the agent reads. */
export const imageMarker = (path: string): string => `[Image: source: ${path}]`

const PLACEHOLDER = /\[Image #(\d+)\]/g

/** At the end of the draft (an upload lands without knowing the caret): as it is behind whitespace, after a space behind text. */
export function insertPlaceholderText(draft: string, token: string): string {
  if (draft === '' || /\s$/.test(draft)) return `${draft}${token}`
  return `${draft} ${token}`
}

/**
 * Every `[Image #N]` of a chip in `list` becomes that chip's marker; anything else (a placeholder the reader broke, a number no
 * chip owns) stays as typed. The same number twice names the same file twice.
 */
export function expandPlaceholders(draft: string, list: readonly Attachment[]): string {
  const paths = new Map<number, string>()
  for (const a of list) if (a.seq !== undefined) paths.set(a.seq, a.path)
  if (paths.size === 0) return draft
  return draft.replace(PLACEHOLDER, (whole, n: string) => {
    const path = paths.get(Number(n))
    return path === undefined ? whole : imageMarker(path)
  })
}

/** One line without `token`: each copy goes with the space after it, else the space before it. */
function stripToken(line: string, token: string): string {
  let out = line
  for (let at = out.indexOf(token); at >= 0; at = out.indexOf(token)) {
    const end = at + token.length
    if (out[end] === ' ') out = out.slice(0, at) + out.slice(end + 1)
    else if (at > 0 && out[at - 1] === ' ') out = out.slice(0, at - 1) + out.slice(end)
    else out = out.slice(0, at) + out.slice(end)
  }
  return out
}

/** Takes every copy of `token` out (with one space beside it); a line that was only the token goes with its line break. */
export function removePlaceholderText(draft: string, token: string): string {
  if (!draft.includes(token)) return draft
  const kept: string[] = []
  for (const line of draft.split('\n')) {
    if (line === token) continue
    kept.push(stripToken(line, token))
  }
  return kept.join('\n')
}

/** Where the placeholders of the given numbers sit in the draft (for the tag overlay behind the box). */
export function placeholderRanges(draft: string, seqs: ReadonlySet<number>): Array<{ start: number; end: number }> {
  const out: Array<{ start: number; end: number }> = []
  for (const m of draft.matchAll(PLACEHOLDER)) {
    if (seqs.has(Number(m[1]))) out.push({ start: m.index, end: m.index + m[0].length })
  }
  return out
}

export type MessagePart = { kind: 'text'; text: string } | { kind: 'image'; path: string }

const IMAGE_REF = /\[Image: source: ([^\]\n]+)\]/g

/** A sent message cut at its `[Image: source: <path>]` markers, in order. */
export function splitImageRefs(text: string): MessagePart[] {
  const parts: MessagePart[] = []
  let at = 0
  for (const m of text.matchAll(IMAGE_REF)) {
    if (m.index > at) parts.push({ kind: 'text', text: text.slice(at, m.index) })
    parts.push({ kind: 'image', path: m[1] })
    at = m.index + m[0].length
  }
  if (at < text.length) parts.push({ kind: 'text', text: text.slice(at) })
  return parts
}
