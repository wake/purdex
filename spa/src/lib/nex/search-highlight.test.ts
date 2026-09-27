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

const m = (unitId: string, start: number, end: number): SearchMatch => ({ unitId, start, end, reveal: [] })
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

  it('falls back to scrolling the unit into view without layout', () => {
    installApi()
    const root = dom('<pre data-search-unit="a">needle</pre>')
    const pre = root.firstElementChild as HTMLElement
    const scroll = vi.fn()
    pre.scrollIntoView = scroll
    highlightSearch('p1', root, 'needle', [m('a', 0, 6)], 0)
    expect(scroll).toHaveBeenCalledWith({ block: 'center' })
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
    highlightSearch('p1', root, 'needle', [m('a', 0, 6), m('a', 7, 13)], 0)
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
    highlightSearch('p1', one, 'needle', [m('a', 6, 12), m('a', 13, 19)], 0)
    highlightSearch('p2', two, 'needle', [m('a', 5, 11), m('a', 12, 18)], 1)
    const owners = (name: string) => (highlights.get(name)?.ranges ?? [])
      .map((r) => (r.startContainer.textContent ?? '').split(' ')[0]).sort()
    expect(owners('search-current')).toEqual(['alpha', 'beta'])
    expect(owners('search-match')).toEqual(['alpha', 'beta'])

    clearSearchHighlights('p1')
    expect(owners('search-current')).toEqual(['beta'])
    expect(owners('search-match')).toEqual(['beta'])

    // Re-marking one owner replaces only its own ranges.
    highlightSearch('p2', two, 'needle', [m('a', 5, 11), m('a', 12, 18)], 0)
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
