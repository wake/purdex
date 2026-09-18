// spa/src/components/ToolDiffView.tsx — unified diff with line numbers for
// Edit / Write tool results (P-B3 spec §4.4 R5). Pure presentational: the
// hunk numbering comes from lib/nex/diff-lines, the copy from i18n.
// TODO: theme tokens for the add / del row tints (same debt as ToolResultBlock)
import { useI18nStore } from '../stores/useI18nStore'
import { diffRows, type DiffRowKind } from '../lib/nex/diff-lines'
import type { ToolActivity } from '../lib/nex/tool-activity'

interface Props {
  diff: NonNullable<ToolActivity['diff']>
}

/** Sign column: the raw unified-diff characters, so a copied row reads as a diff. */
const SIGN: Record<DiffRowKind, string> = { add: '+', del: '-', ctx: '', meta: '' }

const ROW_CLASS: Record<DiffRowKind, string> = {
  add: 'bg-[#1f2a1f]', /* TODO: theme token */
  del: 'bg-[#2a1f1f]', /* TODO: theme token */
  ctx: '',
  meta: 'text-text-muted italic',
}

const NUM_CLASS = 'w-10 shrink-0 text-right tabular-nums text-text-muted pr-1 select-none'

export default function ToolDiffView({ diff }: Props) {
  const t = useI18nStore((s) => s.t)
  if (diff.hunks.length === 0) return null

  return (
    <div data-testid="tool-diff" className="font-mono text-xs">
      {diff.hunks.map((hunk, i) => (
        <div key={i} data-testid="diff-hunk">
          <div data-testid="diff-hunk-header" className="text-text-muted px-2 py-0.5">
            {`@@ -${hunk.oldStart},${hunk.oldLines} +${hunk.newStart},${hunk.newLines} @@`}
          </div>
          {diffRows(hunk).map((row, j) => (
            <div key={j} data-kind={row.kind} className={`flex ${ROW_CLASS[row.kind]}`}>
              <span className={NUM_CLASS}>{row.old ?? ''}</span>
              <span className={NUM_CLASS}>{row.new ?? ''}</span>
              <span className="w-4 shrink-0 select-none">{SIGN[row.kind]}</span>
              <span className="whitespace-pre-wrap break-all flex-1 min-w-0">{row.text}</span>
            </div>
          ))}
        </div>
      ))}
      {diff.truncated && (
        <div data-testid="diff-truncated" className="text-text-muted italic px-2 py-0.5">
          {t('execution.tool.diff_truncated')}
        </div>
      )}
    </div>
  )
}
