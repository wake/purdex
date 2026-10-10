// spa/src/components/deck/StatusRow.tsx — the one-line row under a session view's input, in the shape of Claude Code's terminal
// status line: left `repo [worktree] [model · effort]`, right the session's cost. No state cell and no usage rings (the rings live
// in the bottom status bar; idle is the tab light). Props in, nothing fetched.
import { formatSessionLine, type SessionLineInput } from './session-line'

export interface StatusRowProps extends SessionLineInput {
  /** USD so far; null = no value, nothing printed. */
  cost: number | null
}

export function StatusRow({ cost, ...line }: StatusRowProps) {
  const text = formatSessionLine(line)
  return (
    <div data-testid="status-row" className="flex h-7 items-center gap-2 overflow-hidden px-3 text-xs text-text-secondary">
      <span data-testid="status-row-line" title={text} className="min-w-0 flex-1 truncate">{text || '—'}</span>
      {cost !== null && Number.isFinite(cost) && <span data-testid="item-cost" className="shrink-0 tabular-nums">${cost.toFixed(2)}</span>}
    </div>
  )
}
