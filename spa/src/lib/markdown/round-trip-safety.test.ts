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

/**
 * An image node was added to the schema so `![alt](a.png)` stops degrading to
 * the bare text `alt`. Two forms still do not survive, measured against the real
 * editor, so they are blockers rather than silent rewrites:
 *
 * - an image under a link / strong / em / del: Tiptap models those as MARKS and
 *   a mark cannot wrap a node, so the mark is dropped —
 *   `[![Build](b.svg)](https://ci)` came back as `![Build](b.svg)` (the badge
 *   loses its link), and `[text ![a](a.png)](u)` came back as the broken
 *   `[text ](u)![a](a.png)[`.
 * - an angle-bracketed destination whose URL contains a space: the brackets are
 *   dropped, so `![alt](<a b.png>)` becomes `![alt](a b.png)`, which is not an
 *   image at all to a CommonMark renderer. Links share the defect and the rule.
 */
describe('assessMarkdownRoundTrip — image forms', () => {
  const safe: Array<[string, string]> = [
    ['a bare image', '![alt](a.png)\n'],
    ['an image with a title', '![alt](a.png "title")\n'],
    ['an image with no alt text', '![](a.png)\n'],
    ['an image inside a paragraph', 'See ![alt](a.png) here.\n'],
    ['an image inside a list item', '- ![alt](a.png)\n'],
    ['an image inside a table cell', '| a | b |\n| --- | --- |\n| ![alt](a.png) | 2 |\n'],
    // No space in the destination, so unwrapping the brackets changes nothing.
    ['a bracketed destination with no space', '![alt](<a.png>)\n'],
    ['a plain link next to a plain image', '[link](https://example.com) ![alt](a.png)\n'],
    ['emphasis that contains no image', '**bold** and *em* and ~~del~~\n'],
  ]

  it.each(safe)('%s is safe', (_label, md) => {
    expect(assessMarkdownRoundTrip(md)).toEqual({ safe: true, blockers: [] })
  })

  const nested: Array<[string, string]> = [
    ['a linked image', '[![Build](https://img.shields.io/b.svg)](https://ci.example.com)\n'],
    ['a link mixing text and an image', '[text ![alt](a.png)](https://example.com)\n'],
    ['a bold image', '**![alt](a.png)**\n'],
    ['an emphasised image', '*![alt](a.png)*\n'],
    ['a struck-through image', '~~![alt](a.png)~~\n'],
    ['an image nested two marks deep', '**[![alt](a.png)](https://e.com)**\n'],
  ]

  it.each(nested)('%s is unsafe', (_label, md) => {
    const verdict = assessMarkdownRoundTrip(md)
    expect(verdict.safe).toBe(false)
    expect(verdict.blockers).toContain('image-in-mark')
  })

  const bracketed: Array<[string, string]> = [
    ['an image whose bracketed URL has a space', '![alt](<a b.png>)\n'],
    ['a link whose bracketed URL has a space', '[text](<a b.html>)\n'],
  ]

  it.each(bracketed)('%s is unsafe', (_label, md) => {
    const verdict = assessMarkdownRoundTrip(md)
    expect(verdict.safe).toBe(false)
    expect(verdict.blockers).toContain('bracketed-url')
  })
})
