// spa/src/components/room/prelude/PreludeCostFooter.tsx — one turn's cost line
// under a prompt span of an earlier worker segment (conversation entity spec
// §10.4): "$0.5000 · 1.2k · 38s" — the CostPanel row's usd, output tokens and
// wall time. Styled as TurnFooter and, like it, not a search unit.
import type { TurnCost } from '../../../lib/nex/cost-summary'
import { formatTokens, formatUsd } from '../../../lib/nex/format-cost'
import { formatDuration } from '../../../lib/nex/format-duration'

export interface PreludeCostFooterProps {
  turn: TurnCost
  /** Set on turn 1 of a resumed stint: its spend includes the hand-over's history. */
  title?: string
}

export default function PreludeCostFooter({ turn, title }: PreludeCostFooterProps) {
  const parts = [formatUsd(turn.costUsd), ...(turn.tokens ? [formatTokens(turn.tokens.output)] : []), formatDuration(turn.durationMs ?? 0)]
  return (
    <div data-testid="prelude-cost-footer" title={title} className="mt-1 text-[length:var(--wt-font-size)] text-[var(--wt-footer-color)] select-none">
      {parts.join(' · ')}
    </div>
  )
}
