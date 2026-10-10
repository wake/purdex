// spa/src/components/deck/StatusRow.tsx — the one-line status row under the input (U3-5, scheme A, user 2026-10-10):
// [state | model · effort | 🧠 context ring + left + tokens left | 🕐 5h ring + left + ↺reset | 📅 7d … | cost].
// Props in, nothing fetched: the caller owns the data. Narrowing is by the row's own @container (no JS measuring), see
// `HIDE` in status-row-model.ts: cost → 7d → (1M) → context tokens → reset times → effort → 5h.
import { Separator } from '../status/StatusSegments'
import type { UsageWindow } from '../../lib/usage-display'
import { USAGE_STALE_MS } from '../../lib/usage-display'
import { StateSlot, type SlotState } from './StateSlot'
import { ContextPart, CostPart, LimitPart, ModelPart } from './StatusRowParts'
import { HIDE } from './status-row-model'

export interface StatusRowProps {
  state: SlotState
  exitCode?: number
  /** How long the running step has been going, ms. */
  elapsedMs?: number
  model: string | null
  effort: string | null
  /** Context used share 0-100; null = no value (「—」). */
  contextUsed: number | null
  /** The model's window in tokens; without it the remaining-token text is left out. */
  contextWindowTokens?: number
  fiveHour: UsageWindow | null
  sevenDay: UsageWindow | null
  cost: number | null
  now: number
  /** When the usage snapshot was taken; older than 10 minutes dims the usage items. */
  usageAt?: number
}

export function StatusRow(p: StatusRowProps) {
  const stale = p.usageAt !== undefined && p.now - p.usageAt > USAGE_STALE_MS
  return (
    <div data-testid="status-row" className="@container border-t border-border-subtle bg-surface-primary">
      <div className="flex h-8 items-center gap-1 overflow-hidden px-2">
        <StateSlot state={p.state} exitCode={p.exitCode} elapsedMs={p.elapsedMs} />
        <Separator />
        <ModelPart model={p.model} effort={p.effort} />
        <Separator className={HIDE.modelSep} />
        <ContextPart usedShare={p.contextUsed} windowTokens={p.contextWindowTokens} stale={stale} />
        <Separator className={HIDE.fiveHour} />
        <LimitPart which="five_hour" win={p.fiveHour} now={p.now} stale={stale} className={HIDE.fiveHour} />
        <Separator className={HIDE.sevenDay} />
        <LimitPart which="seven_day" win={p.sevenDay} now={p.now} stale={stale} className={HIDE.sevenDay} />
        <span data-testid="cost-group" className={`ml-auto flex shrink-0 items-center gap-1 ${HIDE.cost}`}>
          <Separator />
          <CostPart cost={p.cost} />
        </span>
      </div>
    </div>
  )
}
