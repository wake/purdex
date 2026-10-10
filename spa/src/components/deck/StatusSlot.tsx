// spa/src/components/deck/StatusSlot.tsx — the fixed-width slot at the right of every step line / card (U3 spec §4), so a
// step finishing never shifts the layout: a running dot, or a chip (失敗 / 已拒絕 / 已中斷 / exit N), or empty.
import { useI18nStore } from '../../stores/useI18nStore'
import { stepChip } from '../../lib/conversations/deck-format'
import type { StepItem } from '../../lib/conversations/types'

export function StatusSlot({ step }: { step: Pick<StepItem, 'status' | 'denial' | 'command'> }) {
  const t = useI18nStore((s) => s.t)
  const chip = stepChip(step)
  let body = null
  if (chip?.kind === 'running') {
    body = <span data-testid="step-running" aria-label={t('deck.status.running')} className="inline-block h-2 w-2 animate-pulse rounded-full bg-accent" />
  } else if (chip?.kind === 'failed') {
    body = <span data-testid="step-chip" className="rounded bg-status-error/15 px-1.5 text-status-error">{t('deck.status.failed')}</span>
  } else if (chip?.kind === 'exit') {
    body = <span data-testid="step-chip" className="rounded bg-status-error/15 px-1.5 tabular-nums text-status-error">{t('deck.status.exit', { n: chip.code })}</span>
  } else if (chip?.kind === 'denied') {
    body = <span data-testid="step-chip" className="rounded bg-surface-secondary px-1.5 text-text-muted">{t('deck.status.denied')}</span>
  } else if (chip?.kind === 'interrupted') {
    body = <span data-testid="step-chip" className="rounded bg-surface-secondary px-1.5 text-text-muted">{t('deck.status.interrupted')}</span>
  }
  return <span data-testid="status-slot" className="flex w-20 shrink-0 items-center justify-end text-xs">{body}</span>
}
