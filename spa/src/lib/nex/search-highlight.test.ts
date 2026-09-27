// spa/src/lib/nex/search-highlight.test.ts — marking and scrolling to search
// matches (R3 plan T3.2). jsdom has neither the CSS Custom Highlight API nor
// layout, so both are stubbed per test.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { clearSearchHighlights, highlightSearch, SEARCH_MARK_LIMIT } from './search-highlight'
import { findMatches, type SearchMatch } from './transcript-search'
import { proseText } from './markdown-text'

class FakeHighlight {
  ranges: Range[]
  constructor(...ranges: Range[]) {
    this.ranges = ranges
  }
  add(range: Range) {
    this.ranges.push(range)
    return this
  }
}

const g = globalThis as unknown as { CSS?: unknown; Highlight?: unknown }
let savedCSS: unknown
let savedHighlight: unknown
let highlights: Map<string, FakeHighlight>

function installApi() {
  highlights = new Map()
  g.CSS = { highlights }
  g.Highlight = FakeHighlight
}

function removeApi() {
  g.CSS = undefined
  g.Highlight = undefined
}

function dom(html: string): HTMLElement {
  const root = document.createElement('div')
  root.innerHTML = html
  document.body.appendChild(root)
  return root
}

const m = (unitId: string, start: number, end: number, ordinal = 0): SearchMatch => ({ unitId, start, end, ordinal, reveal: [] })
const texts = (name: string) => (highlights.get(name)?.ranges ?? []).map((r) => r.toString())

beforeEach(() => {
  savedCSS = g.CSS
  savedHighlight = g.Highlight
})

afterEach(() => {
  // Owners are module state: forget every one a test used.
  clearSearchHighlights('p1')
  clearSearchHighlights('p2')
  g.CSS = savedCSS
  g.Highlight = savedHighlight
  document.body.innerHTML = ''
  vi.restoreAllMocks()
})

