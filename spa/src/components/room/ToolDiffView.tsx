// spa/src/components/room/ToolDiffView.tsx — unified diff with line numbers for
// Edit / Write tool results (P-B3 spec §4.4 R5). Pure presentational: the
// hunk numbering comes from lib/nex/diff-lines, the copy from i18n, and the
// display budget from `foldPlan` — the same ladder every other block in the
// pane folds by (#1227). A budget of its own here would be the third
// truncation mechanism spec §3.2 names as the defect.
import { useI18nStore } from '../../stores/useI18nStore'
import { diffRows, type DiffRow, type DiffRowKind } from '../../lib/nex/diff-lines'
import type { ToolActivity } from '../../lib/nex/tool-activity'
import { foldPlan } from '../../lib/nex/fold'
import { useFold } from './fold-context'

interface Props {
  diff: NonNullable<ToolActivity['diff']>
  /** The operation's fold key; the diff registers under `${foldKey}:diff`. */
  foldKey: string
  /**
   * Draw `diff.path` beside the stat. Off by default: for Edit and Write the
   * path is the header's own `primary_arg`, and the block that owns the header
   * is the only one that knows whether it drew one (spec §3.1.1 #3 — one fact,
   * one place). An orphan result has no call and so no argument, which is the
   * case this exists for.
   */
  showPath?: boolean
}

/**
 * U+2212 MINUS SIGN, not the hyphen: it is the width of `+` under tabular-nums,
 * so `+5 −0` and `+80 −12` line up. Inherited from `tool-result-facts.ts`,
 * whose facts span carried this stat before room dismantled it.
 */
const MINUS = '−'

/** Sign column: the raw unified-diff characters, so a copied row reads as a diff. */
const SIGN: Record<DiffRowKind, string> = { add: '+', del: '-', ctx: '', meta: '' }

const ROW_CLASS: Record<DiffRowKind, string> = {
  add: 'bg-status-success/10',
  del: 'bg-status-error/10',
  ctx: '',
  meta: 'text-text-muted italic',
}

interface HunkRows {
  hunk: NonNullable<ToolActivity['diff']>['hunks'][number]
  rows: DiffRow[]
}

const NUM_CLASS = 'w-10 shrink-0 text-right tabular-nums text-text-muted pr-1 select-none'
const BUTTON_CLASS =
  'mt-1 text-xs text-text-muted hover:text-text-primary cursor-pointer text-left px-2'

/**
 * Spends the row budget hunk by hunk, in order. A hunk whose rows all fall
 * outside it comes back empty and draws nothing — not even its header, which
 * is a label for rows that are not there.
 *
 * Module-level and pure: a running counter inside the component's own map is a
 * reassignment after render (react-hooks/immutability).
 */
function spendBudget(hunks: HunkRows[], budget: number): HunkRows[] {
  const out: HunkRows[] = []
  let left = budget
  for (const { hunk, rows } of hunks) {
    const take = Math.max(0, Math.min(rows.length, left))
    left -= take
    out.push({ hunk, rows: rows.slice(0, take) })
  }
  return out
}

export default function ToolDiffView({ diff, foldKey, showPath = false }: Props) {
  const t = useI18nStore((s) => s.t)
  const [expanded, toggle] = useFold(`${foldKey}:diff`)

  // A hunk header is a label for its rows, not a row: it never counts against
  // the budget, and it is drawn only when at least one of its rows survives it.
  const hunks: HunkRows[] = diff.hunks.map((hunk) => ({ hunk, rows: diffRows(hunk) }))
  const rows = hunks.flatMap((h) => h.rows)

  // The shared ladder decides how many rows show: ≤ 6 whole, ≤ 40 six, more
  // three, and a daemon-truncated diff three whatever its size — identical to
  // an output of the same number of lines.
  const plan = foldPlan({
    text: rows.map((r) => r.text).join('\n'),
    totalLines: rows.length,
    truncated: diff.truncated,
  })
  const budget = expanded || !plan.collapsible ? rows.length : plan.previewLines.length

  // No hunks and nothing dropped → nothing to say. No hunks but truncated →
  // the daemon dropped every hunk, and what is left — the stat and the
  // truncation note — is the only account of the edit there will be.
  if (diff.hunks.length === 0 && !diff.truncated) return null

  const visible = spendBudget(hunks, budget)

  // Spec §3.1.1 #1: room drops the card from every operation and opens one
  // exception — "only special blocks (diff) keep a container". A diff is a
  // grid of its own with its own left edge (two number columns and a sign
  // column), so it needs an edge to sit against; the rest of the block reads
  // off the rail. Outline only: the fill is reserved for error and denied
  // (#2), and an edit is not a failure.
  return (
    <div data-testid="tool-diff" className="font-mono text-xs rounded-lg border border-border-subtle">
      {/*
        Spec §3.1.1 #3: the stat travels with the diff, not in the header's
        right column where it used to be stacked under the duration and the
        size. It stays outside the fold — `+N −M` is the summary you read
        *instead* of expanding — and it is drawn even when the daemon dropped
        every hunk, where it is the only thing left that says what changed.
        `+0 −0` is a normal edit result and renders (contract rule 7).

        The numbers always show. The path only joins them when the caller says
        the header has no argument of its own: for Edit and Write `diff.path`
        is that argument, and printing it again here would rebuild, one column
        over, the stacking spec §3.1.1 #3 objects to.
      */}
      <div data-testid="diff-stat" className="flex items-baseline gap-2 px-2 py-0.5 text-text-muted">
        {showPath && (
          <span data-testid="diff-path" className="min-w-0 flex-1 break-all">{diff.path}</span>
        )}
        <span className="shrink-0 tabular-nums">{`+${diff.added} ${MINUS}${diff.removed}`}</span>
      </div>
      {visible.map(({ hunk, rows: hunkRows }, i) => {
        if (hunkRows.length === 0) return null
        return (
          <div key={i} data-testid="diff-hunk">
            <div data-testid="diff-hunk-header" className="text-text-muted px-2 py-0.5">
              {`@@ -${hunk.oldStart},${hunk.oldLines} +${hunk.newStart},${hunk.newLines} @@`}
            </div>
            {hunkRows.map((row, j) => (
              <div key={j} data-kind={row.kind} className={`flex ${ROW_CLASS[row.kind]}`}>
                <span className={NUM_CLASS}>{row.old ?? ''}</span>
                <span className={NUM_CLASS}>{row.new ?? ''}</span>
                <span className="w-4 shrink-0 select-none">{SIGN[row.kind]}</span>
                <span className="whitespace-pre-wrap break-all flex-1 min-w-0">{row.text}</span>
              </div>
            ))}
          </div>
        )
      })}
      {diff.truncated && (
        <div data-testid="diff-truncated" className="text-text-muted italic px-2 py-0.5">
          {t('execution.tool.diff_truncated')}
        </div>
      )}
      {plan.collapsible && (
        <button
          type="button"
          data-testid={expanded ? 'diff-less' : 'diff-more'}
          className={BUTTON_CLASS}
          onClick={toggle}
        >
          {expanded ? t('room.fold.less') : t('room.fold.more', { n: plan.hiddenLines })}
        </button>
      )}
    </div>
  )
}
