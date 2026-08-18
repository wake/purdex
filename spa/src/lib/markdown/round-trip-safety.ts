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

/** The opening fence: `---` alone on the very first line of the document. */
const FENCE_OPEN = /^---[ \t]*$/

/** The closing fence: `---` or `...` alone on a line. */
const FENCE_CLOSE = /^(?:---|\.\.\.)[ \t]*$/

/**
 * `key:` or `key: value` — the shape every YAML mapping entry has. The value
 * must be separated by whitespace or absent, which is what keeps a bare URL
 * (`https://example.com`) from reading as the key `https`.
 */
const YAML_KEY = /^[ \t]*(?:"[^"]*"|'[^']*'|[^\s:#][^:]*)[ \t]*:(?:[ \t].*)?$/

/** `- item` / `-` — a YAML sequence entry, at any indentation. */
const YAML_SEQUENCE_ITEM = /^[ \t]*-(?:[ \t]+\S.*)?$/

/** `# …` — a YAML comment. */
const YAML_COMMENT = /^[ \t]*#/

/**
 * A fence on line 1 is not enough to call something front matter: `---` is also
 * a thematic break and the underline of a setext heading, so
 *
 *     ---
 *     hello
 *     ---
 *
 * is ordinary markdown that round-trips perfectly. What separates the two is
 * whether the fenced block reads as metadata, so every non-blank line inside it
 * has to look like YAML — a `key:` entry, a `- item`, a `#` comment, or a line
 * indented under an entry already seen (nested maps and block scalars). At least
 * one real `key:` entry must be present, which also rules out an empty fence.
 *
 * The bias is deliberate: a false positive costs a round-trippable file its Live
 * Mode, and there is no signal left to recover it from.
 */
function hasFrontMatter(md: string): boolean {
  const lines = md.split(/\r?\n/)
  if (!FENCE_OPEN.test(lines[0] ?? '')) return false

  let sawKey = false
  for (const line of lines.slice(1)) {
    if (FENCE_CLOSE.test(line)) return sawKey
    if (line.trim() === '') continue
    if (YAML_COMMENT.test(line) || YAML_SEQUENCE_ITEM.test(line)) continue
    if (YAML_KEY.test(line)) {
      sawKey = true
      continue
    }
    // A continuation line only makes sense under an entry, and only indented.
    if (sawKey && /^[ \t]/.test(line)) continue
    return false
  }

  // An unclosed fence is just a thematic break followed by the rest of the file.
  return false
}

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
  if (hasFrontMatter(md)) add('frontmatter')
  if (hasFootnote(md)) add('footnote')
  if (hasMixedLineEndings(md)) add('mixed-eol')
}

/**
 * The buffer records ONE `sourceEol`, so the Live Mode path can restore only one
 * line ending: `a\r\nb\nc\r\n` is written back as `a\r\nb\r\nc\r\n`, silently
 * rewriting a line the user never touched. A per-line mixture cannot be
 * reconstructed from a single value, so a mixed file opens raw instead — Monaco
 * hands back exactly the bytes in its model.
 */
