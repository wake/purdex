// spa/src/components/room/OperationBlock.tsx — one tool call is one block
// (spec §4.2). The call and the result that answers it share a header line
// and an indent rail; the two bordered cards they used to be are gone, and
// with them the green fill that made success louder than failure
// (spec §3.1.1 #1 and #2 — errors and denials carry the fill now, success
// carries nothing).
//
// Nothing here holds its own expansion: a block unmounts whenever its turn is
// re-keyed, so both affordances read the pane-level memory through `useFold`,
// which also registers them with the surrounding turn so expand-all reaches
// them (spec §3.2).
import { CircleNotch } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { formatDuration } from '../../lib/nex/format-duration'
import { toolSummary } from '../../lib/nex/tool-summary'
import { foldPlan } from '../../lib/nex/fold'
import type { ToolActivity, ToolCallActivity } from '../../lib/nex/tool-activity'
import type { ToolResultFacts } from '../../lib/nex/tool-result-facts'
import type { OperationResult } from '../../lib/nex/operations'
import { useFold } from './fold-context'
import { FoldedOutput } from './FoldedOutput'
import ToolDiffView from './ToolDiffView'

export interface OperationBlockProps {
  tool: string
  input: Record<string, unknown>
  /** Absent → status is derived from `facts` / `result` alone (a subagent's call, PR-5). */
  activity?: ToolCallActivity
  summaryEntry?: Pick<ToolActivity, 'primaryArg' | 'known'>
  /** N2 facts for the result: output volume, file, diff, status. */
  facts?: ToolResultFacts
  /** null → the call has not been answered yet. */
  result: OperationResult | null
  /** Stable key for the pane-level fold memory (the tool_use id). */
  foldKey: string
}

/** `pending` is a call with nothing said about it yet — no activity, no facts, no result. */
type OpStatus = ToolActivity['status'] | 'streaming' | 'pending'

/** Duration is shown at a second and up (spec §3.1.1 #3) until #1229 lands. */
const DURATION_FLOOR_MS = 1_000

const DOT_CLASS: Record<Exclude<OpStatus, 'running' | 'streaming'>, string> = {
  done: 'bg-status-success',
  error: 'bg-status-error',
  denied: 'bg-status-warning',
  aborted: 'bg-text-muted',
  pending: 'bg-text-muted',
}

const RAIL_FILL: Partial<Record<OpStatus, string>> = {
  error: 'bg-status-error/10',
  denied: 'bg-status-warning/10',
}

/**
 * What the block is: the lifecycle variant when there is one, else the N2
 * status, else what the raw frame's `is_error` says. N2 outranks the raw
 * frame both ways — a denial it flagged is not downgraded by a result that
 * arrived without `is_error`, and an `is_error` it contradicts is not an
 * error (P-B3 R3 / codex R2 A1).
 */
function resolveStatus(
  activity: ToolCallActivity | undefined,
  facts: ToolResultFacts | undefined,
  result: OperationResult | null,
): OpStatus {
  if (activity) return activity.status
  if (facts?.status) return facts.status
  if (result) return result.isError ? 'error' : 'done'
  return 'pending'
}

/**
 * The milliseconds worth showing, or null. The daemon's `durationMs` wins
 * outright — it is measured, while the raw-frame clocks are two server
 * timestamps that are 0 when unknown (P-B3 R2). `null` durationMs is an
 * unmatched result, not a duration, so it falls through to the clocks.
 */
function durationOf(activity: ToolCallActivity | undefined): number | null {
  if (!activity) return null
  switch (activity.status) {
    case 'running':
      return activity.startedAt > 0 ? Math.max(0, activity.now - activity.startedAt) : null
    case 'done':
    case 'error':
    case 'denied':
      if (typeof activity.durationMs === 'number') return activity.durationMs
      return activity.startedAt > 0 && activity.endedAt > 0 ? activity.endedAt - activity.startedAt : null
    default:
      return null
  }
}

