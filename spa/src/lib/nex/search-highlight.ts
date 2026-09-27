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
// **Locating a match.** A match is located by its **ordinal**: the n-th match
// of a unit is the n-th occurrence of the query in the unit element's
// `textContent`, found with the same literal, case-insensitive pattern. That
// holds only while the unit's text and the DOM count occurrences alike: text
// drawn verbatim (user lines, outputs, inputs, diff rows, thinking, paths) is
// its own source, and agent prose is indexed by `proseText` — the text
// RoomProse renders, not the markdown source (markdown-text.ts) — so a link's
// URL or a split `nee**dle**` cannot shift the count. Offsets are not used.
// The index is NFC (A10) but the DOM draws text as it arrived. An element
// whose text is not already NFC is not marked at all — in a unit mixing both
// forms the n-th NFC occurrence in the DOM is not the n-th match, so counting
// would mark the wrong word (A F3) — and its match is still scrolled to by
// its element.
//
// **Owners.** Highlight names are document-wide, so two panes searching at
// once would overwrite — or, clearing, erase — each other's marks. Each caller
// marks under its own `owner` (a pane id): the module keeps every owner's
// ranges and registers the union of them under the one pair of names.
import { searchPattern, type SearchMatch } from './transcript-search'

const MATCH = 'search-match'
const CURRENT = 'search-current'

interface HighlightRegistry {
  set(name: string, highlight: unknown): void
  delete(name: string): void
}

/**
 * Built empty and filled with `add`: spreading ~10^5 Ranges into the
 * constructor overflows the call stack (RangeError), finding A3.
 */
interface HighlightLike {
  add(range: Range): unknown
}
type HighlightCtor = new () => HighlightLike

function highlightApi(): { registry: HighlightRegistry; Highlight: HighlightCtor } | null {
  const g = globalThis as unknown as { CSS?: { highlights?: HighlightRegistry }; Highlight?: HighlightCtor }
  const registry = g.CSS?.highlights
  if (!registry || typeof g.Highlight !== 'function') return null
  return { registry, Highlight: g.Highlight }
}

interface OwnerMarks {
  matches: Range[]
  current: Range | null
}

/** Every owner's marks; the registered highlights are their union. */
const owners = new Map<string, OwnerMarks>()

/** Registers the union of every owner's marks (or removes a name nobody uses). */
function publish(): void {
  const api = highlightApi()
  if (!api) return
  const matches = new api.Highlight()
  const currents = new api.Highlight()
  let anyMatch = false
  let anyCurrent = false
  for (const marks of owners.values()) {
    for (const range of marks.matches) matches.add(range)
    anyMatch ||= marks.matches.length > 0
    if (marks.current) {
      currents.add(marks.current)
      anyCurrent = true
    }
  }
  if (anyMatch) api.registry.set(MATCH, matches)
  else api.registry.delete(MATCH)
  if (anyCurrent) api.registry.set(CURRENT, currents)
  else api.registry.delete(CURRENT)
}

/** Removes `owner`'s marks, keeping every other owner's. Safe without the API. */
export function clearSearchHighlights(owner: string): void {
  if (!owners.delete(owner)) return
  publish()
}

/**
 * The first `count` occurrences of `pattern` in `el`'s text, as Ranges over
 * its text nodes (a match may cross nodes). None when that text is not NFC.
 */
function occurrences(el: Element, pattern: RegExp, count: number): Range[] {
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
  // Not NFC: the ordinals of the index would not line up (see the header).
  if (text !== text.normalize('NFC')) return []
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
    if (ranges.length === count) break
    const range = document.createRange()
    const [sn, so] = at(m.index, false)
    const [en, eo] = at(m.index + m[0].length, true)
    range.setStart(sn, so)
    range.setEnd(en, eo)
    ranges.push(range)
  }
  return ranges
}

/** Overflow values that clip without offering a scrollbar (a `truncate` label). */
const CLIPPING = new Set(['hidden', 'clip', 'visible'])

/**
 * Whether `el` scrolls horizontally: it overflows and its `overflow-x` offers
 * a scrollbar. A box that clips on purpose is left alone — scrolling it would
 * slide its text under the ellipsis.
 */
function scrollsX(el: HTMLElement): boolean {
  if (el.scrollWidth <= el.clientWidth) return false
  return !CLIPPING.has(getComputedStyle(el).overflowX)
}

/**
 * Brings `range` into view in every scrolling box between it and `container`,
 * innermost first: a match deep in a long output sits inside FoldedOutput's
 * own scroll box (`max-h-96 overflow-auto`), which `scrollIntoView` on the
 * element would show only from its top; one far along a long line of a code
 * fence (`overflow-x: auto`) needs the box scrolled sideways too (A6). Each
 * box centres the match on each axis it scrolls on.
 */
