// spa/src/lib/nex/search-highlight.test.ts — marking and scrolling to search
// matches (R3 plan T3.2). jsdom has neither the CSS Custom Highlight API nor
// layout, so both are stubbed per test.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { clearSearchHighlights, highlightSearch } from './search-highlight'
import type { SearchMatch } from './transcript-search'

class FakeHighlight {
  ranges: Range[]
  constructor(...ranges: Range[]) {
    this.ranges = ranges
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
  g.CSS = savedCSS
  g.Highlight = savedHighlight
  document.body.innerHTML = ''
  vi.restoreAllMocks()
})

describe('highlightSearch', () => {
  it('marks the current match and the rest', () => {
    installApi()
    // The second unit is markdown: its unit text is `**Needle** and nee**dle**`,
    // the DOM is what ReactMarkdown drew — different offsets, same occurrences.
    const root = dom(
      '<pre data-search-unit="a">one needle</pre>' +
      '<div data-search-unit="b"><p><strong>Needle</strong> and nee<strong>dle</strong></p></div>',
    )
    highlightSearch(root, 'needle', [m('a', 4, 10), m('b', 2, 8), m('b', 17, 25)], 1)
    expect(texts('search-current')).toEqual(['Needle'])
    // The last one crosses two text nodes.
    expect(texts('search-match')).toEqual(['needle', 'needle'])
  })

  it('skips a match whose unit is not on screen', () => {
    installApi()
    const root = dom('<pre data-search-unit="a">needle</pre>')
    highlightSearch(root, 'needle', [m('gone', 0, 6), m('a', 0, 6)], 0)
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
      highlightSearch(scroller, 'needle', [m('a', 4, 10)], 0)
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
    highlightSearch(root, 'needle', [m('a', 0, 6)], 0)
    expect(scroll).toHaveBeenCalledWith({ block: 'center' })
  })

  it('does nothing to highlights without the API', () => {
    removeApi()
    const root = dom('<pre data-search-unit="a">needle</pre>')
    const pre = root.firstElementChild as HTMLElement
    const scroll = vi.fn()
    pre.scrollIntoView = scroll
    expect(() => highlightSearch(root, 'needle', [m('a', 0, 6)], 0)).not.toThrow()
    expect(() => clearSearchHighlights()).not.toThrow()
    // It still takes the reader there.
    expect(scroll).toHaveBeenCalled()
  })

  it('clears both highlights', () => {
    installApi()
    const root = dom('<pre data-search-unit="a">needle needle</pre>')
    highlightSearch(root, 'needle', [m('a', 0, 6), m('a', 7, 13)], 0)
    expect(highlights.size).toBe(2)
    clearSearchHighlights()
    expect(highlights.size).toBe(0)
  })

  it('a query too short to search clears the marks', () => {
    installApi()
    const root = dom('<pre data-search-unit="a">needle</pre>')
    highlightSearch(root, 'needle', [m('a', 0, 6)], 0)
    highlightSearch(root, 'n', [], -1)
    expect(highlights.size).toBe(0)
  })
})