export default function OperationBlock({
  tool,
  input,
  activity,
  summaryEntry,
  facts,
  result,
  foldKey,
}: OperationBlockProps) {
  const t = useI18nStore((s) => s.t)
  const [outputExpanded, toggleOutput] = useFold(foldKey)
  const [inputExpanded, toggleInput] = useFold(`${foldKey}:input`)

  const status = resolveStatus(activity, facts, result)
  const streaming = status === 'streaming'
  const failed = status === 'error' || status === 'denied'

  const name = tool || t('execution.tool.unknown')
  const nameClass = status === 'denied'
    ? 'font-semibold line-through text-text-muted'
    : 'font-semibold text-text-primary'

  // The full primary_arg, wrapped — never cut to an ellipsis in the middle
  // (spec §4.2), which is why SUMMARY_LIMIT is not applied here any more.
  const summary = streaming ? '' : toolSummary(tool, input, summaryEntry)

  const ms = durationOf(activity)
  const showDuration = ms !== null && ms >= DURATION_FLOOR_MS

  // A second affordance, and only when it has something the header does not
  // already say: an input whose single key is the primary arg reveals nothing.
  const inputKeys = Object.keys(input)
  const hasRawInput =
    inputKeys.length > 0 && !(summaryEntry?.primaryArg && inputKeys.length === 1)

  const diff = facts?.diff
  const hasDiff = diff !== undefined && (diff.hunks.length > 0 || diff.truncated)

  // `error` and `denied` fold one step less: a failure you have to expand is a
  // failure you will miss (spec §4.2).
  const plan = foldPlan({
    text: result?.text ?? '',
    totalLines: facts?.output?.totalLines,
    totalBytes: facts?.output?.totalBytes,
    truncated: facts?.output?.truncated,
    severity: failed ? 'error' : 'normal',
  })

  const railFill = RAIL_FILL[status] ?? ''
  const showRail = result !== null || hasDiff || (hasRawInput && inputExpanded)

  return (
    <div data-testid="operation-block" className="text-sm my-1">
      <div className="flex items-start gap-2 min-w-0">
        {status === 'running' || streaming ? (
          <CircleNotch
            size={12}
            data-testid="op-dot"
            className="mt-1 shrink-0 text-text-secondary animate-spin"
          />
        ) : (
          <span
            data-testid="op-dot"
            className={`mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full ${DOT_CLASS[status]}`}
          />
        )}
        <span data-testid="op-name" className={nameClass}>{name}</span>
        {streaming ? (
          // The half-assembled JSON is noise, not information (spec §3.1.1 #7).
          <span data-testid="op-arg-pending" className="text-text-muted">…</span>
        ) : summary ? (
          <span data-testid="op-arg" className="text-text-muted whitespace-pre-wrap break-all min-w-0 flex-1">
            {summary}
          </span>
        ) : (
          <span className="flex-1" />
        )}
        {status === 'aborted' && (
          <span data-testid="op-aborted" className="text-xs text-text-muted shrink-0">
            {t('execution.tool.aborted')}
          </span>
        )}
        {showDuration && (
          <span
            data-testid={status === 'running' ? 'op-elapsed' : 'op-duration'}
            className="text-xs text-text-muted tabular-nums shrink-0"
          >
            {formatDuration(ms)}
          </span>
        )}
      </div>

      {hasRawInput && (
        <button
          type="button"
          data-testid="op-input-toggle"
          aria-expanded={inputExpanded}
          className="ml-5 text-xs text-text-muted hover:text-text-primary cursor-pointer text-left"
          onClick={toggleInput}
        >
          {t('room.op.show_input')}
        </button>
      )}

      {showRail && (
        <div data-testid="op-rail" className={`ml-[3px] border-l border-border-subtle pl-3 ${railFill}`}>
          {hasRawInput && inputExpanded && (
            <pre data-testid="op-input" className="text-xs text-text-secondary whitespace-pre-wrap break-all overflow-auto max-h-60">
              {JSON.stringify(input, null, 2)}
            </pre>
          )}
          {hasDiff && <ToolDiffView diff={diff} foldKey={foldKey} />}
          {result !== null && (
            <FoldedOutput
              text={result.text}
              plan={plan}
              expanded={outputExpanded}
              onToggle={toggleOutput}
              tone={status === 'error' ? 'error' : 'normal'}
            />
          )}
        </div>
      )}
    </div>
  )
}
