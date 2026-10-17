// spa/src/components/BackgroundSymbol.tsx — the corner symbol of a tab's agent icon for background work (spec N6).
//
// One small, static symbol at the top-left of the agent icon (or of the dot in the `dot` style): TreeStructure for a
// workflow, Eye for a monitor, Clock for a scheduled wake-up, Hourglass for an agent waiting in a host's command queue. The colour is the icon's (`currentColor`); background
// SHELL commands draw nothing (the daemon never sends them). The caller decides WHERE it is drawn and that it is not
// drawn at all when the lights are off.
import { Clock, Eye, Hourglass, TreeStructure } from '@phosphor-icons/react'
import type { BackgroundKind } from '../stores/useAgentStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useCoarseNow } from '../hooks/useCoarseNow'
import type { FleetWaiter } from '../lib/fleet/types'
import { queuedTooltip } from '../lib/watch/queued-sessions'

const ICONS = { queued: Hourglass, workflow: TreeStructure, monitor: Eye, schedule: Clock } as const

interface Props {
  kind: BackgroundKind
  /** The waiters of a `queued` symbol: its tooltip names the longest wait (place, minutes, command) and counts the others. */
  waiters?: FleetWaiter[]
  /** Offsets from the top-left of the positioned box it sits in (px). */
  top?: number
  left?: number
  size?: number
}

export function BackgroundSymbol({ waiters, ...rest }: Props) {
  // The minute clock is subscribed only while an hourglass is drawn: a tab with none holds no per-minute subscription.
  return rest.kind === 'queued' && waiters && waiters.length > 0
    ? <QueuedSymbol waiters={waiters} {...rest} />
    : <Symbol {...rest} />
}

function QueuedSymbol({ waiters, ...rest }: Props & { waiters: FleetWaiter[] }) {
  const t = useI18nStore((s) => s.t)
  useCoarseNow() // re-render each minute; the minutes themselves are read from the clock below
  return <Symbol {...rest} title={queuedTooltip(waiters, Date.now(), t)} />
}

function Symbol({ kind, title, top = -3, left = -3, size = 8 }: Omit<Props, 'waiters'> & { title?: string }) {
  const t = useI18nStore((s) => s.t)
  const Icon = ICONS[kind]
  const label = title || t(`tab.background.${kind}`)
  return (
    <span
      data-testid="tab-background-symbol"
      data-kind={kind}
      role="img"
      aria-label={label}
      title={label}
      className="absolute z-20 inline-flex"
      style={{ top, left, lineHeight: 0, color: 'currentColor' }}
    >
      <Icon size={size} weight="bold" />
    </span>
  )
}
