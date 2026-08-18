import { describe, expect, it, vi, afterEach } from 'vitest'
import { Lexer } from 'marked'

import { assessMarkdownRoundTrip } from './round-trip-safety'

/**
 * The gate decides whether a markdown document may open in Live Mode (Tiptap).
 * Tiptap's document model is a lossy subset of markdown and the loss happens at
 * parse time, so anything the gate lets through must survive parse -> serialize.
 */

const safeCases: Array<[string, string]> = [
  ['empty document', ''],
  ['plain prose', 'Hello world.\n\nSecond paragraph.\n'],
  ['headings', '# One\n\n## Two\n\n### Three\n'],
  ['nested lists', '- a\n  - b\n    - c\n\n1. one\n2. two\n   1. two-one\n'],
  ['fenced code', '```js\nconst a = 1\n```\n'],
  ['indented code', 'para\n\n    indented code\n'],
  ['blockquote', '> quoted\n>\n> more\n'],
  ['horizontal rule', 'para\n\n---\n\npara\n'],
  ['links and images', '[link](https://example.com) and ![img](a.png)\n'],
  ['emphasis', 'some *em*, **strong**, `code`, and ~~del~~\n'],
  ['hard break', 'line one  \nline two\n'],
  ['escape', 'a \\* literal asterisk\n'],
  ['gfm table', '| a | b |\n| --- | --- |\n| 1 | 2 |\n'],
  ['task list', '- [ ] todo\n- [x] done\n'],
  ['loose task list', '- [ ] todo\n\n- [x] done\n'],
  // Regression guard: reference-style definitions are rewritten to inline links
  // by the round-trip, which is an accepted style-level change (spec decision 3).
  // Blocking them would push a large share of ordinary documents into raw mode.
  ['reference-style link definition', '[ref]: https://example.com\n\nSee [ref].\n'],
  // Regression guard: `---` followed by a heading is an ordinary rule, NOT front
  // matter. Inferring front matter from an hr+heading token pair misclassifies
  // a very common document shape.
  ['genuine rule followed by a heading', 'intro\n\n---\n\n# Heading\n\nbody\n'],
]

describe('assessMarkdownRoundTrip — safe documents', () => {
  it.each(safeCases)('%s is safe', (_label, md) => {
    expect(assessMarkdownRoundTrip(md)).toEqual({ safe: true, blockers: [] })
  })
})

const unsafeCases: Array<[string, string, string]> = [
  ['raw HTML block', '<div class="x">\n  content\n</div>\n', 'html'],
  ['inline HTML', 'hello <span>there</span>\n', 'html'],
  ['raw HTML inside a table cell', '| a | b |\n| --- | --- |\n| <b>1</b> | 2 |\n', 'html'],
  ['front matter closed by ---', '---\ntitle: x\ntags: [a]\n---\n\n# Body\n', 'frontmatter'],
  ['front matter closed by ...', '---\ntitle: x\n...\n\n# Body\n', 'frontmatter'],
  ['footnote reference plus definition', 'Text[^1] here.\n\n[^1]: the note\n', 'footnote'],
]

describe('assessMarkdownRoundTrip — unsafe documents', () => {
  it.each(unsafeCases)('%s is unsafe with blocker %s', (_label, md, blocker) => {
    const verdict = assessMarkdownRoundTrip(md)
    expect(verdict.safe).toBe(false)
    expect(verdict.blockers).toContain(blocker)
  })
})

describe('assessMarkdownRoundTrip — footnote detection needs both halves', () => {
  it('a bare [^1] reference with no definition is not a footnote blocker', () => {
    expect(assessMarkdownRoundTrip('Text[^1] here.\n').blockers).not.toContain('footnote')
  })

  it('a definition whose label is never referenced is not a footnote blocker', () => {
    expect(assessMarkdownRoundTrip('[^1]: orphan definition\n').blockers).not.toContain(
      'footnote',
    )
  })
})

