// spa/src/components/executions/ExecutionRowCompact.tsx — one row of the
// sidebar Executions view (P-C spec §4.3): state dot, one-line brief,
// relative age, and the ↩ marker when the execution was handed over from a
// tmux session on this same host.
//
// R4 T4.1 — worker_rollup fields, each only when the row carries it (an older
// daemon's row renders exactly as before): a running badge before the age, the
// cost after it (behind the `cost_basis` gate, `showCost`), and the activity
// as the state dot's tooltip. The brief is the only part that shrinks; every
// other item is `shrink-0 whitespace-nowrap`, so a narrow sidebar truncates
// the brief instead of wrapping the row.
import { Terminal } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { stateDotClass } from '../../lib/nex/state-dot'
import { firstLine } from '../../lib/nex/format'
import { formatUsd } from '../../lib/nex/format-cost'
import { normalizePhase } from '../../lib/nex/activity'
import { rowCostIncludesPriorHistory } from '../../lib/nex/prior-history'
import { relativeAge } from '../../lib/nex/relative-age'
import { sameHostSessionCode } from '../../lib/nex/execution-groups'
import type { ExecutionSummary } from '../../lib/nex/types'

type T = ReturnType<typeof useI18nStore.getState>['t']

interface Props {
  row: ExecutionSummary
  /** The daemon's own host id (`capabilities.host_id`), not the client's host entry id — `origin` is stamped by the daemon. */
  daemonHostId: string | null
  now: number
  /** The host's `worker_rollup.cost_basis` is trusted (`selectRollupCostShown`); false hides `cost_usd`. */
  showCost?: boolean
  /** Absent → the row is not an action (its host is hidden in this workbench — plan H2d-2): a plain, listed row. */
  onOpen?: () => void
}

const ROW_CLASS = 'flex items-center gap-1.5 w-full min-w-0 px-3 py-1 text-left'

/** The dot's tooltip: the activity when the row carries one, else (and for `ended`) the state as before. */
function activityLabel(t: T, row: ExecutionSummary): string {
  if (!row.activity) return row.state
  const phase = normalizePhase(row.activity.phase)
  if (phase === 'ended') return row.state
  if (phase === 'tool') {
    const tool = row.activity.tool?.name ?? row.last_tool?.name
    return tool ? t('executions.activity.tool', { tool }) : t('executions.activity.tool_unnamed')
  }
  return t(`executions.activity.${phase}`)
}

export function ExecutionRowCompact({ row, daemonHostId, now, showCost = false, onOpen }: Props) {
  const t = useI18nStore((s) => s.t)
  const age = relativeAge(row.updated_at, now)
  const sessionCode = daemonHostId ? sameHostSessionCode(row.origin, daemonHostId) : null
  const brief = firstLine(typeof row.brief === 'string' ? row.brief : '')
  const markerTitle = sessionCode !== null ? t('executions.marker_title', { code: sessionCode }) : null
  const ageText = t(`executions.age.${age.key}`, { n: age.n })
  const dotTitle = activityLabel(t, row)
  const running = typeof row.running_tasks === 'number' && row.running_tasks > 0 ? row.running_tasks : 0
  const runningText = running > 0 ? t(running === 1 ? 'room.dock.running_one' : 'room.dock.running_other', { count: running }) : null
  const costText = showCost && typeof row.cost_usd === 'number' ? formatUsd(row.cost_usd, 2) : null
  const costTitle = costText !== null && rowCostIncludesPriorHistory(row) ? t('execution.cost.includesPriorHistory') : undefined

  const content = (
    <>
      <span
        data-testid="executions-state-dot"
        className={`shrink-0 inline-block w-2 h-2 rounded-full ${stateDotClass(row.state)}`}
        title={dotTitle}
      />
      <span data-testid="executions-brief" className="flex-1 min-w-0 truncate text-xs text-text-primary">
        {brief}
      </span>
      {markerTitle !== null && (
        <span data-testid="executions-marker" className="shrink-0 text-xs text-text-muted" title={markerTitle}>
          ↩
        </span>
      )}
      {runningText !== null && (
        <span
          data-testid="executions-running"
          className="shrink-0 whitespace-nowrap inline-flex items-center gap-0.5 text-xs text-text-muted tabular-nums"
          title={runningText}
        >
          <Terminal size={11} aria-hidden="true" />
          {running}
        </span>
      )}
      <span data-testid="executions-age" className="shrink-0 text-xs text-text-muted tabular-nums">
        {ageText}
      </span>
      {costText !== null && (
        <span data-testid="executions-cost" className="shrink-0 whitespace-nowrap text-xs text-text-muted tabular-nums" title={costTitle}>
          {costText}
        </span>
      )}
    </>
  )

  if (!onOpen) {
    // Not an action (plan H2d-2): a list item of the group's list — no tabIndex (a focusable non-interactive element
    // is an anti-pattern; screen readers reach it with the browse cursor), named with what a sighted user sees plus
    // the full id that is otherwise only in `title`.
    const label = [brief, dotTitle, markerTitle, runningText, ageText, costText, row.id]
      .filter((part) => part !== null && part !== '')
      .join(' · ')
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
