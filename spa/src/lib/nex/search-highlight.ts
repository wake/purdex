// spa/src/lib/nex/search-highlight.ts — marks transcript search matches and
// takes the reader to the current one (R3 plan T3.2).
//
// Marking uses the CSS Custom Highlight API: `Range`s registered under
// `search-match` (every match on screen) and `search-current` (the one being
// visited), painted by `::highlight(...)` rules in index.css. Nothing is
// inserted into the DOM, so React's tree is never touched. Where the API is
// missing (jsdom, Safari before 17.2) nothing is marked and the match is still
// scrolled to.
//
// **Locating a match.** A match's `[start, end)` is an offset into the unit's
// *data* text (transcript-search), and the rendered text is not always that
// text: prose is markdown (`**x**` renders as `x`, a code fence loses its
// backticks). So the offsets are not trusted. A match is located by its
// **ordinal** instead — the n-th match of a unit is the n-th occurrence of the
// query in the unit element's `textContent`, found with the same literal,
// case-insensitive pattern. For text drawn verbatim (user lines, outputs,
// inputs, diff rows, thinking) the two agree exactly. For markdown an
// occurrence the syntax breaks (`nee**dle**` in the data is `needle` on screen
// and vice versa) can shift or drop a mark; the unit is then still scrolled to.
//
// Highlight names are document-wide: one search is marked at a time.
import { searchPattern, type SearchMatch } from './transcript-search'

const MATCH = 'search-match'
const CURRENT = 'search-current'

interface HighlightRegistry {
  set(name: string, highlight: unknown): void
  delete(name: string): void
}

type HighlightCtor = new (...ranges: Range[]) => unknown

function highlightApi(): { registry: HighlightRegistry; Highlight: HighlightCtor } | null {
  const g = globalThis as unknown as { CSS?: { highlights?: HighlightRegistry }; Highlight?: HighlightCtor }
  const registry = g.CSS?.highlights
  if (!registry || typeof g.Highlight !== 'function') return null
  return { registry, Highlight: g.Highlight }
}

/** Removes both marks. Safe without the API. */
export function clearSearchHighlights(): void {
  const api = highlightApi()
  if (!api) return
  api.registry.delete(MATCH)
  api.registry.delete(CURRENT)
}

/** Every occurrence of `pattern` in `el`'s text, as Ranges over its text nodes (a match may cross nodes). */
function occurrences(el: Element, pattern: RegExp): Range[] {
  const nodes: Text[] = []
  const starts: number[] = []
  let text = ''
  const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT)
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const t = n as Text
    nodes.push(t)
    starts.push(text.length)
    text += t.data
  }
  // The node holding character `pos`: the last node starting at or before it.
  // `forEnd` resolves a boundary to the node that ends there, not the next one.
  const at = (pos: number, forEnd: boolean): [Text, number] => {
    let i = nodes.length - 1
    while (i > 0 && (forEnd ? starts[i] >= pos : starts[i] > pos)) i--
    return [nodes[i], pos - starts[i]]
  }
  const ranges: Range[] = []
  pattern.lastIndex = 0
  for (const m of text.matchAll(pattern)) {
    const range = document.createRange()
    const [sn, so] = at(m.index, false)
    const [en, eo] = at(m.index + m[0].length, true)
    range.setStart(sn, so)
    range.setEnd(en, eo)
    ranges.push(range)
  }
  return ranges
}

/**
 * Brings `range` into view in every scrolling box between it and `container`,
 * innermost first: a match deep in a long output sits inside FoldedOutput's
 * own scroll box (`max-h-96 overflow-auto`), which `scrollIntoView` on the
 * element would show only from its top. Each box centres the match.
 */
function scrollRangeIntoView(range: Range, container: HTMLElement, fallback: Element): void {
  if (typeof range.getBoundingClientRect !== 'function') {
    fallback.scrollIntoView?.({ block: 'center' })
    return
  }
  let el: HTMLElement | null = range.startContainer.parentElement
  while (el) {
    if (el.scrollHeight > el.clientHeight) {
      const r = range.getBoundingClientRect()
      const box = el.getBoundingClientRect()
      if (r.top < box.top || r.bottom > box.bottom) {
        el.scrollTop += r.top - box.top - (box.height - r.height) / 2
      }
    }
    if (el === container) break
    el = el.parentElement
  }
}

/**
 * Marks `matches` under `container` and scrolls to `matches[current]`.
 * Matches whose unit is not rendered (still folded) are skipped; the caller
 * expands the current match's `reveal` keys and calls this after that render
 * commits. `current` outside the list marks without scrolling.
 */
export function highlightSearch(
  container: HTMLElement,
  query: string,
  matches: readonly SearchMatch[],
  current: number,
): void {
  const pattern = searchPattern(query)
  if (!pattern) {
    clearSearchHighlights()
    return
  }

  // Unit id → its element, read off the attribute rather than a selector so
  // an id never has to be CSS-escaped.
  const elements = new Map<string, Element>()
  for (const el of container.querySelectorAll('[data-search-unit]')) {
    const id = el.getAttribute('data-search-unit')
    if (id !== null && !elements.has(id)) elements.set(id, el)
  }

  const found = new Map<string, Range[]>()
  const seen = new Map<string, number>()
  const others: Range[] = []
  let currentRange: Range | null = null
  let currentEl: Element | null = null

  matches.forEach((match, i) => {
    const ordinal = seen.get(match.unitId) ?? 0
    seen.set(match.unitId, ordinal + 1)
    const el = elements.get(match.unitId)
    if (!el) return
    let ranges = found.get(match.unitId)
    if (!ranges) {
      ranges = occurrences(el, pattern)
      found.set(match.unitId, ranges)
    }
    const range = ranges[ordinal] ?? null
    if (i === current) {
      currentRange = range
      currentEl = el
    } else if (range) {
      others.push(range)
    }
  })

  const api = highlightApi()
  if (api) {
    if (others.length > 0) api.registry.set(MATCH, new api.Highlight(...others))
    else api.registry.delete(MATCH)
    if (currentRange) api.registry.set(CURRENT, new api.Highlight(currentRange))
    else api.registry.delete(CURRENT)
  }

  if (currentEl) {
    if (currentRange) scrollRangeIntoView(currentRange, container, currentEl)
    else (currentEl as Element).scrollIntoView?.({ block: 'center' })
  }
}
