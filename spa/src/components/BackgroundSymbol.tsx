// spa/src/components/BackgroundSymbol.tsx — the corner symbol of a tab's agent icon for background work (spec N6).
//
// One small, static symbol at the top-left of the agent icon (or of the dot in the `dot` style): TreeStructure for a
// workflow, Eye for a monitor, Clock for a scheduled wake-up, Hourglass for an agent waiting in a host's command queue. The colour is the icon's (`currentColor`); background
// SHELL commands draw nothing (the daemon never sends them). The caller decides WHERE it is drawn and that it is not
// drawn at all when the lights are off.
import { Clock, Eye, Hourglass, TreeStructure } from '@phosphor-icons/react'
import type { MouseEvent, PointerEvent } from 'react'
import type { BackgroundKind } from '../stores/useAgentStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useCoarseNow } from '../hooks/useCoarseNow'
import { openWatchArea } from '../features/workspace/lib/open-watch-area'
import { jobKey, writeJobsMode, writeSelectedJob } from '../lib/pool/jobs-view-memory'
import { isJobMark, queuedTooltip, type QueuedItem } from '../lib/watch/queued-sessions'

const ICONS = { queued: Hourglass, workflow: TreeStructure, monitor: Eye, schedule: Clock } as const

interface Props {
  kind: BackgroundKind
  /** What a `queued` symbol stands for: pool jobs (their place or where they run) and, until WWD-9, gate waiters (the longest wait, the others counted). */
  waiters?: QueuedItem[]
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

function QueuedSymbol({ waiters, ...rest }: Props & { waiters: QueuedItem[] }) {
  const t = useI18nStore((s) => s.t)
  useCoarseNow() // re-render each minute; the minutes themselves are read from the clock below
  // Date.now(), not the coarse minute: the minute value would understate a wait by up to a minute. The tick above re-renders.
  // The hourglass of a pool job is a way into the job queue (M34, M40); one that stands only for gate waiters is not pressable, as before.
  const first = waiters.find(isJobMark)
  const open = first
    ? () => { writeJobsMode('live'); writeSelectedJob(jobKey(first.origin, first.jobId)); openWatchArea({ tab: 'jobs' }) }
    : undefined
  // eslint-disable-next-line react-hooks/purity
  return <Symbol {...rest} title={queuedTooltip(waiters, Date.now(), t)} onOpen={open} />
}

/** `onOpen`: a press on the symbol. It does not go up: the tab holding it is itself a button, and the drag of a tab starts on a pointer-down. */
function Symbol({ kind, title, top = -3, left = -3, size = 8, onOpen }: Omit<Props, 'waiters'> & { title?: string; onOpen?: () => void }) {
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
      className={`absolute z-20 inline-flex ${onOpen ? 'cursor-pointer' : ''}`}
      {...(onOpen ? { onClick: (e: MouseEvent) => { e.stopPropagation(); onOpen() }, onPointerDown: (e: PointerEvent) => e.stopPropagation() } : {})}
      style={{ top, left, lineHeight: 0, color: 'currentColor' }}
    >
      <Icon size={size} weight="bold" />
    </span>
  )
}
