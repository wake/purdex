import { Lexer } from 'marked'

/**
 * Live Mode (Tiptap) models a lossy subset of markdown, and the loss happens at
 * parse time rather than at serialization: a construct Tiptap cannot represent is
 * already gone by the time the user types a character, and the next save writes
 * the degraded document back over the original.
 *
 * This gate answers "may this document be opened in Live Mode?" so that the
 * invariant becomes: anything that reaches Live Mode is known to round-trip.
 * It is deliberately default-deny — an unrecognised construct fails closed and
 * the file opens raw.
 */
export interface RoundTripVerdict {
  safe: boolean
  /** Stable keys for the UI, e.g. 'html' / 'frontmatter' / 'footnote'. */
  blockers: string[]
}

/**
 * marked v17 token types Tiptap round-trips faithfully enough (style-level
 * rewrites such as bullet or emphasis markers are accepted).
 *
 * Notes:
 * - Task items are not their own token type: they are `list_item`s carrying
 *   `task: boolean`, plus a `checkbox` token that marked unshifts into the
 *   item's children. (`taskList` / `taskItem` are Tiptap's internal
 *   intermediate names and never appear here.)
 * - `def` (reference-style link definitions) is allowed: the round-trip inlines
 *   them, which is a style-level change. Blocking it would push a large share of
 *   ordinary documents into raw mode.
 */
const WHITELIST = new Set([
  'space',
  'paragraph',
  'text',
  'heading',
  'list',
  'list_item',
  'checkbox',
  'code',
  'codespan',
  'blockquote',
  'hr',
  'table',
  'link',
  'image',
  'strong',
  'em',
  'del',
  'br',
  'escape',
  'def',
])

/**
 * A `---` fence on the very first line, closed by a later `---` or `...` line.
 * Intentionally not multiline-anchored: the opening fence must be the start of
 * the document, and the closing fence is kept on a line of its own by the
 * preceding newline.
 */
const FRONT_MATTER = /^---[ \t]*\r?\n[\s\S]*?\r?\n(?:---|\.\.\.)[ \t]*(?:\r?\n|$)/

/** `[^label]:` at the start of a line — a footnote definition. */
const FOOTNOTE_DEF = /^ {0,3}\[\^([^\]\s]+)\]:/gm

/** `[^label]` not followed by `:` — a footnote reference. */
const FOOTNOTE_REF = /\[\^([^\]\s]+)\](?!:)/g

/**
 * marked degrades these two constructs instead of giving them their own token
 * type, so they have to be spotted in the source text:
 * - front matter lexes as `hr` + whatever the YAML happens to look like, which
 *   is why it must NOT be inferred from an `hr` + `heading` token pair — plenty
 *   of legitimate documents contain exactly that sequence.
 * - footnotes lex as an ordinary `link` plus a `def`.
 */
function probeSource(md: string, add: (blocker: string) => void): void {
  if (FRONT_MATTER.test(md)) add('frontmatter')
  if (hasFootnote(md)) add('footnote')
}

function hasFootnote(md: string): boolean {
  const defined = new Set<string>()
  for (const match of md.matchAll(FOOTNOTE_DEF)) defined.add(match[1])
  if (defined.size === 0) return false
  for (const match of md.matchAll(FOOTNOTE_REF)) {
    if (defined.has(match[1])) return true
  }
  return false
}

/** Shape of the token children marked produces; deliberately structural. */
interface TokenLike {
  type?: string
  tokens?: unknown
  items?: unknown
  header?: unknown
  rows?: unknown
}

function isTokenLike(value: unknown): value is TokenLike {
  return typeof value === 'object' && value !== null
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

/**
 * Walks the token tree the way marked's own `walkTokens` does: children hang off
 * `tokens` for most nodes, off `items` for lists, and off the cells in `header` /
 * `rows` for tables (a cell's `tokens` is the only place inline content inside a
 * table can be seen).
 */
function walk(nodes: unknown[], add: (blocker: string) => void): void {
  for (const node of nodes) {
    if (!isTokenLike(node)) continue
    const type = typeof node.type === 'string' ? node.type : ''
    if (!WHITELIST.has(type)) add(type || 'unknown')

    walk(asArray(node.tokens), add)
    walk(asArray(node.items), add)
    for (const cell of asArray(node.header)) {
      if (isTokenLike(cell)) walk(asArray(cell.tokens), add)
    }
    for (const row of asArray(node.rows)) {
      for (const cell of asArray(row)) {
        if (isTokenLike(cell)) walk(asArray(cell.tokens), add)
      }
    }
  }
}

/**
 * Pure and synchronous: the same input always yields the same verdict, and the
 * returned `blockers` array is freshly allocated per call.
 */
export function assessMarkdownRoundTrip(md: string): RoundTripVerdict {
  const seen = new Set<string>()
  const add = (blocker: string) => seen.add(blocker)

  probeSource(md, add)
  walk(new Lexer().lex(md), add)

  return { safe: seen.size === 0, blockers: [...seen] }
}
