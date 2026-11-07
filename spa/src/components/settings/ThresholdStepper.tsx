// spa/src/components/settings/ThresholdStepper.tsx — a − N + stepper for a percentage threshold: it reports a DELTA
// (±5) per press, never an absolute value, so the caller applies it to the value current when its task runs (two quick
// presses add up). Clamped 1–100 (the buttons are off at the ends). No input box, so no draft to lose on a tab switch.
import { useI18nStore } from '../../stores/useI18nStore'
import { StepButton } from '../team/Steppers'
import { THRESHOLD_MAX, THRESHOLD_MIN, THRESHOLD_STEP } from '../../lib/threshold'

export function ThresholdStepper({ value, onStep, disabled, label, testId }: {
  value: number; onStep: (delta: number) => void; disabled?: boolean; label: string; testId?: string
}) {
  const t = useI18nStore((s) => s.t)
  const id = testId ?? 'threshold'
  return (
    <span className="inline-flex items-center gap-1 text-sm" data-testid={id}>
      <StepButton sign="−" testId={`${id}-minus`} label={t('threshold_stepper.minus', { label })}
        disabled={!!disabled || value <= THRESHOLD_MIN} onClick={() => onStep(-THRESHOLD_STEP)} />
      <span data-testid={`${id}-value`} className="min-w-[3ch] text-center tabular-nums text-text-primary">{value}%</span>
      <StepButton sign="+" testId={`${id}-plus`} label={t('threshold_stepper.plus', { label })}
        disabled={!!disabled || value >= THRESHOLD_MAX} onClick={() => onStep(THRESHOLD_STEP)} />
    </span>
  )
}
