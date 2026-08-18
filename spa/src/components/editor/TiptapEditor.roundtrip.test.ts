// Real-editor round-trip coverage for T2.2a.
//
// Deliberately declares NO tiptap mocks and does not import TiptapEditor.test.tsx
// — vi.mock is file-scoped, so the sibling suite's `@tiptap/react` /
// `@tiptap/starter-kit` mocks do not reach this file. Everything here runs
// against the real ProseMirror schema built from `tiptapExtensions`, which is
// the only way to prove that table / task-list markdown actually survives parse.
import { Editor } from '@tiptap/react'
import { afterEach, describe, expect, it } from 'vitest'
import { tiptapExtensions } from './tiptapExtensions'
import { normalizeSerializedMarkdown } from '../../lib/markdown/normalize-serialized'
import { useEditorStore } from '../../stores/useEditorStore'

function roundTrip(markdown: string): string {
  const editor = new Editor({
    extensions: tiptapExtensions,
    content: markdown,
    contentType: 'markdown',
  })
  try {
    return editor.getMarkdown()
  } finally {
    editor.destroy()
  }
}

describe('TiptapEditor markdown round-trip (real editor)', () => {
  it('preserves every cell and the alignment row of a GFM table', () => {
    const source = [
      '| Name | Qty | Price |',
      '| --- | :---: | ---: |',
      '| Apple | 3 | 1.50 |',
      '| Banana | 12 | 0.25 |',
    ].join('\n')

    const output = roundTrip(source)

    // Exact string equality is NOT asserted: serialization re-pads columns,
    // rewrites alignment markers and adds surrounding blank lines. That is an
    // accepted style-level rewrite (spec decision 3). Content must survive.
    expect(output).not.toBe('')
    for (const cell of ['Name', 'Qty', 'Price', 'Apple', '3', '1.50', 'Banana', '12', '0.25']) {
      expect(output).toContain(cell)
    }

    const rows = output.split('\n').filter((line) => line.trim().startsWith('|'))
    expect(rows).toHaveLength(4)

    // Alignment row survives, and the per-column alignment is still encoded.
    const alignmentRow = rows[1]
    expect(alignmentRow).toMatch(/^\|[\s:|-]+\|$/)
    const alignmentCells = alignmentRow.split('|').slice(1, -1).map((cell) => cell.trim())
    expect(alignmentCells).toHaveLength(3)
    expect(alignmentCells[1].startsWith(':')).toBe(true) // center
    expect(alignmentCells[1].endsWith(':')).toBe(true)
    expect(alignmentCells[2].endsWith(':')).toBe(true) // right
    expect(alignmentCells[2].startsWith(':')).toBe(false)

    // Cell ordering is preserved row by row.
    const cellsOf = (row: string) => row.split('|').slice(1, -1).map((cell) => cell.trim())
    expect(cellsOf(rows[0])).toEqual(['Name', 'Qty', 'Price'])
    expect(cellsOf(rows[2])).toEqual(['Apple', '3', '1.50'])
    expect(cellsOf(rows[3])).toEqual(['Banana', '12', '0.25'])
  })

  it('re-pads a table only once — the rewrite is idempotent, blank lines do not accumulate', () => {
    // The first pass reformats (column padding + surrounding blank lines). If the
    // rewrite were not a fixed point, every Live Mode edit would grow the file by
    // another blank line, which would turn decision 3's "style-level rewrite" into
    // unbounded drift.
    const source = '# Doc\n\nIntro text.\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n\nAfter text.'

    const first = roundTrip(source)
    const second = roundTrip(first)

    expect(second).toBe(first)
  })

  it('round-trips a task list identically', () => {
    const source = '- [ ] a\n- [x] b'

    expect(roundTrip(source)).toBe(source)
  })

  // Measured during the PR-B adversarial review: with no image node in the
  // schema, `![alt](a.png)` parsed down to the bare text `alt` — the URL was
  // gone before the first keystroke, and the whitelist called it safe. These
  // assert the forms the image node does carry losslessly; the forms it does not
  // (an image under a link/emphasis mark, a bracketed destination) are gate
  // blockers instead and are covered in round-trip-safety.test.ts.
  describe('images', () => {
    it('keeps the URL of a bare image', () => {
      expect(roundTrip('![alt](a.png)')).toBe('![alt](a.png)')
    })

    it('keeps the title attribute', () => {
      expect(roundTrip('![alt](a.png "title")')).toBe('![alt](a.png "title")')
    })

    it('keeps an image that has no alt text', () => {
      expect(roundTrip('![](a.png)')).toBe('![](a.png)')
    })

    it('keeps an image sitting inside a paragraph', () => {
      const source = 'See ![alt](a.png) and ![other](b.png "t") here.'

      expect(roundTrip(source)).toBe(source)
    })

    it('keeps images inside list items', () => {
      const source = '- ![alt](a.png)\n- text with ![other](b.png "t") inline'

      expect(roundTrip(source)).toBe(source)
    })

    it('keeps an image inside a blockquote and a heading', () => {
      expect(roundTrip('> ![alt](a.png)')).toBe('> ![alt](a.png)')
      expect(roundTrip('# ![alt](a.png)')).toBe('# ![alt](a.png)')
    })

    it('keeps an image inside a table cell', () => {
      // Table output is re-padded (accepted style rewrite), so the assertion is
      // on the cell content rather than on the whole string.
      const output = roundTrip('| a | b |\n| --- | --- |\n| ![alt](a.png) | 2 |')

      expect(output).toContain('![alt](a.png)')
    })

    // Evidence for the `bracketed-url` gate blocker, and specifically for its
    // reference-style half. Serialization writes every destination inline and
    // unbracketed, so a definition that relied on angle brackets to carry a
    // space comes back as a URL that is no longer a link or an image to a
    // CommonMark renderer — even though the reference token's own source text
    // (`![alt][img]`) contains neither brackets nor a space for a source-shape
    // rule to notice.
    it('breaks a reference-style destination whose definition needed angle brackets', () => {
      expect(roundTrip('![alt][img]\n\n[img]: <a b.png>')).toBe('![alt](a b.png)')
      expect(roundTrip('[text][ref]\n\n[ref]: <a b.html>')).toBe('[text](a b.html)')
    })

    it('inlines a reference-style destination without a space losslessly', () => {
      expect(roundTrip('![alt][img]\n\n[img]: a.png')).toBe('![alt](a.png)')
    })

    it('leaves a document full of images at a fixed point', () => {
      const source = '# Doc\n\n![one](a.png)\n\nText ![two](b.png "t") text.\n\n- ![three](c.png)'

      expect(roundTrip(roundTrip(source))).toBe(roundTrip(source))
      expect(roundTrip(source)).toBe(source)
    })
  })

  // The evidence behind the `html-entity` gate blocker. Measured, not assumed:
  // the round trip escapes the ampersand of a reference it cannot model, which
  // turns rendered content into literal text.
  describe('HTML entities', () => {
    it('turns a rendered entity into literal text', () => {
      expect(roundTrip('Copyright &#169; 2026')).toBe('Copyright &amp;#169; 2026')
      expect(roundTrip('The letter &#x41;')).toBe('The letter &amp;#x41;')
      expect(roundTrip('Copyright &copy; 2026')).toBe('Copyright &amp;copy; 2026')
    })

    it('returns the escapes it emits itself byte-for-byte', () => {
      // This is why `&amp;` / `&lt;` / `&gt;` are exempt from the blocker rather
      // than swept up with the rest: the serializer writes a bare `A & B` as
      // `A &amp; B`, so blocking them would make every file Live Mode saves
      // ineligible for Live Mode the next time it is opened.
      expect(roundTrip('A & B')).toBe('A &amp; B')
      expect(roundTrip('A &amp; B')).toBe('A &amp; B')
      expect(roundTrip('a &lt; b &gt; c')).toBe('a &lt; b &gt; c')
    })

    it('leaves an entity inside code untouched', () => {
      expect(roundTrip('Write `&copy;` here.')).toBe('Write `&copy;` here.')
      expect(roundTrip('```html\n&copy;\n```')).toBe('```html\n&copy;\n```')
    })
  })

  it('round-trips plain prose identically (no regression from the added extensions)', () => {
    const source = '# Title\n\nSome **bold** prose with a [link](https://example.com).\n\n- one\n- two'

    expect(roundTrip(source)).toBe(source)
  })

  // T2.4, measured against the real serializer rather than a fixture: the raw
  // output loses CRLF, the trailing newline and any leading blank lines, and
  // `normalizeSerializedMarkdown` is what makes the pair byte-identical again.
  describe('with T2.4 normalization applied', () => {
    afterEach(() => {
      useEditorStore.getState().clearAllBuffers()
    })

    // The shape is not recomputed locally: it is read back off the buffer the
    // store recorded at load time, which is the same value EditorPane hands the
    // serializer. A drift between the two would show up here.
    let nextKey = 0
    const shapeOf = (source: string) => {
      const key = `roundtrip-${nextKey++}`
      useEditorStore.getState().openBuffer(key, source, { language: 'markdown' })
      const buffer = useEditorStore.getState().buffers[key]
      return {
        eol: buffer.sourceEol,
        trailingNewline: buffer.sourceTrailingNewline,
        leadingBlankLines: buffer.sourceLeadingBlankLines,
      }
    }

    const normalizedRoundTrip = (source: string) =>
      normalizeSerializedMarkdown(roundTrip(source), shapeOf(source))

    it('returns a CRLF prose file byte-for-byte', () => {
      const source = '# Title\r\n\r\nSome **bold** prose.\r\n\r\n- one\r\n- two\r\n'

      // Without normalization the file would be rewritten to LF on the first edit.
      expect(roundTrip(source)).not.toBe(source)
      expect(normalizedRoundTrip(source)).toBe(source)
    })

    it('stops a table-first file from moving, once its padding is canonical', () => {
      // Column re-padding is an accepted style-level rewrite (decision 3) and is
      // NOT something normalization undoes — so the first pass over a loosely
      // padded table still changes the file. What must not survive that pass is
      // the leading newline: otherwise every edit prepends another blank line.
      const canonical = normalizedRoundTrip('| a | b |\n| --- | --- |\n| 1 | 2 |\n')
      expect(canonical.startsWith('\n')).toBe(false)
      expect(canonical.endsWith('|\n')).toBe(true)

      // From there the document is a fixed point: opening and serializing it
      // again reproduces it byte for byte, so Live Mode shows no spurious diff.
      expect(roundTrip(canonical).startsWith('\n')).toBe(true)
      expect(normalizedRoundTrip(canonical)).toBe(canonical)
    })

    it('keeps a file that never had a trailing newline without one', () => {
      const source = '# Title\n\nbody'

      expect(normalizedRoundTrip(source)).toBe(source)
    })

    it('returns a file that opens with blank lines byte-for-byte', () => {
      const source = '\n\n# Title\n\nbody\n'

      // Measured here, not assumed: the round-trip swallows exactly ONE leading
      // blank line and keeps the rest, so the count that comes out of the
      // serializer says nothing about the count that went in. Only the shape
      // recorded at load time can put the file back.
      expect(roundTrip('\n# Title\n')).toBe('# Title')
      expect(roundTrip(source)).not.toBe(source)
      expect(normalizedRoundTrip(source)).toBe(source)
    })

    // The degenerate end of the same problem, found by the PR-B adversarial
    // review: a file made of nothing but newlines serializes to the empty
    // string, so the recorded shape is all there is to rebuild it from. With the
    // leading count reported as 0 for every such file, `\n\n` came back as a
    // single `\n` — lines the user never touched, gone.
    it.each([['\n'], ['\n\n'], ['\n\n\n'], ['\r\n\r\n'], ['\r\n\r\n\r\n'], ['']])(
      'returns a file of only line endings byte-for-byte (%j)',
      (source) => {
        expect(roundTrip(source)).toBe('')
        expect(normalizedRoundTrip(source)).toBe(source)
      },
    )

    // The whole point of T2.4: merely opening a file in Live Mode must not mark
    // it dirty. This walks the exact path EditorPane's onChange takes.
    it('leaves a blank-line-first file undirty when Live Mode opens it untouched', () => {
      const source = '\n\n# Title\n\nbody\n'
      useEditorStore.getState().openBuffer('leading-blank', source, { language: 'markdown' })
      const buffer = useEditorStore.getState().buffers['leading-blank']

      useEditorStore.getState().updateContent('leading-blank', normalizeSerializedMarkdown(roundTrip(buffer.content), {
        eol: buffer.sourceEol,
        trailingNewline: buffer.sourceTrailingNewline,
        leadingBlankLines: buffer.sourceLeadingBlankLines,
      }))

      expect(useEditorStore.getState().buffers['leading-blank'].content).toBe(source)
      expect(useEditorStore.getState().buffers['leading-blank'].isDirty).toBe(false)
    })
  })
})
