// spa/src/components/watch/jobs/JobQueue.tsx — the queue of 「佇列與執行中」 in *display order* (not a promise of the next claim: each worker scores for itself). A card says the class, `repo task args`, where it was sent from, by which session,
// how long it has waited (yellow past 2 minutes), its labels, and — in red — when no host with its worker on can take it. A held job is dimmed and last.
import { useI18nStore } from '../../../stores/useI18nStore'
import { mss } from '../../../lib/watch/queue-time'
import { jobLine, type QueueEntry } from '../../../lib/pool/jobs-lanes'
import { jobKey } from '../../../lib/pool/jobs-view-memory'

const KNOWN_CLASSES = ['release', 'merge', 'dev']

function QueueCard({ entry, now, selected, onSelect }: { entry: QueueEntry; now: number; selected: boolean; onSelect: (key: string | null) => void }) {
  const t = useI18nStore((s) => s.t)
  const { job, held, noHost, long } = entry
  const cls = KNOWN_CLASSES.includes(job.class) ? t(`watch.jobs.class.${job.class}`) : job.class
  const tag = job.manual === 'front' ? 'watch.jobs.tag.front' : job.manual === 'back' ? 'watch.jobs.tag.back' : held ? 'watch.jobs.tag.hold' : null
  const labels = noHost ? (noHost.labels.length > 0 ? t('watch.jobs.no_host', { labels: noHost.labels.join(' ') }) : t('watch.jobs.no_host_any')) : null
  return (
    <li
      data-testid="job-queued" data-job-id={job.id} data-held={held || undefined} title={jobLine(job)}
      className={`rounded-md border bg-surface-secondary text-xs ${selected ? 'border-border-active' : 'border-border-default'} ${held ? 'opacity-50' : ''}`}
    >
      <button type="button" aria-pressed={selected} onClick={() => onSelect(selected ? null : jobKey(job.origin, job.id))} className="block w-full cursor-pointer px-2.5 py-1.5 text-left">
      <div className="flex items-center gap-2 min-w-0">
        <span data-testid="job-class" className="shrink-0 rounded border border-current px-1.5 text-[10px] leading-4 text-text-secondary">{cls}</span>
        {tag && <span data-testid="job-tag" className="shrink-0 rounded border border-current px-1.5 text-[10px] leading-4 text-text-muted">{t(tag)}</span>}
        {job.local && <span className="shrink-0 text-[10px] text-text-muted">{t('watch.jobs.fast_lane')}</span>}
        <span className="flex-1 min-w-0 truncate font-mono text-text-primary">{jobLine(job)}</span>
        <span data-testid="job-wait" className={`shrink-0 tabular-nums ${long ? 'text-status-warning-text' : 'text-text-muted'}`}>
          {mss(job.created_at, now)}
        </span>
      </div>
      <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[10px] text-text-muted">
        <span>{t('watch.jobs.from', { origin: job.origin })}</span>
        <span data-testid="job-session">{job.submitter.session !== '' ? job.submitter.session : t('watch.jobs.session_none')}</span>
        {job.labels.map((l) => <span key={l} className="rounded border border-border-default px-1">{l}</span>)}
      </div>
      {labels && <div data-testid="job-no-host" className="mt-0.5 text-[11px] text-status-error-text">{labels}</div>}
      </button>
    </li>
  )
}

export function JobQueue({ entries, now, selected, onSelect }: { entries: QueueEntry[]; now: number; selected: string | null; onSelect: (key: string | null) => void }) {
  const t = useI18nStore((s) => s.t)
  if (entries.length === 0) return <p data-testid="job-queue-none" className="text-xs text-text-muted">{t('watch.jobs.queue.none')}</p>
  return (
    <ul data-testid="job-queue" className="flex flex-col gap-1.5">
      {entries.map((e) => <QueueCard key={e.job.id} entry={e} now={now} selected={selected === jobKey(e.job.origin, e.job.id)} onSelect={onSelect} />)}
    </ul>
  )
}
