// spa/src/lib/nex/diff-lines.ts — unified-hunk line numbering (P-B3 spec §4.4 R5).
//
// Pure helper for ToolDiffView: walks a hunk's lines from oldStart / newStart
// and assigns each row its old / new line number. ` ` advances both, `-` old,
// `+` new, `\` (no-newline marker) neither. Anything else — including an empty
// string — is treated as context (fail-safe, still advances both) so a
// malformed line never desynchronises the numbering of the rows after it.
import type { DiffHunk } from './tool-activity'

export type DiffRowKind = 'ctx' | 'add' | 'del' | 'meta'

export interface DiffRow {
  kind: DiffRowKind
  /** Old-side line number; null on the side that has no line. */
  old: number | null
  /** New-side line number; null on the side that has no line. */
  new: number | null
  /** Line content without the sign column (for meta: without the leading `\ `). */
  text: string
}

export function diffRows(hunk: DiffHunk): DiffRow[] {
  const rows: DiffRow[] = []
  let old = hunk.oldStart
  let neu = hunk.newStart
  for (const line of hunk.lines) {
    switch (line[0]) {
      case ' ':
        rows.push({ kind: 'ctx', old: old++, new: neu++, text: line.slice(1) })
        break
      case '-':
        rows.push({ kind: 'del', old: old++, new: null, text: line.slice(1) })
        break
      case '+':
        rows.push({ kind: 'add', old: null, new: neu++, text: line.slice(1) })
        break
      case '\\':
        rows.push({ kind: 'meta', old: null, new: null, text: line.slice(line[1] === ' ' ? 2 : 1) })
        break
      default:
        rows.push({ kind: 'ctx', old: old++, new: neu++, text: line })
    }
  }
  return rows
}
