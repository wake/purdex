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
//
// Permission channel PC2 (spec §5.4): a row whose summary carries
// `pending_permission` shows 「等待核准」 — the warning dot, a HandPalm icon
// beside it, and the text as the tooltip. Queued keeps the bare warning dot.
//
// #1771: the row's name is `workerRowName` — the brief's first line as before, else (a handoff has no brief) the
// conversation title when this host's capability says the field exists, else the cwd basename. The name is the
// visible text and the plain row's aria-label lead.
import { HandPalm, SignOut, Terminal } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { selectSessionTitleSupported, useNexHostStore } from '../../stores/useNexHostStore'
import { workerDotClass } from '../../lib/nex/state-dot'
import { isAwaitingApproval } from '../../lib/nex/worker-summary'
import { workerRowName } from '../../lib/nex/worker-row-name'
import { formatUsd } from '../../lib/nex/format-cost'
import { normalizePhase } from '../../lib/nex/activity'
import { rowCostIncludesPriorHistory } from '../../lib/nex/prior-history'
import { relativeAge } from '../../lib/nex/relative-age'
import { sameHostSessionCode } from '../../lib/nex/execution-groups'
import type { ExecutionSummary } from '../../lib/nex/types'

type T = ReturnType<typeof useI18nStore.getState>['t']

interface Props {
  row: ExecutionSummary
  /**
   * The client's host entry id: its `session_title` capability (`selectSessionTitleSupported`) decides whether a row
   * without a brief may be named by the conversation title. Absent → the capability counts as absent (fail closed).
   */
  hostId?: string
  /** The daemon's own host id (`capabilities.host_id`), not the client's host entry id — `origin` is stamped by the daemon. */
  daemonHostId: string | null
  now: number
  /** The host's `worker_rollup.cost_basis` is trusted (`selectRollupCostShown`); false hides `cost_usd`. */
  showCost?: boolean
  /** Absent → the row is not an action (its host is hidden in this workbench — plan H2d-2): a plain, listed row. */
  onOpen?: () => void
  /** Present → a sibling 退出 button beside the open button (shown hosts only). */
  onExit?: () => void
  /** The exit is on its way: the button stays (layout) but is disabled. */
  exitPending?: boolean
}

const ROW_CLASS = 'flex items-center gap-1.5 w-full min-w-0 px-3 py-1 text-left'

/**
 * The dot's tooltip: 「等待核准」 while the row awaits approval, else the activity when the row carries one, else (and
 * for `ended`) the state as before.
 */
function activityLabel(t: T, row: ExecutionSummary, awaiting: boolean): string {
  if (awaiting) return t('executions.activity.awaiting_approval')
  if (!row.activity) return row.state
  const phase = normalizePhase(row.activity.phase)
  if (phase === 'ended') return row.state
  if (phase === 'tool') {
    const tool = row.activity.tool?.name ?? row.last_tool?.name
    return tool ? t('executions.activity.tool', { tool }) : t('executions.activity.tool_unnamed')
  }
  return t(`executions.activity.${phase}`)
}

export function ExecutionRowCompact({ row, hostId, daemonHostId, now, showCost = false, onOpen, onExit, exitPending = false }: Props) {
  const t = useI18nStore((s) => s.t)
  const titleSupported = useNexHostStore((s) => (hostId ? selectSessionTitleSupported(hostId)(s) : false))
  const age = relativeAge(row.updated_at, now)
  const sessionCode = daemonHostId ? sameHostSessionCode(row.origin, daemonHostId) : null
  const name = workerRowName(row, titleSupported)
  const markerTitle = sessionCode !== null ? t('executions.marker_title', { code: sessionCode }) : null
  const ageText = t(`executions.age.${age.key}`, { n: age.n })
  const awaiting = isAwaitingApproval(row)
  const dotTitle = activityLabel(t, row, awaiting)
  const running = typeof row.running_tasks === 'number' && row.running_tasks > 0 ? row.running_tasks : 0
  const runningText = running > 0 ? t(running === 1 ? 'room.dock.running_one' : 'room.dock.running_other', { count: running }) : null
  const costText = showCost && typeof row.cost_usd === 'number' ? formatUsd(row.cost_usd, 2) : null
  const costTitle = costText !== null && rowCostIncludesPriorHistory(row) ? t('execution.cost.includesPriorHistory') : undefined

  const content = (
    <>
      <span
        data-testid="executions-state-dot"
        className={`shrink-0 inline-block w-2 h-2 rounded-full ${workerDotClass(row.state, awaiting)}`}
        title={dotTitle}
      />
      {awaiting && (
        // The icon is what tells 「等待核准」 apart from queued, which has the same warning dot and no icon.
        <span data-testid="executions-awaiting" className="shrink-0 inline-flex text-status-warning" title={dotTitle}>
          <HandPalm size={11} weight="fill" aria-hidden="true" />
        </span>
      )}
      <span data-testid="executions-brief" className="flex-1 min-w-0 truncate text-xs text-text-primary">
        {name}
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
    const label = [name, dotTitle, markerTitle, runningText, ageText, costText, row.id]
      .filter((part) => part !== null && part !== '')
      .join(' · ')
    return (
      <div data-testid="executions-row" role="listitem" aria-label={label} title={row.id} className={ROW_CLASS}>
        {content}
      </div>
    )
  }
  const openButton = (
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
  if (!onExit) return openButton
  // The exit button is a sibling, never nested in the open button. It is visually hidden until hover/focus, but
  // stays in the tab order (opacity, not display:none).
  return (
    <div className="group relative flex items-center">
      {openButton}
      <button
        type="button"
        data-testid="executions-row-exit"
        aria-label={t('worker.exit.button')}
        title={t('worker.exit.button')}
        disabled={exitPending}
        onClick={onExit}
        className="absolute right-1 shrink-0 p-1 rounded bg-surface-secondary text-text-muted hover:text-status-error opacity-0 group-hover:opacity-100 focus:opacity-100 cursor-pointer disabled:opacity-40 disabled:cursor-default"
      >
        <SignOut size={12} aria-hidden="true" />
      </button>
    </div>
  )
}