describe('assessMarkdownRoundTrip — blockers list shape', () => {
  it('deduplicates repeated blockers', () => {
    const verdict = assessMarkdownRoundTrip('<div>a</div>\n\ntext <b>b</b>\n\n<p>c</p>\n')
    expect(verdict.blockers).toEqual(['html'])
  })

  it('reports every distinct blocker in a stable order', () => {
    const md = '---\ntitle: x\n---\n\nText[^1] and <b>raw</b>.\n\n[^1]: note\n'
    const first = assessMarkdownRoundTrip(md)
    expect(first.safe).toBe(false)
    expect(first.blockers).toEqual(['frontmatter', 'footnote', 'html'])
    expect(assessMarkdownRoundTrip(md).blockers).toEqual(first.blockers)
  })
})

describe('assessMarkdownRoundTrip — default-deny', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  // No real markdown lexes to an unknown token today, so the unknown-token path
  // is driven through the lexer seam. This pins fail-closed behaviour for syntax
  // marked may add in a future release.
  it('treats a token type outside the whitelist as a blocker', () => {
    vi.spyOn(Lexer.prototype, 'lex').mockReturnValue([
      { type: 'paragraph', raw: 'x', text: 'x', tokens: [{ type: 'mystery', raw: 'x' }] },
    ] as never)

    const verdict = assessMarkdownRoundTrip('anything')
    expect(verdict.safe).toBe(false)
    expect(verdict.blockers).toEqual(['mystery'])
  })

  it('walks list items, which hang off `items` rather than `tokens`', () => {
    vi.spyOn(Lexer.prototype, 'lex').mockReturnValue([
      {
        type: 'list',
        raw: '- x',
        items: [{ type: 'list_item', raw: 'x', task: false, tokens: [{ type: 'mystery' }] }],
      },
    ] as never)

    expect(assessMarkdownRoundTrip('- x').blockers).toEqual(['mystery'])
  })
})

describe('assessMarkdownRoundTrip — purity', () => {
  it('returns the same verdict for the same input across calls', () => {
    const md = '# Title\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n\n- [x] done\n'
    expect(assessMarkdownRoundTrip(md)).toEqual(assessMarkdownRoundTrip(md))
  })

  it('hands back a fresh blockers array each call', () => {
    const unsafe = assessMarkdownRoundTrip('<div>x</div>')
    unsafe.blockers.push('tampered')
    expect(assessMarkdownRoundTrip('<div>x</div>').blockers).toEqual(['html'])
  })
})

/**
 * Front matter has to be spotted in the source text because marked lexes it as
 * an `hr` plus whatever the YAML happens to look like. Detection therefore has
 * to separate a metadata block from the many ordinary documents that also open
 * with a `---` line — a thematic break, or the underline of a setext heading.
 * Getting that wrong locks a perfectly round-trippable file out of Live Mode.
 */
describe('assessMarkdownRoundTrip — front matter detection', () => {
  const notFrontMatter: Array<[string, string]> = [
    // The most direct false positive: a rule, a line of prose, and a setext
    // underline. Nothing inside the fence looks like metadata.
    ['a rule around a line of prose', '---\nhello\n---\n'],
    ['an empty fence', '---\n---\n'],
    ['an opening rule that is never closed', '---\n\nhello world\n\nmore prose\n'],
    ['prose whose only colon belongs to a URL', '---\nhttps://example.com\n---\n'],
  ]

  it.each(notFrontMatter)('%s is safe', (_label, md) => {
    expect(assessMarkdownRoundTrip(md)).toEqual({ safe: true, blockers: [] })
  })

  const isFrontMatter: Array<[string, string]> = [
    ['a single key: value pair', '---\ntitle: x\n---\n\n# Body\n'],
    ['nested keys and sequence items', '---\ntitle: x\ntags:\n  - a\n  - b\n---\n\n# Body\n'],
    ['a valueless key', '---\ndraft:\n---\n\n# Body\n'],
    ['a fence closed by ...', '---\ntitle: x\n...\n\n# Body\n'],
  ]

  it.each(isFrontMatter)('%s is unsafe', (_label, md) => {
    const verdict = assessMarkdownRoundTrip(md)
    expect(verdict.safe).toBe(false)
    expect(verdict.blockers).toContain('frontmatter')
  })
})
