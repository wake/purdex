// spa/src/lib/nex/format.ts — per-cell text formatting shared by the Nex
// table rows and the sidebar Executions view (spec §4.3).

/** First 12 chars of the 26-char ULID id; the full id lives in `title`. */
export function shortId(id: string): string {
  return id.slice(0, 12)
}

/** First line of the (possibly multi-line, free-text) brief, capped at `max` chars. */
export function firstLine(text: string, max = 80): string {
  const line = text.split('\n', 1)[0] ?? ''
  return line.length > max ? `${line.slice(0, max - 1)}…` : line
}
