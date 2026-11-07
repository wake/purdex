// spa/src/components/settings/ThresholdStepper.tsx — a − N + stepper for a percentage threshold: 5 per press, clamped
// 1–100, no input box (so no draft to lose on a tab switch). The look is team/Steppers.tsx's.
import { Minus, Plus } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'

const STEP = 5
const MIN = 1
const MAX = 100
const STEP_BTN = 'w-5 h-5 inline-flex items-center justify-center rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer disabled:opacity-30 disabled:pointer-events-none'

export function ThresholdStepper({ value, onChange, disabled, label, testId }: {
  value: number; onChange: (v: number) => void; disabled?: boolean; label: string; testId?: string
}) {
  const t = useI18nStore((s) => s.t)
  const id = testId ?? 'threshold'
  return (
    <span className="inline-flex items-center gap-1 text-sm" data-testid={id}>
      <button type="button" data-testid={`${id}-minus`} aria-label={t('threshold_stepper.minus', { label })}
        disabled={disabled || value <= MIN} onClick={() => onChange(Math.max(MIN, value - STEP))} className={STEP_BTN}>
        <Minus size={12} />
      </button>
      <span data-testid={`${id}-value`} className="min-w-[3ch] text-center tabular-nums text-text-primary">{value}%</span>
      <button type="button" data-testid={`${id}-plus`} aria-label={t('threshold_stepper.plus', { label })}
        disabled={disabled || value >= MAX} onClick={() => onChange(Math.min(MAX, value + STEP))} className={STEP_BTN}>
        <Plus size={12} />
      </button>
    </span>
  )
}
