// spa/src/components/watch/jobs/HostJobs.tsx — the host tab's 工作 section (plan 7b1-4), a pool host's: three blocks in the jobs tab's own components — on this host now (claimed or running here, any origin: the lane of the jobs tab, one host),
// queued from here (this host's own queue, in display order), and the last five that ended here — then a link to the jobs tab's 歷史 filtered to this host. It only reads: the jobs are the jobs store's (`watchJobs` makes it poll the
// origins no daemon announces while this is mounted), the hosts are the pool's.
//
// Tab-hosted checklist: nothing here is the reader's own choice to keep; the link writes the jobs tab's view memory (`lib/pool/jobs-view-memory`) before it opens that tab.
import { useEffect, useMemo, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { Section } from '../../hosts/form-fields'
import { useJobsStore } from '../../../lib/pool/jobs-store'
import { usePoolStore } from '../../../lib/pool/store'
import { watchJobs } from '../../../lib/pool/jobs-driver-live'
import { hostJobBlocks } from '../../../lib/pool/jobs-host'
import { jobLine, laneModels, queueEntries } from '../../../lib/pool/jobs-lanes'
import { resultText, resultWord, whenText } from '../../../lib/pool/jobs-view'
import { writeHistoryFilter, writeJobsMode } from '../../../lib/pool/jobs-view-memory'
import type { PoolHost } from '../../../lib/pool/types'
import { openWatchArea } from '../../../features/workspace/lib/open-watch-area'
import { JobLanes } from './JobLanes'
import { JobQueue } from './JobQueue'

const KNOWN_STATES = ['succeeded', 'failed', 'cancelled', 'superseded', 'expired']

/** One interval for the elapsed and waited times, cleared on unmount. */
function useNowEverySecond(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])
  return now
}

function Block({ testId, title, children }: { testId: string; title: string; children: React.ReactNode }) {
  return (
    <div data-testid={testId} className="mb-3">
      <h3 className="mb-1.5 text-xs font-medium text-text-secondary">{title}</h3>
      {children}
    </div>
  )
}

export function HostJobs({ host }: { host: PoolHost }) {
  const t = useI18nStore((s) => s.t)
  useEffect(() => watchJobs(), [])
  const now = useNowEverySecond()
  const poolHosts = usePoolStore((s) => s.hosts)
  const byOrigin = useJobsStore((s) => s.byOrigin)
  const jobs = useMemo(() => Object.values(byOrigin).flatMap((o) => Object.values(o.jobs)), [byOrigin])
  const blocks = useMemo(() => hostJobBlocks(jobs, host.alias), [jobs, host.alias])
  const lanes = useMemo(() => laneModels([host], blocks.running), [host, blocks.running])
  const entries = useMemo(() => queueEntries(blocks.queued, now, poolHosts), [blocks.queued, now, poolHosts])

  const openAll = () => {
    writeHistoryFilter({ group: 'all', host: host.alias, repo: '' })
    writeJobsMode('history')
    openWatchArea({ tab: 'jobs' })
  }

  return (
    <Section title={t('watch.host.jobs')}>
      <div data-testid="host-jobs">
        <Block testId="host-jobs-running" title={t('watch.host.jobs.running')}>
          <JobLanes lanes={lanes} now={now} />
        </Block>
        <Block testId="host-jobs-queued" title={t('watch.host.jobs.queued')}>
          <JobQueue entries={entries} now={now} />
        </Block>
        <Block testId="host-jobs-ended" title={t('watch.host.jobs.ended')}>
          {blocks.ended.length === 0
            ? <p data-testid="host-jobs-ended-none" className="text-xs text-text-muted">{t('watch.host.jobs.ended_none')}</p>
            : (
              <ul className="flex flex-col">
                {blocks.ended.map((j) => (
                  <li key={`${j.origin}/${j.id}`} data-testid="host-jobs-ended-row" data-job-id={j.id} data-state={j.state} className="flex items-center gap-2 border-b border-border-subtle/50 py-1.5 text-xs">
                    <span className="w-16 shrink-0 text-text-secondary">{KNOWN_STATES.includes(j.state) ? t(`watch.jobs.state.${j.state}`) : j.state}</span>
                    <span className="min-w-0 flex-1 truncate font-mono text-text-primary" title={jobLine(j)}>{jobLine(j)}</span>
                    <span className="shrink-0 tabular-nums text-text-muted">{whenText(j.result?.finished_at ?? null, now)}</span>
                    <span className="shrink-0 text-text-muted">{resultText(resultWord(j), t)}</span>
                  </li>
                ))}
              </ul>
            )}
        </Block>
        <button type="button" data-testid="host-jobs-all" onClick={openAll} className="text-xs text-text-secondary underline-offset-2 hover:text-text-primary hover:underline cursor-pointer">
          {t('watch.host.jobs.all')}
        </button>
      </div>
    </Section>
  )
}
