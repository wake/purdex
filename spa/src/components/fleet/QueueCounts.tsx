// spa/src/components/fleet/QueueCounts.tsx — the narrow sidebar's queue (monitoring tabs spec M15, M25, replacing the strip there): two blocks above the host cells, heavy (purple) and light (teal), each with the number
// of jobs of that kind still waiting — the same jobs as the wide strip's, whatever its segment setting. A kind with none is a neutral grey block and still says 0; a pool nobody can read is two grey 0s. Always drawn, so the
// cells below do not move. Hover lists the waiting jobs (the strip's list, to the right); a click opens the job queue.
import { useId, useMemo, useRef, useState } from 'react'
import { openWatchArea } from '../../features/workspace/lib/open-watch-area'
import { queueEntries } from '../../lib/pool/jobs-lanes'
import { usePoolJobs, usePoolReadable } from '../../lib/pool/pool-jobs'
import { queueStripOf } from '../../lib/pool/queue-strip'
import { usePoolStore } from '../../lib/pool/store'
import { useClock } from '../../lib/pool/use-cell-models'
import { useI18nStore } from '../../stores/useI18nStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { QueueTooltip } from './QueueTooltip'

const figure = (n: number): string => (n > 99 ? '99+' : String(n))

export function QueueCounts() {
  const t = useI18nStore((s) => s.t)
  const nseg = useLayoutStore((s) => s.queueStripSegments)
  const readable = usePoolReadable()
  const jobs = usePoolJobs()
  const hosts = usePoolStore((s) => s.hosts)
  const now = useClock()
  const strip = useMemo(() => queueStripOf(queueEntries(jobs, now, hosts), nseg, readable), [jobs, now, hosts, nseg, readable])
  const ref = useRef<HTMLButtonElement>(null)
  const tipId = useId()
  const [anchor, setAnchor] = useState<DOMRect | null>(null)
  const [open, setOpen] = useState(false)
  const show = () => { setAnchor(ref.current?.getBoundingClientRect() ?? null); setOpen(true) }
  const hide = () => setOpen(false)
  const { heavy, light } = strip.counts

  return (
    <button
      type="button"
      ref={ref}
      data-testid="queue-counts"
      aria-label={t('pool.strip.label', { n: heavy + light })}
      aria-describedby={open ? tipId : undefined}
      onMouseEnter={show}
      onMouseLeave={hide}
      onFocus={show}
      onBlur={hide}
      onClick={() => { hide(); openWatchArea({ tab: 'jobs' }) }}
      className="relative flex shrink-0 cursor-pointer items-center gap-0.5 pb-1 outline-none focus-visible:ring-1 focus-visible:ring-border-active"
    >
      {([['heavy', heavy, 'bg-queue-heavy'], ['light', light, 'bg-queue-light']] as const).map(([kind, n, bg]) => (
        <span
          key={kind}
          data-testid={`queue-count-${kind}`}
          data-n={n}
          className={`inline-flex h-5 min-w-[20px] items-center justify-center rounded px-1 text-[10px] font-semibold leading-none tabular-nums ${n > 0 ? `${bg} text-white` : 'bg-surface-hover text-text-muted'}`}
        >
          {figure(n)}
        </span>
      ))}
      {open && <QueueTooltip id={tipId} strip={strip} placement="right" anchor={anchor} />}
    </button>
  )
}
