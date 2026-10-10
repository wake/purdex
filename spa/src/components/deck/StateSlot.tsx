// spa/src/components/deck/StateSlot.tsx — the status row's state cell (U3-5), Collie's one-of trick: every state word sits
// in the SAME grid cell, the losers are opacity-0 + inert, so the slot is as wide as the widest word and a state change
// repaints it without moving its neighbours. The deck's step chip keeps its own `StatusSlot` (a different, fixed-width box).
import type { ReactNode } from 'react'
import { Prohibit, XCircle } from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { formatElapsed, formatExit } from './status-row-model'

export type SlotState = 'idle' | 'running' | 'failed' | 'denied' | 'exit'

/** widest exit text (a code is shown 0-255, above that 255+). */
const WIDEST_EXIT = '255+'
const WIDEST_CLOCK = '88:88+'

function Chip({ tone, icon: IconCmp, children }: { tone: 'error' | 'muted'; icon: Icon; children: ReactNode }) {
  const cls = tone === 'error' ? 'bg-status-error/15 text-status-error' : 'bg-surface-secondary text-text-muted'
  return <span data-testid="state-chip" className={`flex items-center gap-1 rounded px-1.5 tabular-nums ${cls}`}><IconCmp size={11} weight="fill" aria-hidden="true" />{children}</span>
}

interface Props {
  state: SlotState
  /** exit N, when `state` is 'exit'. */
  exitCode?: number
  /** Time the running step has been going, ms. */
  elapsedMs?: number
  className?: string
}

export function StateSlot({ state, exitCode = 1, elapsedMs = 0, className = '' }: Props) {
  const t = useI18nStore((s) => s.t)
  const idle = (
    <span className="flex items-center gap-1.5 text-text-muted">
      <span className="inline-block h-2 w-2 shrink-0 rounded-full border border-text-muted" />
      <span>{t('chat.status.idle')}</span>
    </span>
  )
  const running = (clock: string) => (
    <span className="flex items-center gap-1.5 text-text-primary">
      <span className="inline-block h-2 w-2 shrink-0 animate-pulse rounded-full bg-accent" />
      <span>{t('chat.status.running')}</span>
      <span className="tabular-nums text-text-muted">{clock}</span>
    </span>
  )
  const exit = (n: string) => <Chip tone="error" icon={XCircle}>{t('deck.status.exit', { n })}</Chip>
  // An inactive option carries the WIDEST text of its kind; the active one adds an invisible copy of that widest text (the
  // sizer), so a short clock or exit 2 never makes the slot narrower than when it is idle.
  const options: Array<{ key: SlotState; node: ReactNode; sizer?: ReactNode }> = [
    { key: 'idle', node: idle },
    { key: 'running', node: running(state === 'running' ? formatElapsed(elapsedMs) : WIDEST_CLOCK), sizer: running(WIDEST_CLOCK) },
    { key: 'failed', node: <Chip tone="error" icon={XCircle}>{t('deck.status.failed')}</Chip> },
    { key: 'denied', node: <Chip tone="muted" icon={Prohibit}>{t('deck.status.denied')}</Chip> },
    { key: 'exit', node: exit(state === 'exit' ? formatExit(exitCode) : WIDEST_EXIT), sizer: exit(WIDEST_EXIT) },
  ]
  return (
    <span data-testid="state-slot" data-state={state} className={`inline-grid shrink-0 items-center justify-items-start whitespace-nowrap text-xs ${className}`}>
      {options.map(({ key, node, sizer }) => {
        const on = key === state
        return (
          <span key={key} data-state-option={key} data-active={on ? 'true' : 'false'} aria-hidden={on ? undefined : true}
            inert={!on} className={`col-start-1 row-start-1 ${on ? '' : 'opacity-0'}`}>
            {on && sizer ? (
              <span className="inline-grid">
                <span data-sizer aria-hidden="true" className="invisible col-start-1 row-start-1">{sizer}</span>
                <span className="col-start-1 row-start-1">{node}</span>
              </span>
            ) : node}
          </span>
        )
      })}
    </span>
  )
}
