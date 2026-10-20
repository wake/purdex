// spa/src/components/watch/jobs/JobsPane.tsx — the 工作 tab of the watcher area (plan "WWD-7 design", Jobs tab): 「佇列與執行中」 (lanes per host, then the queue) and 「歷史」. It only reads: the jobs are the jobs
// store's (the driver keeps it, `watchJobs` makes it poll the origins no daemon announces while this is mounted), the hosts are the pool's.
//
// Tab-hosted checklist: the pane unmounts when another tab is shown (keepAliveCount 0), so the mode (and 歷史's filters, selection and scroll) live in `lib/pool/jobs-view-memory`, not in component state. The jobs themselves are in the store, outside the pane.
import { useEffect, useMemo, useState } from 'react'
import type { PaneRendererProps } from '../../../lib/module-registry'
import { useI18nStore } from '../../../stores/useI18nStore'
import { SegmentControl } from '../../settings/SegmentControl'
import { useJobsStore } from '../../../lib/pool/jobs-store'
import { usePoolStore } from '../../../lib/pool/store'
import { useRunPool } from '../../../lib/pool/use-cell-models'
import { watchJobs } from '../../../lib/pool/jobs-driver-live'
import { laneModels, queueEntries } from '../../../lib/pool/jobs-lanes'
import { usePoolJobs } from '../../../lib/pool/pool-jobs'
import { errorTextKey } from '../../../lib/pool/words'
import { jobKey, readJobsMode, readSelectedJob, writeJobsMode, writeSelectedJob, type JobsMode } from '../../../lib/pool/jobs-view-memory'
import { JobLanes } from './JobLanes'
import { JobQueue } from './JobQueue'
import { JobHistory } from './JobHistory'
import { JobDetail } from './JobDetail'

/** One interval for the elapsed and waited times, cleared on unmount. */
function useNowEverySecond(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])
  return now
}

export function JobsPane(_props: PaneRendererProps) {
  const t = useI18nStore((s) => s.t)
  useRunPool()
  useEffect(() => watchJobs(), [])
  const now = useNowEverySecond()
  const hosts = usePoolStore((s) => s.hosts)
  const oldDaemon = usePoolStore((s) => s.oldDaemon)
  const errors = useJobsStore((s) => s.errors)
  // The tab outlives this component (the pane unmounts on a tab switch): the memory is the source, the state only re-renders.
  const [mode, setModeState] = useState<JobsMode>(() => readJobsMode())
  const setMode = (m: JobsMode) => { writeJobsMode(m); setModeState(m) }
  const [selected, setSelectedState] = useState<string | null>(() => readSelectedJob())
  const select = (key: string | null) => { writeSelectedJob(key); setSelectedState(key) }

  // The store keeps what it read; the pane shows the pool's hosts' jobs (`poolJobsOf`: a pool that could not be read for a moment keeps what was read).
  const answered = usePoolStore((s) => s.sourceHostId !== null)
  const jobs = usePoolJobs()
  const shown = useMemo((): ((origin: string) => boolean) => {
    if (hosts.length > 0 || answered) {
      const aliases = new Set(hosts.map((h) => h.alias))
      return (origin) => aliases.has(origin)
    }
    return () => !oldDaemon
  }, [hosts, answered, oldDaemon])
  // The selected job as the store holds it now. One the store no longer has (it keeps the newest 200 ended ones) shows no detail; the memory keeps the choice, which is not an error.
  const open = useMemo(() => jobs.find((j) => jobKey(j.origin, j.id) === selected) ?? null, [jobs, selected])
  const lanes = useMemo(() => laneModels(hosts, jobs), [hosts, jobs])
  const entries = useMemo(() => queueEntries(jobs, now, hosts), [jobs, now, hosts])
  const failed = Object.entries(errors).flatMap(([origin, e]) => {
    const key = e && shown(origin) ? errorTextKey(e.code) : null
    return key ? [{ origin, text: t(key) }] : []
  })

  return (
    <div data-testid="jobs-pane" className="h-full min-h-0 p-4 flex flex-col gap-3 overflow-hidden">
      <div className="flex items-center gap-3 text-xs">
        <span role="group" aria-label={t('watch.jobs.mode_label')} data-testid="jobs-mode">
          <SegmentControl
            options={[{ value: 'live', label: t('watch.jobs.mode.live') }, { value: 'history', label: t('watch.jobs.mode.history') }]}
            value={mode} onChange={setMode}
          />
        </span>
        {mode === 'live' && <span data-testid="jobs-count" className="tabular-nums text-text-secondary">{t('watch.jobs.queue.count', { n: entries.length })}</span>}
      </div>
      {failed.map((f) => <p key={f.origin} data-testid="jobs-error" className="text-xs text-status-warning-text">{f.origin}：{f.text}</p>)}
      {oldDaemon && <p data-testid="jobs-no-pool" className="text-xs text-text-muted">{t('watch.jobs.no_pool')}</p>}
      <div className="flex min-h-0 flex-1 gap-3">
        <div className={`flex min-h-0 min-w-0 flex-1 flex-col gap-3 ${mode === 'live' ? 'overflow-y-auto' : 'overflow-hidden'}`}>
          {mode === 'live'
            ? (
              <>
                <JobLanes lanes={lanes} now={now} selected={selected} onSelect={select} />
                <div>
                  <h3 className="mb-1.5 text-xs font-medium text-text-secondary">{t('watch.jobs.queue.title')}</h3>
                  <JobQueue entries={entries} now={now} selected={selected} onSelect={select} />
                </div>
              </>
            )
            : <JobHistory jobs={jobs} now={now} selected={selected} onSelect={select} />}
        </div>
        {open && <div className="flex min-h-0 w-[26rem] max-w-[45%] shrink-0 flex-col"><JobDetail job={open} now={now} onClose={() => select(null)} /></div>}
      </div>
    </div>
  )
}
