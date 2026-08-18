// spa/src/lib/markdown/normalize-serialized.ts
//
// Spec 2.4. Tiptap's markdown serializer emits one canonical shape regardless of
// what the file looked like: LF line endings, no trailing newline, and a leading
// newline in front of a document that starts with a table (measured in T2.2a:
// `"\n| Name | …"`). Writing that back means a single Live Mode edit rewrites
// every line ending in the file — a whole-file diff for a one-word change.
//
// This restores the shape recorded when the file was loaded
// (`sourceEol` / `sourceTrailingNewline` on the buffer). It is applied at the
// point the serializer produces text, before the content reaches the store, so
// `isDirty` compares like with like. The raw (Monaco) path needs none of this:
// Monaco hands back exactly the text in its model.

/** The shape of the file as it was loaded — see `EditorBuffer.sourceEol`. */
export interface SerializedSourceShape {
  eol: 'lf' | 'crlf'
  trailingNewline: boolean
}

/**
 * Pure: same input, same output, no reliance on the buffer or the editor.
 *
 * The leading newline is stripped unconditionally rather than compared against
 * the source, because Tiptap cannot represent a leading blank line in the first
 * place — the parser drops it — so one in the output is always an artifact of
 * serialization and never content the source contributed.
 */
export function normalizeSerializedMarkdown(serialized: string, source: SerializedSourceShape): string {
  // Work in LF space so the rules below never have to see a mixed document.
  const body = serialized
    .replace(/\r\n/g, '\n')
    .replace(/^\n+/, '')
    .replace(/\n+$/, '')

  const withTrailing = source.trailingNewline ? `${body}\n` : body
  return source.eol === 'crlf' ? withTrailing.replace(/\n/g, '\r\n') : withTrailing
}