describe('highlightSearch', () => {
  it('marks the current match and the rest', () => {
    installApi()
    // The second unit is markdown: the index holds its rendered text (proseText),
    // the DOM is what ReactMarkdown drew for the same source.
    const root = dom(
      '<pre data-search-unit="a">one needle</pre>' +
      '<div data-search-unit="b"><p><strong>Needle</strong> and nee<strong>dle</strong></p></div>',
    )
    const { matches } = findMatches([
      { id: 'a', text: 'one needle', reveal: [] },
      { id: 'b', text: proseText('**Needle** and nee**dle**'), reveal: [] },
    ], 'needle')
    expect(matches).toHaveLength(3)
    highlightSearch('p1', root, 'needle', matches, 1)
    expect(texts('search-current')).toEqual(['Needle'])
    // The last one crosses two text nodes.
    expect(texts('search-match')).toEqual(['needle', 'needle'])
  })

  it('skips a match whose unit is not on screen', () => {
    installApi()
    const root = dom('<pre data-search-unit="a">needle</pre>')
    highlightSearch('p1', root, 'needle', [m('gone', 0, 6), m('a', 0, 6)], 0)
    expect(highlights.has('search-current')).toBe(false)
    expect(texts('search-match')).toEqual(['needle'])
  })

  it('scrolls the current match into view', () => {
    installApi()
    const root = dom('<div data-testid="scroller"><pre data-search-unit="a">top\nneedle</pre></div>')
    const scroller = root.firstElementChild as HTMLElement
    const pre = scroller.firstElementChild as HTMLElement
    // The pre scrolls inside the transcript's own scroller (FoldedOutput's max-h-96).
    for (const [el, height] of [[scroller, 300], [pre, 100]] as const) {
      Object.defineProperty(el, 'scrollHeight', { configurable: true, value: 2000 })
      Object.defineProperty(el, 'clientHeight', { configurable: true, value: height })
      el.getBoundingClientRect = () => ({ top: 0, bottom: height, height } as DOMRect)
    }
    const rangeRect = vi.fn(() => ({ top: 500, bottom: 510, height: 10 } as DOMRect))
    Range.prototype.getBoundingClientRect = rangeRect
    try {
      highlightSearch('p1', scroller, 'needle', [m('a', 4, 10)], 0)
    } finally {
      delete (Range.prototype as { getBoundingClientRect?: unknown }).getBoundingClientRect
    }
    // The inner box first, then the transcript: each centres the match.
    expect(pre.scrollTop).toBe(500 - (100 - 10) / 2)
    expect(scroller.scrollTop).toBe(500 - (300 - 10) / 2)
  })

  // R3-C2: the search bar re-marks after every commit (A8) and must not drag
  // the reader back to the match each time — it scrolls only when moving.
  it('marks the current match without scrolling when told not to', () => {
    installApi()
    const root = dom('<div><pre data-search-unit="a">top\nneedle</pre></div>')
    const scroller = root.firstElementChild as HTMLElement
    Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: 2000 })
    Object.defineProperty(scroller, 'clientHeight', { configurable: true, value: 300 })
    scroller.getBoundingClientRect = () => ({ top: 0, bottom: 300, height: 300 } as DOMRect)
    const intoView = vi.fn()
    Element.prototype.scrollIntoView = intoView
    Range.prototype.getBoundingClientRect = () => ({ top: 500, bottom: 510, height: 10 } as DOMRect)
    try {
      highlightSearch('p1', scroller, 'needle', [m('a', 4, 10)], 0, { scroll: false })
    } finally {
      delete (Range.prototype as { getBoundingClientRect?: unknown }).getBoundingClientRect
      delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView
    }
    expect(texts('search-current')).toEqual(['needle'])
    expect(scroller.scrollTop).toBe(0)
    expect(intoView).not.toHaveBeenCalled()
  })

  it('scrolls a horizontally scrolling box to the current match too', () => {
    // Finding A6: prose code fences are overflow-x:auto; a match far along a
    // long line was scrolled to vertically and stayed out of sight.
    installApi()
    const root = dom(
      '<div data-search-unit="a"><pre style="overflow-x: auto"><code>' + 'x'.repeat(200) + ' needle</code></pre>' +
      // overflow-x, not the shorthand: jsdom does not expand `overflow` into it.
      '<p style="overflow-x: hidden">clipped needle</p></div>',
    )
    const [pre, clipped] = [root.querySelector('pre')!, root.querySelector('p')!]
    for (const el of [pre, clipped]) {
      Object.defineProperty(el, 'scrollWidth', { configurable: true, value: 2000 })
      Object.defineProperty(el, 'clientWidth', { configurable: true, value: 200 })
      el.getBoundingClientRect = () => ({ top: 0, bottom: 20, height: 20, left: 0, right: 200, width: 200 } as DOMRect)
    }
    Range.prototype.getBoundingClientRect = () => ({ top: 5, bottom: 15, height: 10, left: 900, right: 910, width: 10 } as DOMRect)
    try {
      highlightSearch('p1', root, 'needle', [m('a', 0, 6), m('a', 0, 6, 1)], 0)
      expect(pre.scrollLeft).toBe(900 - (200 - 10) / 2)
      // Inside the box already: left alone.
      pre.scrollLeft = 0
      Range.prototype.getBoundingClientRect = () => ({ top: 5, bottom: 15, height: 10, left: 50, right: 60, width: 10 } as DOMRect)
      highlightSearch('p1', root, 'needle', [m('a', 0, 6), m('a', 0, 6, 1)], 0)
      expect(pre.scrollLeft).toBe(0)
      // A box that clips on purpose (overflow hidden, e.g. truncate) is not scrolled.
      Range.prototype.getBoundingClientRect = () => ({ top: 5, bottom: 15, height: 10, left: 900, right: 910, width: 10 } as DOMRect)
      highlightSearch('p1', root, 'needle', [m('a', 0, 6), m('a', 0, 6, 1)], 1)
      expect(clipped.scrollLeft).toBe(0)
    } finally {
      delete (Range.prototype as { getBoundingClientRect?: unknown }).getBoundingClientRect
    }
  })

  it('falls back to scrolling the unit into view without layout', () => {
    installApi()
    const root = dom('<pre data-search-unit="a">needle</pre>')
    const pre = root.firstElementChild as HTMLElement
    const scroll = vi.fn()
    pre.scrollIntoView = scroll
    highlightSearch('p1', root, 'needle', [m('a', 0, 6)], 0)
    expect(scroll).toHaveBeenCalledWith({ block: 'center' })
  })

  // A F3: the index is NFC, the DOM is as it arrived. In a unit mixing both
  // forms the n-th NFC occurrence in the DOM is not the n-th match, so the
  // element is not marked at all — only scrolled to.
  it('an element whose text is not NFC is scrolled to, not marked', () => {
    installApi()
    const nfc = 'café'
    const nfd = 'café'
    const root = dom(`<pre data-search-unit="a">${nfd} then ${nfc}</pre><pre data-search-unit="b">${nfc}</pre>`)
    const pre = root.firstElementChild as HTMLElement
    const scroll = vi.fn()
    pre.scrollIntoView = scroll
    const { matches } = findMatches([
      { id: 'a', text: `${nfd} then ${nfc}`.normalize('NFC'), reveal: [] },
      { id: 'b', text: nfc, reveal: [] },
    ], nfc)
    expect(matches).toHaveLength(3)
    highlightSearch('p1', root, nfc, matches, 0)
    // Not the second word marked as if it were the first.
    expect(texts('search-current')).toEqual([])
    expect(scroll).toHaveBeenCalledWith({ block: 'center' })
    // Other units are marked as usual.
    expect(texts('search-match')).toEqual([nfc])
  })

  it('does nothing to highlights without the API', () => {
    removeApi()
    const root = dom('<pre data-search-unit="a">needle</pre>')
    const pre = root.firstElementChild as HTMLElement
    const scroll = vi.fn()
    pre.scrollIntoView = scroll
    expect(() => highlightSearch('p1', root, 'needle', [m('a', 0, 6)], 0)).not.toThrow()
    expect(() => clearSearchHighlights('p1')).not.toThrow()
    // It still takes the reader there.
    expect(scroll).toHaveBeenCalled()
  })

  it('clears both highlights', () => {
    installApi()
    const root = dom('<pre data-search-unit="a">needle needle</pre>')
    highlightSearch('p1', root, 'needle', [m('a', 0, 6), m('a', 7, 13, 1)], 0)
    expect(highlights.size).toBe(2)
    clearSearchHighlights('p1')
    expect(highlights.size).toBe(0)
  })

  it('two owners mark at once, and clearing one keeps the other', () => {
    // CSS highlight names are document-wide; two panes searching must not
    // overwrite or clear each other (finding R1-F3 / A1).
    installApi()
    const one = dom('<pre data-search-unit="a">alpha needle needle</pre>')
    const two = dom('<pre data-search-unit="a">beta needle needle</pre>')
    highlightSearch('p1', one, 'needle', [m('a', 6, 12), m('a', 13, 19, 1)], 0)
    highlightSearch('p2', two, 'needle', [m('a', 5, 11), m('a', 12, 18, 1)], 1)
    const owners = (name: string) => (highlights.get(name)?.ranges ?? [])
      .map((r) => (r.startContainer.textContent ?? '').split(' ')[0]).sort()
    expect(owners('search-current')).toEqual(['alpha', 'beta'])
    expect(owners('search-match')).toEqual(['alpha', 'beta'])

    clearSearchHighlights('p1')
    expect(owners('search-current')).toEqual(['beta'])
    expect(owners('search-match')).toEqual(['beta'])

    // Re-marking one owner replaces only its own ranges.
    highlightSearch('p2', two, 'needle', [m('a', 5, 11), m('a', 12, 18, 1)], 0)
    expect(texts('search-current')).toEqual(['needle'])
    expect(texts('search-match')).toEqual(['needle'])
    clearSearchHighlights('p2')
    expect(highlights.size).toBe(0)
  })

  it('marks 150,000 matches without throwing, replacing the old marks', () => {
    // Finding A3: spreading ~10^5 Ranges into the Highlight constructor threw
    // a RangeError and left the previous search's marks behind.
    installApi()
    const old = dom('<pre data-search-unit="old">needle</pre>')
    highlightSearch('p1', old, 'needle', [m('old', 0, 6)], 0)
    const text = 'ab '.repeat(150_000)
    const root = dom(`<pre data-search-unit="u">${text}</pre>`)
    const { matches } = findMatches([{ id: 'u', text, reveal: [] }], 'ab', 200_000)
    expect(matches).toHaveLength(150_000)
    expect(() => highlightSearch('p1', root, 'ab', matches, 0)).not.toThrow()
    const all = [...texts('search-match'), ...texts('search-current')]
    expect(all.length).toBeLessThanOrEqual(SEARCH_MARK_LIMIT)
    expect(all.every((t) => t === 'ab')).toBe(true)
  })

  it('marks at most the limit, in a window that holds the current match', () => {
    installApi()
    const text = 'ab '.repeat(5000)
    const root = dom(`<pre data-search-unit="u">${text}</pre>`)
    const { matches } = findMatches([{ id: 'u', text, reveal: [] }], 'ab')
    const offset = (r: Range) => r.startOffset
    for (const current of [0, 4000, 4999]) {
      highlightSearch('p1', root, 'ab', matches, current)
      const cur = highlights.get('search-current')!.ranges
      expect(cur.map(offset)).toEqual([matches[current].start])
      const others = highlights.get('search-match')!.ranges
      expect(others.length + 1).toBe(SEARCH_MARK_LIMIT)
      // A contiguous window around the current match.
      const starts = others.map(offset).concat(cur.map(offset)).sort((x, y) => x - y)
      const first = matches.findIndex((mt) => mt.start === starts[0])
      expect(starts).toEqual(matches.slice(first, first + SEARCH_MARK_LIMIT).map((mt) => mt.start))
    }
  })

  it('a query too short to search clears the marks', () => {
    installApi()
    const root = dom('<pre data-search-unit="a">needle</pre>')
    highlightSearch('p1', root, 'needle', [m('a', 0, 6)], 0)
    highlightSearch('p1', root, 'n', [], -1)
    expect(highlights.size).toBe(0)
  })
})
