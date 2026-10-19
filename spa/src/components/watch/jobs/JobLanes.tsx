// spa/src/components/watch/jobs/JobLanes.tsx — 「佇列與執行中」's lanes (plan "WWD-7 design", Jobs tab): one lane per pool host in the QueueLane language — the host and 「名額 used / total」 at the left, a card per slot,
// dashed free slots, and a gate line that says why the host takes no more. A host whose worker is off is one line, 「不接工作」. A `claimed` job (its workspace being prepared) is a 「準備中」 card — a dashed green
// border, no breathing dot — until it is running; it is never missing from both the lane and the queue.
import { useI18nStore } from '../../../stores/useI18nStore'
import { mss } from '../../../lib/watch/queue-time'
import { reasonWord } from '../../../lib/pool/words'
import { jobLine, type LaneModel } from '../../../lib/pool/jobs-lanes'
import type { PoolJob } from '../../../lib/pool/jobs-types'

const CARD = 'shrink-0 flex items-center gap-1.5 h-7 w-[200px] px-2 rounded-md text-xs font-mono cursor-default'

function JobCard({ job, now }: { job: PoolJob; now: number }) {
  const t = useI18nStore((s) => s.t)
  const preparing = job.state === 'claimed'
  return (
    <div
      data-testid={preparing ? 'job-preparing' : 'job-running'} data-job-id={job.id} title={jobLine(job)}
      className={preparing
        ? `${CARD} border border-dashed border-status-success-text bg-transparent`
        : `${CARD} border border-status-success-text bg-status-success-text/12`}
    >
      {!preparing && <span data-testid="job-dot" aria-hidden="true" className="shrink-0 w-1.5 h-1.5 rounded-full bg-status-success-text animate-breathe motion-reduce:animate-none" />}
      <span className="flex-1 min-w-0 truncate text-text-primary">{jobLine(job)}</span>
      {job.local && <span data-testid="job-fast-lane" className="shrink-0 font-sans text-[10px] text-text-muted">{t('watch.jobs.fast_lane')}</span>}
      {preparing && <span className="shrink-0 font-sans text-[10px] text-text-secondary">{t('watch.jobs.preparing')}</span>}
      <span className="shrink-0 tabular-nums text-text-secondary">{mss(job.claim?.since ?? null, now)}</span>
    </div>
  )
}

function Lane({ lane, now }: { lane: LaneModel; now: number }) {
  const t = useI18nStore((s) => s.t)
  const name = lane.alias === '' ? t('watch.jobs.lane.unknown') : lane.alias
  const free = Math.max(0, lane.total - lane.jobs.length)
  return (
    <div data-testid={`job-lane-${lane.alias}`} data-off={lane.off || undefined} className="flex items-start gap-2.5 py-2">
      <div className="w-28 shrink-0">
        <div className="text-sm font-medium text-text-primary truncate">{name}</div>
        {!lane.off && <div className="text-[10px] text-text-muted tabular-nums">{t('watch.queue.slots', { used: lane.used, total: lane.total })}</div>}
      </div>
      <div className="flex-1 min-w-0 flex flex-wrap items-center gap-1.5">
        {lane.off && lane.jobs.length === 0 && <span data-testid="job-lane-off" className="text-xs text-text-muted">{t('watch.jobs.lane.off')}</span>}
        {lane.jobs.map((j) => <JobCard key={j.id} job={j} now={now} />)}
        {!lane.off && Array.from({ length: free }).map((_, i) => (
          <div key={`free${i}`} data-testid="job-free" className="shrink-0 h-7 w-[200px] rounded-md border border-dashed border-border-default grid place-items-center text-[10px] text-text-muted">{t('watch.queue.free')}</div>
        ))}
        {lane.reason !== null && (
          <span data-testid="job-gate" className="shrink-0 flex items-center gap-1.5 min-w-[64px]">
            <span className="w-px h-5 bg-border-default" />
            <span className="text-[10px] whitespace-nowrap text-text-muted">{reasonWord(lane.reason, t)}</span>
          </span>
        )}
      </div>
    </div>
  )
}

export function JobLanes({ lanes, now }: { lanes: LaneModel[]; now: number }) {
  return (
    <div data-testid="job-lanes" className="divide-y divide-border-subtle/50">
      {lanes.map((l) => <Lane key={l.alias} lane={l} now={now} />)}
    </div>
  )
}
