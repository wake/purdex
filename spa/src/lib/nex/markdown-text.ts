// spa/src/lib/nex/markdown-text.ts — the text RoomProse puts on screen for a
// markdown source (R3 PR #1492 finding R1-F2). Search indexes agent prose by
// this, not by the source: a link's URL, a code fence's language tag and an
// image's alt are in the source but not on screen, and `nee**dle**` is one
// word on screen but not in the source. search-highlight locates a match by
// its ordinal among the occurrences in the unit element's `textContent`, so
// the index and the DOM must count occurrences the same way.
//
// The same unified pipeline react-markdown 10 runs, up to the hast tree:
// remark-parse → remark-gfm → remark-rehype with `allowDangerousHtml: true`
// (react-markdown always sets it). RoomProse runs GFM (A2, spec §5.3: tables,
// strikethrough, task lists, autolinks), so this mirrors it. Its one rehype
// plugin, rehype-highlight, only wraps code in spans without changing text,
// so it is left out. react-markdown then turns every `raw` node (inline or
// block HTML, comments included) into a text node holding the HTML literally
// — it draws `<b>x</b>` as those characters — so a raw node counts as its
// value. Everything else is the DOM's `textContent`: every text node's value
// in document order, the `\n` text nodes remark-rehype puts between blocks
// included, element properties (href, src, alt) excluded. A GFM table's
// `TABLE_ELEMENTS` whitespace-only text nodes are now reachable (RoomProse
// wraps the `<table>` in a plain `<div>`, which adds no text).
//
// Not mdast-util-to-string: it glues paragraphs with no separator and counts
// an image's alt — both differ from the DOM.
import { unified } from 'unified'
import remarkParse from 'remark-parse'
import remarkGfm from 'remark-gfm'
import remarkRehype from 'remark-rehype'

const processor = unified().use(remarkParse).use(remarkGfm).use(remarkRehype, { allowDangerousHtml: true })

/** The slice of hast this walk reads. */
interface HastNode {
  type: string
  value?: string
  tagName?: string
  children?: HastNode[]
}

/**
 * hast-util-to-jsx-runtime drops whitespace-only text directly inside these
 * (React would warn about it). Markdown without GFM never makes them — raw
 * `<table>` HTML is drawn as text — but the walk follows the renderer anyway.
 */
const TABLE_ELEMENTS = new Set(['table', 'tbody', 'thead', 'tfoot', 'tr'])

function collect(node: HastNode, out: string[]): void {
  if (node.type === 'text' || node.type === 'raw') {
    out.push(node.value ?? '')
    return
  }
  if (node.type !== 'root' && node.type !== 'element') return
  const dropWhitespace = node.type === 'element' && TABLE_ELEMENTS.has(node.tagName ?? '')
  for (const child of node.children ?? []) {
    if (dropWhitespace && child.type === 'text' && !(child.value ?? '').trim()) continue
    collect(child, out)
  }
}

function render(markdown: string): string {
  const tree = processor.runSync(processor.parse(markdown)) as unknown as HastNode
  const out: string[] = []
  collect(tree, out)
  return out.join('')
}

/**
 * Memo by content: the index is rebuilt whenever the transcript grows, and
 * every earlier message's prose is the same string as last time. Bounded so a
 * long session does not keep every revision of a streaming block alive;
 * a hit moves to the back (least recently used goes first).
 */
const CACHE_LIMIT = 2000
const cache = new Map<string, string>()

/** The text RoomProse draws for `markdown` (its anchor's `textContent`). */
export function proseText(markdown: string): string {
  const hit = cache.get(markdown)
  if (hit !== undefined) {
    cache.delete(markdown)
    cache.set(markdown, hit)
    return hit
  }
  const text = render(markdown)
  cache.set(markdown, text)
  if (cache.size > CACHE_LIMIT) cache.delete(cache.keys().next().value as string)
  return text
}
