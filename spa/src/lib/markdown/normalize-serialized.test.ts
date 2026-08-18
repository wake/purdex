import { describe, expect, it } from 'vitest'
import { normalizeSerializedMarkdown } from './normalize-serialized'

const LF = { eol: 'lf', trailingNewline: false } as const
const LF_NL = { eol: 'lf', trailingNewline: true } as const
const CRLF = { eol: 'crlf', trailingNewline: false } as const
const CRLF_NL = { eol: 'crlf', trailingNewline: true } as const

describe('normalizeSerializedMarkdown', () => {
  it('leaves an LF document that already matches its source untouched', () => {
    expect(normalizeSerializedMarkdown('# Title\n\nbody', LF)).toBe('# Title\n\nbody')
    expect(normalizeSerializedMarkdown('# Title\n\nbody\n', LF_NL)).toBe('# Title\n\nbody\n')
  })

  it('converts back to CRLF when the file was loaded as CRLF', () => {
    expect(normalizeSerializedMarkdown('# Title\n\nbody', CRLF)).toBe('# Title\r\n\r\nbody')
  })

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
  // "\n| Name | …". Tiptap cannot represent a leading blank line in the first
  // place (the parser drops it), so a leading newline in the output is always a
  // serializer artifact and never something the source contributed.
  it('strips the leading newline the table serializer prepends', () => {
    expect(normalizeSerializedMarkdown('\n| a | b |\n| --- | --- |\n| 1 | 2 |', LF))
      .toBe('| a | b |\n| --- | --- |\n| 1 | 2 |')
    expect(normalizeSerializedMarkdown('\n\n# Title', LF)).toBe('# Title')
    expect(normalizeSerializedMarkdown('\r\n| a |', CRLF)).toBe('| a |')
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
