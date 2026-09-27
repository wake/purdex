// spa/src/lib/nex/markdown-text.test.ts — prose's searchable text is the text
// RoomProse draws, not the markdown source (R3 PR #1492 finding R1-F2 / A2).
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render } from '@testing-library/react'
import RoomProse from '../../components/room/RoomProse'
import { proseText } from './markdown-text'
import { clearSearchHighlights, highlightSearch } from './search-highlight'
import { findMatches } from './transcript-search'

/** What the DOM holds under RoomProse's search anchor. */
function domText(markdown: string): string {
  const { container, unmount } = render(<RoomProse content={markdown} searchUnit="u" />)
  const text = container.querySelector('[data-search-unit="u"]')?.textContent ?? ''
  unmount()
  return text
}

const count = (text: string, query: string) => findMatches([{ id: 'u', text, reveal: [] }], query).matches.length

describe('proseText', () => {
  it('drops a link\'s URL and keeps its label', () => {
    const text = proseText('see [docs](https://needle.dev/x) then needle here')
    expect(text).toBe('see docs then needle here')
    expect(count(text, 'needle')).toBe(1)
  })

  it('drops a code fence\'s language tag and keeps its body', () => {
    const text = proseText('```needle\nconst x = 1\n```')
    expect(count(text, 'needle')).toBe(0)
    expect(text).toContain('const x = 1')
  })

  it('drops an image\'s alt text (the DOM has no text for an img)', () => {
    expect(count(proseText('![a needle](x.png) end'), 'needle')).toBe(0)
  })

  it('an occurrence split by emphasis is one match', () => {
    const text = proseText('nee**dle** and **Needle**')
    expect(text).toBe('needle and Needle')
    expect(count(text, 'needle')).toBe(2)
  })

  it('draws raw HTML as its literal text, like react-markdown 10', () => {
    expect(proseText('a <b>needle</b> b')).toBe(domText('a <b>needle</b> b'))
  })

  it('keeps a paragraph boundary where the DOM has one', () => {
    // mdast-util-to-string would glue these into `needleneedle` and find a
    // match `dlene` the DOM does not have.
    const md = 'first needle\n\nneedle second'
    expect(proseText(md)).toBe(domText(md))
    expect(count(proseText(md), 'dlene')).toBe(0)
  })

  it.each([
    ['link', 'see [docs](https://needle.dev/x) then needle here'],
    ['fence language', 'x\n\n```needle\nneedle()\n```\n\nneedle'],
    ['image alt', '![needle](n.png) needle'],
    ['emphasis', 'nee**dle** and **Needle**'],
    ['raw html', 'a <span title="needle">needle</span> <!-- needle -->'],
    ['entities', 'needle &amp; needle &lt;needle&gt;'],
    ['lists and quotes', '- needle one\n- two needle\n\n> needle quoted\n\n# needle heading'],
    ['inline code', 'call `needle()` then needle'],
    ['hard break', 'needle  \nneedle\\\nneedle'],
    ['autolink', '<https://needle.dev> needle'],
  ])('equals RoomProse\'s DOM text: %s', (_, md) => {
    expect(proseText(md)).toBe(domText(md))
  })
})

describe('prose search against the real RoomProse', () => {
  class FakeHighlight {
    ranges: Range[]
    constructor(...ranges: Range[]) { this.ranges = ranges }
    add(range: Range) { this.ranges.push(range) }
  }
  const g = globalThis as unknown as { CSS?: unknown; Highlight?: unknown }
  let saved: [unknown, unknown]
  let highlights: Map<string, FakeHighlight>
  beforeEach(() => {
    saved = [g.CSS, g.Highlight]
    highlights = new Map()
    g.CSS = { highlights }
    g.Highlight = FakeHighlight
  })
  afterEach(() => {
    ;[g.CSS, g.Highlight] = saved
    clearSearchHighlights('t')
  })

  /** The offset of `range`'s start in `el`'s textContent. */
  function offsetIn(el: Element, range: Range): number {
    const pre = document.createRange()
    pre.selectNodeContents(el)
    pre.setEnd(range.startContainer, range.startOffset)
    return pre.toString().length
  }

  it.each([
    'see [docs](https://needle.dev/x) then needle here, and needle again',
    '```needle\nneedle()\n```\n\nneedle after the fence',
    '![needle](n.png) nee**dle** <i>needle</i> &amp; needle',
  ])('every match is the DOM\'s occurrence at the same place: %s', (md) => {
    const { container } = render(<RoomProse content={md} searchUnit="u" />)
    const el = container.querySelector('[data-search-unit="u"]')!
    const { matches } = findMatches([{ id: 'u', text: proseText(md), reveal: [] }], 'needle')
    const dom = [...(el.textContent ?? '').matchAll(/needle/gi)].map((m) => m.index)
    expect(matches.map((m) => m.start)).toEqual(dom)
    matches.forEach((match, i) => {
      highlightSearch('t', container as HTMLElement, 'needle', matches, i)
      const current = highlights.get('search-current')!.ranges
      expect(current).toHaveLength(1)
      expect(current[0].toString().toLowerCase()).toBe('needle')
      expect(offsetIn(el, current[0])).toBe(match.start)
    })
  })
})
