// spa/src/components/executions/ExecutionRowCompact.tsx — one row of the
// sidebar Executions view (P-C spec §4.3): state dot, one-line brief,
// relative age, and the ↩ marker when the execution was handed over from a
// tmux session on this same host.
import { useI18nStore } from '../../stores/useI18nStore'
import { STATE_DOT_CLASSES } from '../../lib/nex/state-dot'
import { firstLine } from '../../lib/nex/format'
import { relativeAge } from '../../lib/nex/relative-age'
import { sameHostSessionCode } from '../../lib/nex/execution-groups'
import type { ExecutionSummary } from '../../lib/nex/types'

interface Props {
  row: ExecutionSummary
  /** The daemon's own host id (`capabilities.host_id`), not the client's host entry id — `origin` is stamped by the daemon. */
  daemonHostId: string | null
  now: number
  /** Absent → the row is not an action (its host is hidden in this workbench — plan H2d-2): a plain, listed row. */
  onOpen?: () => void
}

const ROW_CLASS = 'flex items-center gap-1.5 w-full min-w-0 px-3 py-1 text-left'

export function ExecutionRowCompact({ row, daemonHostId, now, onOpen }: Props) {
  const t = useI18nStore((s) => s.t)
  const age = relativeAge(row.updated_at, now)
  const sessionCode = daemonHostId ? sameHostSessionCode(row.origin, daemonHostId) : null
  const brief = firstLine(typeof row.brief === 'string' ? row.brief : '')
  const markerTitle = sessionCode !== null ? t('executions.marker_title', { code: sessionCode }) : null
  const ageText = t(`executions.age.${age.key}`, { n: age.n })

  const content = (
    <>
      <span
        data-testid="executions-state-dot"
        className={`shrink-0 inline-block w-2 h-2 rounded-full ${STATE_DOT_CLASSES[row.state] ?? 'bg-text-muted'}`}
        title={row.state}
      />
      <span data-testid="executions-brief" className="flex-1 min-w-0 truncate text-xs text-text-primary">
        {brief}
      </span>
      {markerTitle !== null && (
        <span data-testid="executions-marker" className="shrink-0 text-xs text-text-muted" title={markerTitle}>
          ↩
        </span>
      )}
      <span data-testid="executions-age" className="shrink-0 text-xs text-text-muted tabular-nums">
        {ageText}
      </span>
    </>
  )

  if (!onOpen) {
    // Not an action (plan H2d-2): a list item of the group's list — no tabIndex (a focusable non-interactive element
    // is an anti-pattern; screen readers reach it with the browse cursor), named with what a sighted user sees plus
    // the full id that is otherwise only in `title`.
    const label = [brief, row.state, markerTitle, ageText, row.id].filter((part) => part !== null && part !== '').join(' · ')
    return (
      <div data-testid="executions-row" role="listitem" aria-label={label} title={row.id} className={ROW_CLASS}>
        {content}
      </div>
    )
  }
  return (
    <button
      type="button"
      data-testid="executions-row"
      onClick={onOpen}
      title={row.id}
      className={`${ROW_CLASS} cursor-pointer hover:bg-surface-hover`}
    >
      {content}
    </button>
  )
}
