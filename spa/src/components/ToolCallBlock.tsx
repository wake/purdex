// spa/src/components/ToolCallBlock.tsx
import { useState } from 'react'
import { CaretRight, CaretDown, CircleNotch, Wrench } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import { formatDuration } from '../lib/nex/format-duration'
import { toolSummary, SUMMARY_LIMIT } from '../lib/nex/tool-summary'
import type { ToolActivity, ToolCallActivity } from '../lib/nex/tool-activity'

export type { ToolCallActivity }

type FinishedActivity = Extract<ToolCallActivity, { status: 'done' | 'error' | 'denied' }>

/**
 * R2: the daemon's duration_ms wins even when the raw-frame clocks are
 * unknown (N2 unseen path has startedAt 0); the `> 0` guard only protects
 * the endedAt − startedAt fallback. `null` → nothing to show.
 */
function finishedMs(activity: FinishedActivity): number | null {
  if (typeof activity.durationMs === 'number') return activity.durationMs
  return activity.startedAt > 0 && activity.endedAt > 0 ? activity.endedAt - activity.startedAt : null
}

interface Props {
  tool: string
  input: Record<string, unknown>
  /**
   * Lifecycle stage + the timing that stage can show (P-B2.2 R1/R2).
   * Absent → Stream-mode rendering (wrench, no badge). Unix ms server-clock
   * fields that are 0 mean "unknown" and hide the badge — unless the
   * finished variant carries `durationMs` (P-B3 R2), which wins outright.
   */
  activity?: ToolCallActivity
  /**
   * N2 overlay for the header summary (P-B3 R1): `primaryArg.value` wins,
   * `known === false` falls back to the first input keys, otherwise the
   * client table. Absent → client table, today's DOM.
   */
  summaryEntry?: Pick<ToolActivity, 'primaryArg' | 'known'>
}

// Header summary width; the same constant bounds the R10 preview (A3).
const SUMMARY_MAX = SUMMARY_LIMIT

function TimingBadge({ activity, t }: { activity: ToolCallActivity; t: (key: string) => string }) {
  switch (activity.status) {
    case 'aborted':
      return <span data-testid="tool-aborted" className="text-xs text-text-muted flex-shrink-0">{t('execution.tool.aborted')}</span>
    case 'denied': {
      // R3 badge, then the R2 duration (codex R2 A2: a denial still took time
      // to be decided; muted, not the error colour — a denial is not a failure).
      const ms = finishedMs(activity)
      return (
        <>
          <span data-testid="tool-denied" className="text-xs text-status-warning flex-shrink-0">{t('execution.tool.denied')}</span>
          {ms !== null && (
            <span data-testid="tool-duration" className="text-xs text-text-muted tabular-nums flex-shrink-0">{formatDuration(ms)}</span>
          )}
        </>
      )
    }
    case 'running': {
      if (activity.startedAt <= 0) return null
      return <span data-testid="tool-elapsed" className="text-xs text-text-muted tabular-nums flex-shrink-0">{formatDuration(Math.max(0, activity.now - activity.startedAt))}</span>
    }
    case 'done':
    case 'error': {
      const ms = finishedMs(activity)
      if (ms === null) return null
      const tone = activity.status === 'error' ? 'text-status-error' : 'text-text-muted'
      return <span data-testid="tool-duration" className={`text-xs ${tone} tabular-nums flex-shrink-0`}>{formatDuration(ms)}</span>
    }
    case 'streaming':
      return null
    default: {
      const _exhaustive: never = activity
      void _exhaustive
      return null
    }
  }
}

export default function ToolCallBlock({ tool, input, activity, summaryEntry }: Props) {
  const [expanded, setExpanded] = useState(false)
  const t = useI18nStore((s) => s.t)
  const rawInput = activity?.status === 'streaming' ? activity.rawInput : null
  const summary = (rawInput !== null ? rawInput : toolSummary(tool, input, summaryEntry)).slice(0, SUMMARY_MAX)
  const detail = rawInput !== null ? rawInput : JSON.stringify(input, null, 2)
  const spinning = activity?.status === 'streaming' || activity?.status === 'running'
  // R3: a denial is not a failure — the name is struck through, the icon stays the wrench.
  const nameClass = activity?.status === 'denied' ? 'line-through text-text-muted font-semibold' : 'text-text-primary font-semibold'

  return (
    <div className="rounded-lg border border-border-subtle bg-[#1e1e1e] text-sm my-1 overflow-hidden"> {/* TODO: theme token for bg-[#1e1e1e] */}
      <button
        data-testid="tool-header"
        aria-expanded={expanded}
        className="w-full flex items-center gap-2 px-3 py-1.5 hover:bg-[#252525] cursor-pointer text-left" /* TODO: theme token for hover */
        onClick={() => setExpanded(v => !v)}
      >
        {expanded ? (
          <CaretDown size={12} className="text-text-muted flex-shrink-0" />
        ) : (
          <CaretRight size={12} className="text-text-muted flex-shrink-0" />
        )}
        {spinning ? (
          <CircleNotch size={16} data-testid="tool-icon-spinner" className="text-text-secondary flex-shrink-0 animate-spin" />
        ) : (
          <Wrench size={16} data-testid="tool-icon-wrench" className="text-text-secondary flex-shrink-0" />
        )}
        <span className={nameClass}>{tool}</span>
        {summary && (
          <span className="text-text-muted truncate flex-1 min-w-0">{summary}</span>
        )}
        {activity && <TimingBadge activity={activity} t={t} />}
      </button>
      {expanded && (
        <div data-testid="tool-detail" className="border-t border-border-subtle px-3 py-2 bg-[#161616]"> {/* TODO: theme token for bg-[#161616] */}
          <pre className="text-xs text-text-secondary whitespace-pre-wrap break-all overflow-auto max-h-60">
            {detail}
          </pre>
        </div>
      )}
    </div>
  )
}