function scrollRangeIntoView(range: Range, container: HTMLElement, fallback: Element): void {
  if (typeof range.getBoundingClientRect !== 'function') {
    fallback.scrollIntoView?.({ block: 'center' })
    return
  }
  let el: HTMLElement | null = range.startContainer.parentElement
  while (el) {
    const y = el.scrollHeight > el.clientHeight
    const x = scrollsX(el)
    if (y || x) {
      const r = range.getBoundingClientRect()
      const box = el.getBoundingClientRect()
      if (y && (r.top < box.top || r.bottom > box.bottom)) {
        el.scrollTop += r.top - box.top - (box.height - r.height) / 2
      }
      if (x && (r.left < box.left || r.right > box.right)) {
        el.scrollLeft += r.left - box.left - (box.width - r.width) / 2
      }
    }
    if (el === container) break
    el = el.parentElement
  }
}

/**
 * The first unit drawn at or below the top of `container`'s visible area —
 * the one whose bottom edge is below that top, so a unit cut by it counts —
 * or null when every unit is above it (or nothing is laid out). Where a new
 * search starts (user decision 2026-09-27).
 */
export function firstUnitInView(container: HTMLElement): string | null {
  const top = container.getBoundingClientRect().top
  for (const el of container.querySelectorAll('[data-search-unit]')) {
    if (el.getBoundingClientRect().bottom > top) return el.getAttribute('data-search-unit')
  }
  return null
}

/** The most matches one owner marks at once (the current one included). */
export const SEARCH_MARK_LIMIT = 2000

/** `[lo, hi)`: at most SEARCH_MARK_LIMIT matches, centred on `current` where the ends allow. */
function markWindow(total: number, current: number): [number, number] {
  const centre = current >= 0 && current < total ? current : 0
  const lo = Math.max(0, Math.min(centre - Math.floor(SEARCH_MARK_LIMIT / 2), total - SEARCH_MARK_LIMIT))
  return [lo, Math.min(total, lo + SEARCH_MARK_LIMIT)]
}

/**
 * Marks `matches` under `container` as `owner`'s marks (replacing that owner's
 * earlier ones) and scrolls to `matches[current]`.
 * Matches whose unit is not rendered (still folded) are skipped; the caller
 * expands the current match's `reveal` keys and calls this after that render
 * commits. `current` outside the list marks without scrolling.
 *
 * The marks are Ranges over the text nodes as they are now. When React
 * replaces a text node (new content, a fold toggling, a streaming message
 * growing) its Ranges collapse and the mark silently disappears, so the
 * caller must call this again after every commit that can touch the marked
 * units — the search bar (R3-C2) owns that. Such a re-mark passes
 * `{ scroll: false }`: only moving to a match scrolls, or every new message
 * would drag the reader back to it.
 */
export function highlightSearch(
  owner: string,
  container: HTMLElement,
  query: string,
  matches: readonly SearchMatch[],
  current: number,
  { scroll = true }: { scroll?: boolean } = {},
): void {
  const pattern = searchPattern(query)
  if (!pattern) {
    clearSearchHighlights(owner)
    return
  }

  // Unit id → its element, read off the attribute rather than a selector so
  // an id never has to be CSS-escaped.
  const elements = new Map<string, Element>()
  for (const el of container.querySelectorAll('[data-search-unit]')) {
    const id = el.getAttribute('data-search-unit')
    if (id !== null && !elements.has(id)) elements.set(id, el)
  }

  // Only a window of SEARCH_MARK_LIMIT matches around the current one is
  // marked: only the window's units are searched in the DOM, and each only as
  // far as its last ordinal in the window. A match carries its ordinal within
  // its unit, so a list that starts mid-unit (findMatches past its limit)
  // still locates it.
  const [lo, hi] = markWindow(matches.length, current)
  const needed = new Map<string, number>()
  for (let i = lo; i < hi; i++) {
    const { unitId, ordinal } = matches[i]
    needed.set(unitId, Math.max(needed.get(unitId) ?? 0, ordinal + 1))
  }

  const found = new Map<string, Range[]>()
  const others: Range[] = []
  let currentRange: Range | null = null
  let currentEl: Element | null = null

  for (let i = lo; i < hi; i++) {
    const id = matches[i].unitId
    const el = elements.get(id)
    if (!el) continue
    let ranges = found.get(id)
    if (!ranges) {
      ranges = occurrences(el, pattern, needed.get(id)!)
      found.set(id, ranges)
    }
    const range = ranges[matches[i].ordinal] ?? null
    if (i === current) {
      currentRange = range
      currentEl = el
    } else if (range) {
      others.push(range)
    }
  }

  owners.set(owner, { matches: others, current: currentRange })
  publish()

  if (scroll && currentEl) {
    if (currentRange) scrollRangeIntoView(currentRange, container, currentEl)
    else (currentEl as Element).scrollIntoView?.({ block: 'center' })
  }
}
