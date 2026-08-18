// Real-editor round-trip coverage for T2.2a.
//
// Deliberately declares NO tiptap mocks and does not import TiptapEditor.test.tsx
// — vi.mock is file-scoped, so the sibling suite's `@tiptap/react` /
// `@tiptap/starter-kit` mocks do not reach this file. Everything here runs
// against the real ProseMirror schema built from `tiptapExtensions`, which is
// the only way to prove that table / task-list markdown actually survives parse.
import { Editor } from '@tiptap/react'
import { describe, expect, it } from 'vitest'
import { tiptapExtensions } from './tiptapExtensions'
import { normalizeSerializedMarkdown } from '../../lib/markdown/normalize-serialized'

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

  it('round-trips plain prose identically (no regression from the added extensions)', () => {
    const source = '# Title\n\nSome **bold** prose with a [link](https://example.com).\n\n- one\n- two'

    expect(roundTrip(source)).toBe(source)
  })

  // T2.4, measured against the real serializer rather than a fixture: the raw
  // output loses CRLF, the trailing newline and gains a leading one, and
  // `normalizeSerializedMarkdown` is what makes the pair byte-identical again.
  describe('with T2.4 normalization applied', () => {
    const shapeOf = (source: string) => ({
      eol: source.includes('\r\n') ? 'crlf' as const : 'lf' as const,
      trailingNewline: source.endsWith('\n'),
    })

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
  })
})
