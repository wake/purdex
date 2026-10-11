// spa/src/components/deck/StatusRow.tsx — the one-line row under a session view's input, in the shape of Claude Code's terminal
// status line: left `repo [worktree] [model · effort]`, right the session's cost. No state cell and no usage rings (the rings live
// in the bottom status bar; idle is the tab light). Props in, nothing fetched.
import { formatSessionLine, type SessionLineInput } from './session-line'

export interface StatusRowProps extends SessionLineInput {
  /** USD so far; null = no value, nothing printed. */
  cost: number | null
  /** The host colour mixed into the surface (`useHostTint`): the row's ground. Absent = the plain row. */
  tint?: string
  /** What the pointer resting on the row says (a worker's provider, profile, cwd, session); absent = no tooltip. */
  info?: string
}

export function StatusRow({ cost, tint, info, ...line }: StatusRowProps) {
  const text = formatSessionLine(line)
  return (
    <div
      data-testid="status-row" title={info} data-host-tinted={tint ? 'true' : undefined} style={tint ? { backgroundColor: tint } : undefined}
      className={`flex h-6 items-center gap-2 overflow-hidden px-3 text-xs ${tint ? 'text-text-primary' : 'text-text-secondary'}`}
    >
      <span data-testid="status-row-line" title={text} className="min-w-0 flex-1 truncate">{text || '—'}</span>
      {cost !== null && Number.isFinite(cost) && <span data-testid="item-cost" className="shrink-0 tabular-nums">${cost.toFixed(2)}</span>}
    </div>
  )
}
