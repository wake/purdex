// spa/src/components/deck/StatusRowParts.tsx — the items of the status row (U3-5). Each is a tooltip, not a button: a usage item
// opens nothing. Number semantics are the Mac bar's (user 2026-10-10): context ring = USED, number = LEFT; 5h / 7d ring and
// number = LEFT. A missing value is 「—」 (UsageSegment `used: null`), never 0 %.
import { Brain, CalendarBlank, Clock } from '@phosphor-icons/react'
import { UsageSegment } from '../status/UsageSegments'
import { useI18nStore } from '../../stores/useI18nStore'
import { formatResetsIn, remainingPct, usageTone, usedPct, type UsageWindow } from '../../lib/usage-display'
import { formatTokens, HIDE, shortReset, splitModel, tokensLeft } from './status-row-model'

const TONE_TEXT = { ok: 'text-text-secondary', warn: 'text-status-warning', danger: 'text-status-error' } as const
const ITEM = 'flex shrink-0 items-center gap-1 whitespace-nowrap rounded px-1 py-0.5 text-xs'

export function ModelPart({ model, effort }: { model: string | null; effort: string | null }) {
  const t = useI18nStore((s) => s.t)
  if (!model) return null
  const { name, window } = splitModel(model)
  return (
    <span data-testid="item-model" title={t('statusrow.model', { model, effort: effort ?? '—' })} className={`${ITEM} min-w-0 shrink!`}>
      <span className="min-w-0 truncate text-text-secondary">
        <span>{name}</span>
        {window && <span data-testid="model-window" className={HIDE.modelWindow}> {window}</span>}
        {effort && <span data-testid="model-effort" className={`text-text-muted ${HIDE.effort}`}> · {effort}</span>}
      </span>
    </span>
  )
}

export function ContextPart({ usedShare, windowTokens, stale }: { usedShare: number | null; windowTokens?: number; stale: boolean }) {
  const t = useI18nStore((s) => s.t)
  const used = usedShare === null ? null : usedPct(usedShare)
  let title = t('statusrow.none', { name: t('statusrow.name.context') })
  if (used !== null) {
    title = windowTokens
      ? t('statusrow.context', { pct: used, left: remainingPct(used), used: formatTokens(windowTokens - tokensLeft(windowTokens, used)), total: formatTokens(windowTokens) })
      : t('status.usage.context', { left: remainingPct(used), pct: used })
  }
  return (
    <span data-testid="item-context" title={title} className={ITEM}>
      <UsageSegment testId="status-seg-usage-context" icon={Brain} used={used} ring="used" number="remaining" title={title} stale={stale} />
      {used !== null && windowTokens ? (
        <span data-testid="ctx-tokens" className={`tabular-nums ${TONE_TEXT[usageTone(used)]} ${HIDE.ctxTokens}`}>
          {t('statusrow.ctx_left', { n: formatTokens(tokensLeft(windowTokens, used)) })}
        </span>
      ) : null}
    </span>
  )
}

export function LimitPart({ which, win, now, stale, className = '' }: {
  which: 'five_hour' | 'seven_day'; win: UsageWindow | null; now: number; stale: boolean; className?: string
}) {
  const t = useI18nStore((s) => s.t)
  const five = which === 'five_hour'
  const left = win && win.resetsAtMs !== null && Number.isFinite(now) ? formatResetsIn(win.resetsAtMs, now) : null
  const short = win ? shortReset(win.resetsAtMs, now) : null
  const title = !win
    ? t('statusrow.none', { name: t(`statusrow.name.${which}`) })
    : [t(`statusrow.${which}`, { left: remainingPct(win.pct), pct: usedPct(win.pct) }), left ? t('status.usage.resets_in', { time: left }) : ''].filter(Boolean).join('，')
  return (
    <span data-testid={`item-${which}`} title={title} className={`${ITEM} ${className}`}>
      <UsageSegment testId={five ? 'status-seg-usage-five-hour' : 'status-seg-usage-seven-day'} icon={five ? Clock : CalendarBlank} used={win ? win.pct : null} ring="remaining" number="remaining" title={title} stale={stale} />
      {short && <span data-testid={`${which}-reset`} className={`tabular-nums text-text-muted ${HIDE.reset}`}>↺{short}</span>}
    </span>
  )
}

export function CostPart({ cost }: { cost: number | null }) {
  const t = useI18nStore((s) => s.t)
  const title = cost === null ? t('statusrow.none', { name: t('statusrow.name.cost') }) : t('statusrow.cost', { cost: cost.toFixed(2) })
  return (
    <span data-testid="item-cost" title={title} className={`${ITEM} tabular-nums ${cost === null ? 'text-text-muted' : 'text-text-secondary'}`}>
      {cost === null ? '—' : `$${cost.toFixed(2)}`}
    </span>
  )
}
