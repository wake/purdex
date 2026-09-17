// spa/src/components/ToolCallBlock.tsx
import { useState } from 'react'
import { CaretRight, CaretDown, CircleNotch, Wrench } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import { formatDuration } from '../lib/nex/format-duration'

export type ToolCallStatus = 'streaming' | 'running' | 'done' | 'error' | 'aborted'

interface Props {
  tool: string
  input: Record<string, unknown>
  /** Absent → Stream-mode rendering (no icon swap, no badge). P-B2.2 R1/R2. */
  status?: ToolCallStatus
  /** Unix ms (server clock); 0 or undefined = unknown → no timer. */
  startedAt?: number
  endedAt?: number | null
  /** Ticker value; only consulted for `running`. */
  now?: number
  /** Partial `input_json` prefix; only consulted for `streaming`. */
  rawInput?: string
}

const SUMMARY_MAX = 80

function getSummary(tool: string, input: Record<string, unknown>): string {
  switch (tool) {
    case 'Bash':
      return (input.command as string) ?? ''
    case 'Read':
    case 'Write':
    case 'Edit':
      return (input.file_path as string) ?? ''
    case 'WebFetch':
      return (input.url as string) ?? ''
    case 'Grep':
    case 'Glob':
      return (input.pattern as string) ?? ''
    case 'Agent':
      return (input.description as string) ?? ''
    default:
      return JSON.stringify(input).slice(0, 80)
  }
}

function TimingBadge({ status, startedAt, endedAt, now, t }: {
  status: ToolCallStatus
  startedAt: number
  endedAt: number | null
  now: number | undefined
  t: (key: string) => string
}) {
  if (status === 'aborted') {
    return <span data-testid="tool-aborted" className="text-xs text-text-muted flex-shrink-0">{t('execution.tool.aborted')}</span>
  }
  if (status === 'running') {
    if (startedAt <= 0 || typeof now !== 'number') return null
    return <span data-testid="tool-elapsed" className="text-xs text-text-muted tabular-nums flex-shrink-0">{formatDuration(Math.max(0, now - startedAt))}</span>
  }
  if (status === 'done' || status === 'error') {
    if (startedAt <= 0 || endedAt == null || endedAt <= 0) return null
    const tone = status === 'error' ? 'text-status-error' : 'text-text-muted'
    return <span data-testid="tool-duration" className={`text-xs ${tone} tabular-nums flex-shrink-0`}>{formatDuration(endedAt - startedAt)}</span>
  }
  return null
}

export default function ToolCallBlock({ tool, input, status, startedAt = 0, endedAt = null, now, rawInput }: Props) {
  const [expanded, setExpanded] = useState(false)
  const t = useI18nStore((s) => s.t)
  const streaming = status === 'streaming'
  const summary = streaming ? (rawInput ?? '').slice(0, SUMMARY_MAX) : getSummary(tool, input)
  const detail = streaming ? (rawInput ?? '') : JSON.stringify(input, null, 2)
  const spinning = streaming || status === 'running'

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
        <span className="text-text-primary font-semibold">{tool}</span>
        {summary && (
          <span className="text-text-muted truncate flex-1 min-w-0">{summary}</span>
        )}
        {status && <TimingBadge status={status} startedAt={startedAt} endedAt={endedAt} now={now} t={t} />}
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
