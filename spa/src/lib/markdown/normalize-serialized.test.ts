import { describe, expect, it } from 'vitest'
import { normalizeSerializedMarkdown } from './normalize-serialized'

const LF = { eol: 'lf', trailingNewline: false, leadingBlankLines: 0 } as const
const LF_NL = { eol: 'lf', trailingNewline: true, leadingBlankLines: 0 } as const
const CRLF = { eol: 'crlf', trailingNewline: false, leadingBlankLines: 0 } as const
const CRLF_NL = { eol: 'crlf', trailingNewline: true, leadingBlankLines: 0 } as const

describe('normalizeSerializedMarkdown', () => {
  it('leaves an LF document that already matches its source untouched', () => {
    expect(normalizeSerializedMarkdown('# Title\n\nbody', LF)).toBe('# Title\n\nbody')
    expect(normalizeSerializedMarkdown('# Title\n\nbody\n', LF_NL)).toBe('# Title\n\nbody\n')
  })

  it('converts back to CRLF when the file was loaded as CRLF', () => {
    expect(normalizeSerializedMarkdown('# Title\n\nbody', CRLF)).toBe('# Title\r\n\r\nbody')
  })

  // One `eol` value can only produce one line ending, which is exactly why a
  // file that MIXES them never reaches this function: `assessMarkdownRoundTrip`
  // reports `mixed-eol` and the file opens raw, where Monaco is byte-faithful.
  // Here the mixture can only come from the serializer, and collapsing it to the
  // source's ending is the correct outcome.
  it('does not double up CRLF if the serializer already emitted some', () => {
    expect(normalizeSerializedMarkdown('a\r\nb\nc', CRLF)).toBe('a\r\nb\r\nc')
  })

  it('strips CRLF back to LF for an LF source', () => {
    expect(normalizeSerializedMarkdown('a\r\nb', LF)).toBe('a\nb')
  })

  it('restores exactly one trailing newline, never two', () => {
    expect(normalizeSerializedMarkdown('body', LF_NL)).toBe('body\n')
    expect(normalizeSerializedMarkdown('body\n', LF_NL)).toBe('body\n')
    expect(normalizeSerializedMarkdown('body\n\n\n', LF_NL)).toBe('body\n')
    expect(normalizeSerializedMarkdown('body', CRLF_NL)).toBe('body\r\n')
    expect(normalizeSerializedMarkdown('body\n\n', CRLF_NL)).toBe('body\r\n')
  })

  it('removes a trailing newline the source did not have', () => {
    expect(normalizeSerializedMarkdown('body\n', LF)).toBe('body')
    expect(normalizeSerializedMarkdown('body\n\n', LF)).toBe('body')
    expect(normalizeSerializedMarkdown('body\r\n', CRLF)).toBe('body')
  })

  // Measured in T2.2a: a document that starts with a table serializes as
  // "\n| Name | …" even though its source had no blank first line. The leading
  // newlines the serializer emits carry no information — what the file had is
  // recorded on the buffer instead, and restored below.
  it('strips the leading newline the table serializer prepends', () => {
    expect(normalizeSerializedMarkdown('\n| a | b |\n| --- | --- |\n| 1 | 2 |', LF))
      .toBe('| a | b |\n| --- | --- |\n| 1 | 2 |')
    expect(normalizeSerializedMarkdown('\n\n# Title', LF)).toBe('# Title')
    expect(normalizeSerializedMarkdown('\r\n| a |', CRLF)).toBe('| a |')
  })

  // Tiptap drops a leading blank line at parse time, so the serializer can never
  // reproduce one. That is a fact about the editor, not about the file: a source
  // that opened with blank lines still has them, and dropping them would make
  // such a file impossible to round-trip losslessly.
  describe('leading blank lines the source actually had', () => {
    const lead = (count: number) => ({ eol: 'lf', trailingNewline: false, leadingBlankLines: count } as const)

    it('restores a single leading blank line', () => {
      expect(normalizeSerializedMarkdown('# Title\n\nbody', lead(1))).toBe('\n# Title\n\nbody')
    })

    it('restores exactly as many as the source had', () => {
      expect(normalizeSerializedMarkdown('# Title', lead(3))).toBe('\n\n\n# Title')
    })

    it('never doubles up with the newlines the serializer emitted', () => {
      expect(normalizeSerializedMarkdown('\n\n# Title', lead(1))).toBe('\n# Title')
      expect(normalizeSerializedMarkdown('\n| a |', lead(2))).toBe('\n\n| a |')
    })

    it('re-encodes the restored blank lines for a CRLF source', () => {
      expect(normalizeSerializedMarkdown('body', { eol: 'crlf', trailingNewline: true, leadingBlankLines: 2 }))
        .toBe('\r\n\r\nbody\r\n')
    })
  })

  it('keeps a document that is only whitespace from collapsing into a stray newline', () => {
    expect(normalizeSerializedMarkdown('', LF)).toBe('')
    expect(normalizeSerializedMarkdown('\n', LF)).toBe('')
    // An emptied buffer whose file ended with a newline stays a single newline —
    // the same shape `openBuffer` would record for that file.
    expect(normalizeSerializedMarkdown('', LF_NL)).toBe('\n')
    expect(normalizeSerializedMarkdown('\n\n', CRLF_NL)).toBe('\r\n')
  })
})
