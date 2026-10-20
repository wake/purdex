// spa/src/components/fleet/QueueStrip.tsx — the resource pool's queue as one line above the sidebar's host cells (monitoring tabs spec M1–M5, M25). One segment per job still waiting (heavy purple, light teal), the rest
// grey; more waiting than segments: "+N" on the last one. It is always there, grey when nothing waits or the pool cannot be read, so the cells below do not move. Hover lists the first ten; a click opens the job queue.
import { useMemo, useRef, useState } from 'react'
import { openWatchArea } from '../../features/workspace/lib/open-watch-area'
import { queueEntries } from '../../lib/pool/jobs-lanes'
import { usePoolJobs, usePoolReadable } from '../../lib/pool/pool-jobs'
import { QUEUE_STRIP_SEGMENTS, queueStripOf } from '../../lib/pool/queue-strip'
import { usePoolStore } from '../../lib/pool/store'
import { useClock } from '../../lib/pool/use-cell-models'
import { useI18nStore } from '../../stores/useI18nStore'
import { CountBadge } from './CountBadge'
import { QueueTooltip } from './QueueTooltip'

const SEGMENT_CLASS = { heavy: 'bg-queue-heavy', light: 'bg-queue-light', empty: 'bg-border-default' } as const

export function QueueStrip() {
  const t = useI18nStore((s) => s.t)
  const readable = usePoolReadable()
  const jobs = usePoolJobs()
  const hosts = usePoolStore((s) => s.hosts)
  const now = useClock()
  const strip = useMemo(() => queueStripOf(queueEntries(jobs, now, hosts), QUEUE_STRIP_SEGMENTS, readable), [jobs, now, hosts, readable])
  const ref = useRef<HTMLButtonElement>(null)
  const [anchor, setAnchor] = useState<DOMRect | null>(null)
  const [open, setOpen] = useState(false)
  const show = () => { setAnchor(ref.current?.getBoundingClientRect() ?? null); setOpen(true) }
  const hide = () => setOpen(false)
  const waiting = strip.counts.heavy + strip.counts.light
  const kinds = Array.from({ length: QUEUE_STRIP_SEGMENTS }, (_, i) => strip.segments[i] ?? 'empty')

  return (
    <button
      type="button"
      ref={ref}
      data-testid="queue-strip"
      aria-label={t('pool.strip.label', { n: waiting })}
      onMouseEnter={show}
      onMouseLeave={hide}
      onFocus={show}
      onBlur={hide}
      onClick={() => { hide(); openWatchArea({ tab: 'jobs' }) }}
      className="relative flex h-1.5 w-full shrink-0 cursor-pointer items-stretch gap-px outline-none"
    >
      {kinds.map((kind, i) => (
        <span key={i} data-testid={`queue-seg-${i}`} data-kind={kind} className={`relative min-w-0 flex-1 rounded-[1px] ${SEGMENT_CLASS[kind]}`}>
          {i === kinds.length - 1 && strip.overflow > 0 && (
            <CountBadge n={strip.overflow} label={`+${strip.overflow}`} size="sm" className="bg-text-muted text-white pointer-events-none" testId="queue-overflow" />
          )}
        </span>
      ))}
      {open && <QueueTooltip strip={strip} placement="above" anchor={anchor} />}
    </button>
  )
}