function hasMixedLineEndings(md: string): boolean {
  return md.includes('\r\n') && md.replace(/\r\n/g, '').includes('\n')
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

/**
 * Inline constructs Tiptap models as ProseMirror MARKS. A mark cannot wrap a
 * node, so an image inside one loses it: `[![Build](b.svg)](https://ci)` came
 * back as `![Build](b.svg)` — the badge kept its picture and lost its link — and
 * `[text ![a](a.png)](u)` came back as the broken `[text ](u)![a](a.png)[`.
 */
const MARK_TOKENS = new Set(['link', 'strong', 'em', 'del'])

/**
 * A destination that only survives because it is wrapped in angle brackets: one
 * containing whitespace. The round-trip writes every destination inline and
 * unbracketed, so `![alt](<a b.png>)` comes back as `![alt](a b.png)`, which is
 * not an image at all to a CommonMark renderer (measured).
 *
 * The test is on the destination marked RESOLVED, not on the shape of the source
 * text, because the same broken output has two sources. A reference-style image
 * carries its destination in the definition — `![alt][img]` + `[img]: <a b.png>`
 * — so the token's `raw` is just `![alt][img]`: no parentheses, no brackets, no
 * space for a source-shape rule to see. Both forms serialize to the identical
 * inline destination, so testing the destination covers both at once.
 *
 * Whitespace, not brackets, is the defect: a bracketed URL without one loses
 * only decoration (`![alt](<a.png>)` → `![alt](a.png)`, a decision-3 style
 * rewrite), and a percent-encoded space (`a%20b.png`) is not whitespace and
 * stays valid.
 */
const WHITESPACE = /\s/

/**
 * An HTML entity reference: numeric (`&#169;`, `&#x41;`) or named (`&copy;`).
 * A bare `&` — `A & B`, `Q&A`, a query string — deliberately does not match.
 */
const HTML_ENTITY = /&(?:#\d+|#[xX][0-9a-fA-F]+|[a-zA-Z][a-zA-Z0-9]*);/g

/**
 * The only references that survive: they are the escapes the serializer emits
 * itself, so they come back byte-identical (measured). Exempting them is what
 * keeps the gate from eating its own output — a bare `A & B` is written back as
 * `A &amp; B`, and blocking `&amp;` would lock every file Live Mode has ever
 * saved out of Live Mode. Everything else is left as literal text by marked and
 * then has its ampersand escaped, so `&#169;` becomes `&amp;#169;` and a
 * rendered `©` turns into the visible string `&#169;`.
 */
const SERIALIZER_EMITTED_ENTITIES = new Set(['&amp;', '&lt;', '&gt;'])

function hasCorruptingEntity(raw: string): boolean {
  for (const match of raw.matchAll(HTML_ENTITY)) {
    if (!SERIALIZER_EMITTED_ENTITIES.has(match[0])) return true
  }
  return false
}

/** Shape of the token children marked produces; deliberately structural. */
interface TokenLike {
  type?: string
  raw?: unknown
  href?: unknown
  src?: unknown
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
 * The destination marked resolved for a link or an image, whatever the source
 * form was: `href` is what the lexer fills in for both inline and reference
 * styles, and `src` is accepted as well so a differently-named field never
 * silently reads as "no destination" and passes the gate.
 */
function destinationOf(node: TokenLike): string {
  if (typeof node.href === 'string') return node.href
  if (typeof node.src === 'string') return node.src
  return ''
}

/**
 * Walks the token tree the way marked's own `walkTokens` does: children hang off
 * `tokens` for most nodes, off `items` for lists, and off the cells in `header` /
 * `rows` for tables (a cell's `tokens` is the only place inline content inside a
 * table can be seen).
 *
 * `withinMark` is carried down because whether an image survives depends on
 * where it sits, not on the image itself.
 */
function walk(nodes: unknown[], add: (blocker: string) => void, withinMark = false): void {
  for (const node of nodes) {
    if (!isTokenLike(node)) continue
    const type = typeof node.type === 'string' ? node.type : ''
    if (!WHITELIST.has(type)) add(type || 'unknown')

    // Only a LEAF text token: `code` and `codespan` never reach here, so an
    // entity written inside code — the one place it is literal text on both
    // sides of the round trip — is not mistaken for a broken one. A `text` token
    // that has children (marked wraps a list item's inline content in one)
    // spans those code tokens in its `raw`, so it is left to its children.
    const children = asArray(node.tokens)
    if (type === 'text' && children.length === 0 && typeof node.raw === 'string' && hasCorruptingEntity(node.raw)) {
      add('html-entity')
    }

    if (type === 'image' && withinMark) add('image-in-mark')
    if ((type === 'image' || type === 'link') && WHITESPACE.test(destinationOf(node))) {
      add('bracketed-url')
    }

    const childWithinMark = withinMark || MARK_TOKENS.has(type)
    walk(children, add, childWithinMark)
    walk(asArray(node.items), add, childWithinMark)
    for (const cell of asArray(node.header)) {
      if (isTokenLike(cell)) walk(asArray(cell.tokens), add, childWithinMark)
    }
    for (const row of asArray(node.rows)) {
      for (const cell of asArray(row)) {
        if (isTokenLike(cell)) walk(asArray(cell.tokens), add, childWithinMark)
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
