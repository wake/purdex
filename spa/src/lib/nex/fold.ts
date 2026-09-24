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
  /** N2 `output.total_bytes`; absent → the body's UTF-8 byte length. */
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
  /** N2 said the daemon truncated the payload; the view says so either way. */
  daemonTruncated: boolean
}

/** The UTF-8 size of one code point, matching what `utf8Length` counts. */
function utf8Size(codePoint: number): number {
  if (codePoint < 0x80) return 1
  if (codePoint < 0x800) return 2
  if (codePoint < 0x10000) return 3
  return 4
}

/**
 * How many bytes this string occupies in UTF-8 — what N2's `total_bytes` and
 * spec §4.2's 1 KB bar both measure, and what `String.length` does not: 400
 * Han characters are 1200 bytes and 400 UTF-16 units.
 *
 * A code-point walk, not `new TextEncoder().encode(text).length`: `foldPlan`
 * runs on the render path and the encoder allocates a whole second copy of
 * every body it measures. An unpaired surrogate counts as 3, the size of the
 * replacement character a real encoder would emit for it.
 */
export function utf8Length(text: string): number {
  let bytes = 0
  for (let i = 0; i < text.length; ) {
    const cp = text.codePointAt(i) as number
    bytes += utf8Size(cp)
    i += cp > 0xffff ? 2 : 1
  }
  return bytes
}

/**
 * The longest prefix of `line` within both budgets, cut on a code-point
 * boundary. Slicing by UTF-16 index instead would split a surrogate pair and
 * the browser draws a replacement glyph in its place.
 */
function clampLine(line: string, maxChars: number, maxBytes: number): string {
  let chars = 0
  let bytes = 0
  for (let i = 0; i < line.length; ) {
    const cp = line.codePointAt(i) as number
    const size = utf8Size(cp)
    if (chars + 1 > maxChars || bytes + size > maxBytes) return line.slice(0, i)
    chars += 1
    bytes += size
    i += cp > 0xffff ? 2 : 1
  }
  return line
}

/** A single trailing newline terminates the last line, it does not open a new one. */
function splitLines(text: string): string[] {
  if (text.length === 0) return []
  return (text.endsWith('\n') ? text.slice(0, -1) : text).split('\n')
}

export function firstLine(text: string): string {
  const head = text.split('\n', 1)[0] ?? ''
  return clampLine(head, FOLD_LINE_MAX_CHARS, Number.POSITIVE_INFINITY)
}

export function foldPlan(src: FoldSource): FoldPlan {
  const lines = splitLines(src.text)
  const localLines = lines.length
  const totalLines = src.totalLines ?? localLines
  const bytes = src.totalBytes ?? utf8Length(src.text)

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
    const line = lines[i]
    // Both budgets in one walk: FOLD_LINE_MAX_CHARS counts characters, the
    // remaining budget counts UTF-8 bytes, and whichever runs out first cuts
    // the line on a code-point boundary.
    const kept = clampLine(line, FOLD_LINE_MAX_CHARS, budget)
    if (kept !== line) clamped = true
    previewLines.push(kept)
    budget -= utf8Length(kept)
  }

  // Counted against the body we actually have: an affordance must be able to
  // reveal what it promises, whatever N2's total_lines claims.
  const folded = Math.max(0, localLines - previewLines.length)
  // When N2 counts more lines than the body carries, the daemon cut it
  // whatever the flag says.
  const daemonTruncated = src.truncated === true || totalLines > localLines
  // Not `daemonTruncated`: an affordance exists to reveal something, and a
  // body the daemon cut but the preview shows whole has nothing left to
  // reveal. The old clause drew a `+0 lines` button that expanded to the same
  // text (attack A1). The truncation is still reported — `daemonTruncated`
  // stays on the plan and `FoldedOutput` prints its note either way.
  const collapsible = level > 0 && (folded > 0 || clamped)

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
