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

/** A byte count for a label: B under 1 KiB, whole KB under 1 MiB, one decimal MB above. */
export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (Math.round(n / 1024) < 1024) return `${Math.round(n / 1024)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}
