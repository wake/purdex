// spa/src/components/deck/StatusSlot.tsx — the fixed-width slot at the right of every step line / card (U3 spec §4), so a
// step finishing never shifts the layout: a chip (失敗 / 已拒絕 / 已中斷 / exit N), or empty.
import { useI18nStore } from '../../stores/useI18nStore'
import { stepChip, stepDot } from '../../lib/conversations/deck-format'
import type { StepItem } from '../../lib/conversations/types'

export function StatusSlot({ step }: { step: Pick<StepItem, 'status' | 'denial' | 'command'> }) {
  const t = useI18nStore((s) => s.t)
  const chip = stepChip(step)
  let body = null
  if (chip?.kind === 'failed') {
    body = <span data-testid="step-chip" className="rounded bg-status-error/15 px-1.5 text-status-error">{t('deck.status.failed')}</span>
  } else if (chip?.kind === 'exit') {
    body = <span data-testid="step-chip" className="rounded bg-status-error/15 px-1.5 tabular-nums text-status-error">{t('deck.status.exit', { n: chip.code })}</span>
  } else if (chip?.kind === 'denied') {
    body = <span data-testid="step-chip" className="rounded bg-surface-secondary px-1.5 text-text-muted">{t('deck.status.denied')}</span>
  } else if (chip?.kind === 'interrupted') {
    body = <span data-testid="step-chip" className="rounded bg-surface-secondary px-1.5 text-text-muted">{t('deck.status.interrupted')}</span>
  }
  return <span data-testid="status-slot" className="flex w-20 shrink-0 items-center justify-end deck-t-down">{body}</span>
}

const DOT_CLASS = { running: 'animate-pulse bg-accent', ok: 'bg-status-success', failed: 'bg-status-error', muted: 'bg-text-muted' } as const

/** The leading dot of a step header (like the terminal's coloured ●): running pulses, ok green, failed red, denied / interrupted grey. */
export function StepDot({ step }: { step: Pick<StepItem, 'status' | 'denial' | 'command'> }) {
  const t = useI18nStore((s) => s.t)
  const state = stepDot(step)
  const cls = `inline-block h-2 w-2 shrink-0 rounded-full ${DOT_CLASS[state]}`
  // Running and ok have no chip next to them, so the dot speaks; failed / muted have a chip (or nothing to say), so the dot is decoration.
  if (state === 'running' || state === 'ok') {
    return <span data-testid="step-dot" data-state={state} role="img" aria-label={t(state === 'running' ? 'deck.status.running' : 'deck.status.ok')} className={cls} />
  }
  return <span data-testid="step-dot" data-state={state} aria-hidden="true" className={cls} />
}
