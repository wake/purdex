// spa/src/components/watch/jobs/JobsPane.tsx — the 任務佇列 tab of the watcher area (monitoring tabs MON-5): 「佇列與執行中」 (the queue flow, then the last jobs that ended) and 「歷史」. It only reads: the jobs are the jobs
// store's (the driver keeps it, `watchJobs` makes it poll the origins no daemon announces while this is mounted), the hosts are the pool's.
//
// Tab-hosted checklist: the pane unmounts when another tab is shown (keepAliveCount 0), so the mode (and 歷史's filters, selection and scroll) live in `lib/pool/jobs-view-memory`, not in component state. The jobs themselves are in the store, outside the pane.
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { PaneRendererProps } from '../../../lib/module-registry'
import { useI18nStore } from '../../../stores/useI18nStore'
import { SegmentControl } from '../../settings/SegmentControl'
import { useJobsStore } from '../../../lib/pool/jobs-store'
import { usePoolStore } from '../../../lib/pool/store'
import { useRunPool } from '../../../lib/pool/use-cell-models'
import { watchJobs } from '../../../lib/pool/jobs-driver-live'
import { queueEntries } from '../../../lib/pool/jobs-lanes'
import { recentEndedOf } from '../../../lib/pool/queue-flow'
import { useMonitorHosts } from '../../../lib/pool/use-monitor-hosts'
import { usePoolJobs } from '../../../lib/pool/pool-jobs'
import { errorTextKey } from '../../../lib/pool/words'
import { jobKey, readJobsMode, readLiveScroll, readSelectedJob, writeJobsMode, writeLiveScroll, writeSelectedJob, type JobsMode } from '../../../lib/pool/jobs-view-memory'
import { QueueFlow } from './QueueFlow'
import { RecentEnded } from './RecentEnded'
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
  const monitorHosts = useMonitorHosts()
  const looks = useMemo(() => Object.fromEntries(monitorHosts.map((h) => [h.alias, h.look])), [monitorHosts])
  const recent = useMemo(() => recentEndedOf(jobs), [jobs])
  // The live view's scroll position outlives the pane (tab-hosted): restored when it mounts, written when it scrolls.
  const liveBox = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => { if (liveBox.current) liveBox.current.scrollTop = readLiveScroll() }, [mode])
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
      </div>
      {failed.map((f) => <p key={f.origin} data-testid="jobs-error" className="text-xs text-status-warning-text">{f.origin}：{f.text}</p>)}
      {oldDaemon && <p data-testid="jobs-no-pool" className="text-xs text-text-muted">{t('watch.jobs.no_pool')}</p>}
      <div className="flex min-h-0 flex-1 gap-3">
        <div
          ref={mode === 'live' ? liveBox : undefined} data-testid={mode === 'live' ? 'jobs-live-scroll' : undefined}
          onScroll={mode === 'live' ? (e) => writeLiveScroll(e.currentTarget.scrollTop) : undefined}
          className={`flex min-h-0 min-w-0 flex-1 flex-col gap-3 ${mode === 'live' ? 'overflow-y-auto' : 'overflow-hidden'}`}
        >
          {mode === 'live'
            ? (
              <>
                <QueueFlow hosts={monitorHosts} jobs={jobs} entries={entries} now={now} selected={selected} onSelect={select} />
                <RecentEnded jobs={recent} now={now} looks={looks} selected={selected} onSelect={select} />
              </>
            )
            : <JobHistory jobs={jobs} now={now} selected={selected} onSelect={select} />}
        </div>
        {open && <div className="flex min-h-0 w-[26rem] max-w-[45%] shrink-0 flex-col"><JobDetail job={open} now={now} onClose={() => select(null)} /></div>}
      </div>
    </div>
  )
}
