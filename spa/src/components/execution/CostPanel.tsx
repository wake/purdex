// spa/src/components/execution/CostPanel.tsx — the cost breakdown that opens
// under the header's `$x.xx` anchor (P-B4 spec §4.3 P1–P4, P6). Pure
// presentation over a `CostSummary`: totals line, token row, per-model list,
// and the chronological per-turn table. Every number goes through the same
// `formatUsd` / `formatTokens` / `formatDuration` the header uses, so the
// panel can never disagree with the anchor or its tooltip. The panel is
// props-driven: a `result` frame landing while it is open re-renders it with
// the new summary (P6) — nothing here is cached. The quota row (P5) is the
// next task's; `hostId` is accepted now so the prop contract is final.
import { useEffect, useRef, type RefObject } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { FloatingPanel } from '../FloatingPanel'
import type { CostSummary, TurnCost } from '../../lib/nex/cost-summary'
import { formatTokens, formatUsd } from '../../lib/nex/format-cost'
import { formatDuration } from '../../lib/nex/format-duration'

export interface CostPanelProps {
  summary: CostSummary
  /** For the quota row (P5, Task 5) — unused until then. */
  hostId: string
  anchorRef: RefObject<HTMLElement | null>
  onClose: () => void
}

const CELL = 'px-1.5 py-0.5'
const NUM = `${CELL} text-right tabular-nums`

function TurnRow({ turn, t }: { turn: TurnCost; t: (key: string) => string }) {
  const tok = turn.tokens
  return (
    <tr data-testid="cost-turn" className="border-t border-border-default/50">
      <td className={`${CELL} text-text-muted`}>#{turn.index}</td>
      <td className={NUM}>{formatUsd(turn.costUsd)}</td>
      <td className={NUM} data-testid="turn-out">{tok ? formatTokens(tok.output) : '—'}</td>
      <td className={NUM} data-testid="turn-cache-read">{tok ? formatTokens(tok.cacheRead) : '—'}</td>
      <td className={`${NUM} whitespace-nowrap`} data-testid="turn-timing">{formatDuration(turn.apiMs ?? 0)} / {formatDuration(turn.durationMs ?? 0)}</td>
      <td className={NUM} data-testid="turn-rounds">{turn.rounds ?? '—'}</td>
      <td className={CELL}>
        {turn.isError && (
          <span data-testid="turn-error" title={turn.subtype} className="text-status-error">{t('execution.cost.turn_error')}</span>
        )}
      </td>
    </tr>
  )
}

export default function CostPanel({ summary, hostId, anchorRef, onClose }: CostPanelProps) {
  // P5 (Task 5) fetches the host quota with this; nothing to do with it yet.
  void hostId
  const t = useI18nStore((s) => s.t)
  const turnsRef = useRef<HTMLDivElement>(null)

  // P4: newest turn last — open scrolled to the bottom, once, on mount. Later
  // rows (P6) must not yank a user who scrolled up back down.
  useEffect(() => {
    const el = turnsRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [])

  const { turns, tokens, models, unsplitTurns } = summary
  const anyTokens = turns.some((turn) => turn.tokens !== null)
  const showModels = models.length > 0 || unsplitTurns > 0

  const tokenCells: Array<{ key: string; value: number }> = [
    { key: 'output', value: tokens.output },
    { key: 'input', value: tokens.input },
    { key: 'cache_read', value: tokens.cacheRead },
    { key: 'cache_write', value: tokens.cacheWrite },
  ]

  return (
    <FloatingPanel title={t('execution.cost.title')} anchorRef={anchorRef} onClose={onClose} width={440} testId="cost-panel">
      <div className="flex flex-col gap-2 text-xs text-text-primary">
        <div data-testid="cost-totals" className="tabular-nums">
          {formatUsd(summary.totalUsd)} · {turns.length} {t('execution.cost.turns')} · {summary.rounds} {t('execution.cost.rounds')} · {formatDuration(summary.apiMs)} API / {formatDuration(summary.durationMs)} wall
        </div>

        {anyTokens && (
          <div data-testid="cost-tokens" className="grid grid-cols-4 gap-2 tabular-nums">
            {tokenCells.map(({ key, value }) => (
              <div key={key} data-testid={`cost-tokens-${key}`} className="flex flex-col">
                <span className="text-text-muted">{t(`execution.cost.tokens.${key}`)}</span>
                <span>{formatTokens(value)}</span>
              </div>
            ))}
          </div>
        )}

        {showModels && (
          <div data-testid="cost-models" className="flex flex-col gap-0.5">
            <span className="text-text-muted">{t('execution.cost.models')}</span>
            {models.map((m) => (
              <div key={m.model} data-testid="cost-model" className="flex items-baseline gap-2 tabular-nums">
                <span className="font-mono truncate">{m.model}</span>
                <span className="flex-1" />
                <span>{formatUsd(m.costUsd)}</span>
                <span className="text-text-muted">{formatTokens(m.tokens.output)} out</span>
              </div>
            ))}
            {unsplitTurns > 0 && (
              <span data-testid="cost-models-note" className="text-text-muted">{t('execution.cost.models_note', { n: unsplitTurns })}</span>
            )}
          </div>
        )}

        <div ref={turnsRef} data-testid="cost-turns" className="max-h-64 overflow-auto">
          <table className="w-full border-collapse">
            <thead className="text-text-muted sticky top-0 bg-surface-elevated">
              <tr>
                <th className={`${CELL} text-left font-normal`}>{t('execution.cost.turn')}</th>
                <th className={`${NUM} font-normal`}>$</th>
                <th className={`${NUM} font-normal`}>{t('execution.cost.tokens.output')}</th>
                <th className={`${NUM} font-normal`}>{t('execution.cost.tokens.cache_read')}</th>
                <th className={`${NUM} font-normal whitespace-nowrap`}>API / wall</th>
                <th className={`${NUM} font-normal`}>{t('execution.cost.rounds')}</th>
                <th className={CELL} />
              </tr>
            </thead>
            <tbody>
              {turns.map((turn) => <TurnRow key={turn.index} turn={turn} t={t} />)}
            </tbody>
          </table>
        </div>
      </div>
    </FloatingPanel>
  )
}
