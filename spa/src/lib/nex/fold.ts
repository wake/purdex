// spa/src/lib/nex/fold.ts — spec §4.2's one folding rule, for every block
// type. Pure: no React, no store. Driven by what N2 already sends
// (output.total_lines / total_bytes / truncated) and falling back to the
// body itself when a block has no N2 overlay.
export const FOLD_WHOLE_MAX_LINES = 6
export const FOLD_WHOLE_MAX_BYTES = 1024
export const FOLD_MEDIUM_MAX_LINES = 40
export const FOLD_PREVIEW_MEDIUM = 6
export const FOLD_PREVIEW_LARGE = 3
/** A preview line longer than this is cut; the cut counts as hidden content. */
export const FOLD_LINE_MAX_CHARS = 400

export type FoldSeverity = 'normal' | 'error'

export interface FoldSource {
  text: string
  /** N2 `output.total_lines`; absent → counted from `text`. */
  totalLines?: number
  /** N2 `output.total_bytes`; absent → `text.length`. */
  totalBytes?: number
  /** N2 `output.truncated` — the daemon itself cut the payload at 8 KB. */
  truncated?: boolean
  /** `error` and `denied` fold one step less (spec §4.2). */
  severity?: FoldSeverity
}

export interface FoldPlan {
  /** Lines to show while collapsed; empty when the body is shown whole. */
  previewLines: string[]
  /** The body's true line count (N2's when it has one). */
  totalLines: number
  /**
   * How much this affordance is hiding: the lines the body carries minus the
   * ones the preview shows, and exactly 0 when the body is shown whole.
   */
  hiddenLines: number
  /** At least one preview line was cut at FOLD_LINE_MAX_CHARS. */
  clamped: boolean
  /** false → render the body whole, draw no affordance. */
  collapsible: boolean
  /** N2 said the daemon truncated the payload; the expanded view says so. */
  daemonTruncated: boolean
}

/** A single trailing newline terminates the last line, it does not open a new one. */
function splitLines(text: string): string[] {
  if (text.length === 0) return []
  return (text.endsWith('\n') ? text.slice(0, -1) : text).split('\n')
}

export function firstLine(text: string): string {
  const head = text.split('\n', 1)[0] ?? ''
  return head.length > FOLD_LINE_MAX_CHARS ? head.slice(0, FOLD_LINE_MAX_CHARS) : head
}

export function foldPlan(src: FoldSource): FoldPlan {
  const lines = splitLines(src.text)
  const localLines = lines.length
  const totalLines = src.totalLines ?? localLines
  const bytes = src.totalBytes ?? src.text.length

  let level = src.truncated || totalLines > FOLD_MEDIUM_MAX_LINES
    ? 2
    : totalLines > FOLD_WHOLE_MAX_LINES || bytes > FOLD_WHOLE_MAX_BYTES
      ? 1
      : 0
  // An error or a denial folds one step less than the table says: a failure
  // you have to expand is a failure you will miss (spec §4.2).
  if (src.severity === 'error' && level > 0) level -= 1

  const take = level === 2 ? FOLD_PREVIEW_LARGE : level === 1 ? FOLD_PREVIEW_MEDIUM : 0

  // The preview has a byte budget as well as a line budget: three 350-char
  // lines are over the spec's 1 KB bar while under its 6-line bar.
  const previewLines: string[] = []
  let clamped = false
  let budget = FOLD_WHOLE_MAX_BYTES
  for (let i = 0; i < take && i < localLines; i++) {
    if (budget <= 0) break
    let line = lines[i]
    if (line.length > FOLD_LINE_MAX_CHARS) {
      line = line.slice(0, FOLD_LINE_MAX_CHARS)
      clamped = true
    }
    if (line.length > budget) {
      line = line.slice(0, budget)
      clamped = true
    }
    previewLines.push(line)
    budget -= line.length
  }

  // Counted against the body we actually have: an affordance must be able to
  // reveal what it promises, whatever N2's total_lines claims.
  const folded = Math.max(0, localLines - previewLines.length)
  // When N2 counts more lines than the body carries, the daemon cut it
  // whatever the flag says.
  const daemonTruncated = src.truncated === true || totalLines > localLines
  const collapsible = level > 0 && (folded > 0 || clamped || daemonTruncated)

  return {
    previewLines: collapsible ? previewLines : [],
    totalLines,
    // A body shown whole hides nothing. Without this the formula would hand
    // back the body's own line count (the preview is empty there), which any
    // consumer that renders the count before checking `collapsible` shows as
    // `+3 lines` on a three-line body it is already showing in full.
    hiddenLines: collapsible ? folded : 0,
    clamped,
    collapsible,
    daemonTruncated,
  }
}
