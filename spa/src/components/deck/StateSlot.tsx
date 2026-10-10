// spa/src/components/deck/StateSlot.tsx — the status row's state cell (U3-5), Collie's one-of trick: every state word sits
// in the SAME grid cell, the losers are opacity-0 + inert, so the slot is as wide as the widest word and a state change
// repaints it without moving its neighbours. The deck's step chip keeps its own `StatusSlot` (a different, fixed-width box).
import type { ReactNode } from 'react'
import { Prohibit, XCircle } from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'

export type SlotState = 'idle' | 'running' | 'failed' | 'denied' | 'exit'

/** `0:42`, `12:05`. */
export function formatElapsed(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

/** exit code that sets the reserved width: three digits is the widest a real one gets (exit 255). */
const WIDEST_EXIT = 255
const WIDEST_CLOCK = '88:88'

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
  const options: Array<{ key: SlotState; node: ReactNode }> = [
    { key: 'idle', node: (
      <span className="flex items-center gap-1.5 text-text-muted">
        <span className="inline-block h-2 w-2 shrink-0 rounded-full border border-text-muted" />
        <span>{t('chat.status.idle')}</span>
      </span>
    ) },
    { key: 'running', node: (
      <span className="flex items-center gap-1.5 text-text-primary">
        <span className="inline-block h-2 w-2 shrink-0 animate-pulse rounded-full bg-accent" />
        <span>{t('chat.status.running')}</span>
        <span className="tabular-nums text-text-muted">{state === 'running' ? formatElapsed(elapsedMs) : WIDEST_CLOCK}</span>
      </span>
    ) },
    { key: 'failed', node: <Chip tone="error" icon={XCircle}>{t('deck.status.failed')}</Chip> },
    { key: 'denied', node: <Chip tone="muted" icon={Prohibit}>{t('deck.status.denied')}</Chip> },
    { key: 'exit', node: <Chip tone="error" icon={XCircle}>{t('deck.status.exit', { n: state === 'exit' ? exitCode : WIDEST_EXIT })}</Chip> },
  ]
  return (
    <span data-testid="state-slot" data-state={state} className={`inline-grid shrink-0 items-center justify-items-start whitespace-nowrap text-xs ${className}`}>
      {options.map(({ key, node }) => {
        const on = key === state
        return (
          <span key={key} data-state-option={key} data-active={on ? 'true' : 'false'} aria-hidden={on ? undefined : true}
            inert={!on} className={`col-start-1 row-start-1${on ? '' : 'opacity-0'}`}>
            {node}
          </span>
        )
      })}
    </span>
  )
}
