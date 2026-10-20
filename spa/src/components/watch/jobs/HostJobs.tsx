// spa/src/components/watch/jobs/HostJobs.tsx — the monitoring tab's 工作 column (monitoring tabs spec M41-3, plan MON-6e task 2), a pool host's: one narrow column of three short lists — on this host now (claimed or running here, any origin; a badge when another host sent it),
// queued from here (this host's own queue in display order, three at most then 「還有 n 件」), and the last jobs that ended here (as many as keep the column about as tall as the two process tables) — then a link to the jobs tab's 歷史 filtered to this host.
// It only reads: the jobs are the jobs store's (`watchJobs` makes it poll the origins no daemon announces while this is mounted), the hosts are the pool's.
//
// Tab-hosted checklist: nothing here is the reader's own choice to keep; the links write the jobs tab's view memory (`lib/pool/jobs-view-memory`) before they open that tab.
import { useEffect, useMemo, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useSidebarHostBadgeGeometry } from '../../../hooks/useSidebarHostBadgeGeometry'
import { resolveHostBadge } from '../../../lib/host-color'
import { mss } from '../../../lib/watch/queue-time'
import { useJobsStore } from '../../../lib/pool/jobs-store'
import { usePoolStore } from '../../../lib/pool/store'
import { watchJobs } from '../../../lib/pool/jobs-driver-live'
import { endedShownOf, hostJobBlocks, HOST_QUEUE_SHOWN } from '../../../lib/pool/jobs-host'
import { usePoolHostLookResolver } from '../../../lib/pool/host-looks'
import { jobLine, queueEntries } from '../../../lib/pool/jobs-lanes'
import { resultText, resultWord, whenText } from '../../../lib/pool/jobs-view'
import { writeHistoryFilter, writeJobsMode } from '../../../lib/pool/jobs-view-memory'
import type { PoolHost } from '../../../lib/pool/types'
import { openWatchArea } from '../../../features/workspace/lib/open-watch-area'
import { HostBadge } from '../../HostBadge'

const KNOWN_STATES = ['succeeded', 'failed', 'cancelled', 'superseded', 'expired']
const KNOWN_CLASSES = ['release', 'merge', 'dev']
const ROW = 'flex items-center gap-1.5 border-b border-border-subtle/50 py-1 text-xs'

/** One interval for the elapsed and waited times, cleared on unmount. */
function useNowEverySecond(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])
  return now
}

function Block({ testId, title, count, countTestId, children }: { testId: string; title: string; count?: number; countTestId?: string; children: React.ReactNode }) {
  return (
    <div data-testid={testId} className="mb-3">
      <h3 className="mb-1 flex items-baseline gap-1.5 text-xs font-medium text-text-secondary">
        {title}
        {count !== undefined && count > 0 && <span data-testid={countTestId} className="tabular-nums font-normal text-text-muted">{count}</span>}
      </h3>
      {children}
    </div>
  )
}

function OriginBadge({ look, title }: { look: Parameters<typeof resolveHostBadge>[0] | undefined; title: string }) {
  const { lineColor } = useSidebarHostBadgeGeometry()
  const b = resolveHostBadge(look ?? {}, 'console')
  return (
    <span data-testid="host-jobs-run-origin" title={title} className="inline-flex shrink-0">
      <HostBadge colors={b.colors} icon={b.icon} iconWeight={b.iconWeight} box={13} inset={2} radius={3} lineColor={lineColor} />
    </span>
  )
}

export function HostJobs({ host }: { host: PoolHost }) {
  const t = useI18nStore((s) => s.t)
  useEffect(() => watchJobs(), [])
  const now = useNowEverySecond()
  const poolHosts = usePoolStore((s) => s.hosts)
  const lookOf = usePoolHostLookResolver()
  const byOrigin = useJobsStore((s) => s.byOrigin)
  // The store keeps what it read, a host that has left the pool too: the section shows the pool's hosts' jobs, as the jobs tab does.
  const jobs = useMemo(() => {
    const aliases = new Set(poolHosts.map((h) => h.alias))
    return Object.entries(byOrigin).filter(([origin]) => aliases.has(origin)).flatMap(([, o]) => Object.values(o.jobs))
  }, [byOrigin, poolHosts])
  const blocks = useMemo(() => hostJobBlocks(jobs, host.alias), [jobs, host.alias])
  const entries = useMemo(() => queueEntries(blocks.queued, now, poolHosts), [blocks.queued, now, poolHosts])
  const lookOfAlias = (alias: string) => { const h = poolHosts.find((p) => p.alias === alias); return h ? lookOf(h.host_id) : undefined }
  const endedShown = blocks.ended.slice(0, endedShownOf(blocks.running.length, blocks.queued.length))
  const queuedMore = entries.length - HOST_QUEUE_SHOWN

  const openAll = () => {
    writeHistoryFilter({ group: 'all', host: host.alias, repo: '' })
    writeJobsMode('history')
    openWatchArea({ tab: 'jobs' })
  }
  const openQueue = () => {
    writeJobsMode('live')
    openWatchArea({ tab: 'jobs' })
  }

  return (
    <div data-testid="host-jobs" className="min-w-0">
      {blocks.running.length > 0 && (
        <Block testId="host-jobs-running" title={t('watch.host.jobs.running')} count={blocks.running.length}>
          <ul>
            {blocks.running.map((j) => {
              const preparing = j.state === 'claimed'
              return (
                <li key={`${j.origin}/${j.id}`} data-testid="host-jobs-run-row" data-job-id={j.id} data-state={j.state} title={jobLine(j)} className={ROW}>
                  <span className="shrink-0 text-status-success-text">{preparing ? t('watch.jobs.preparing') : t('watch.host.jobs.state_running')}</span>
                  {j.origin !== host.alias && <OriginBadge look={lookOfAlias(j.origin)} title={t('watch.jobs.from', { origin: j.origin })} />}
                  <span className="min-w-0 flex-1 truncate font-mono text-text-primary">{jobLine(j)}</span>
                  {!preparing && <span data-testid="host-jobs-run-time" className="shrink-0 tabular-nums text-text-muted">{mss(j.started_at ?? j.claim?.since ?? null, now)}</span>}
                  <span data-testid="host-jobs-run-slot" className="shrink-0 text-[10px] text-text-muted">{t(`watch.jobs.flow.class.${j.slot === 'light' ? 'light' : 'heavy'}`)}</span>
                </li>
              )
            })}
          </ul>
        </Block>
      )}
      <Block testId="host-jobs-queued" title={t('watch.host.jobs.queued')} count={entries.length} countTestId="host-jobs-queued-count">
        {entries.length === 0
          ? <p data-testid="host-jobs-queued-none" className="text-xs text-text-muted">{t('watch.jobs.queue.none')}</p>
          : (
            <ul>
              {entries.slice(0, HOST_QUEUE_SHOWN).map(({ job, long, held }) => (
                <li key={`${job.origin}/${job.id}`} data-testid="host-jobs-queue-row" data-job-id={job.id} data-held={held || undefined} title={jobLine(job)} className={`${ROW} ${held ? 'opacity-50' : ''}`}>
                  <span className="shrink-0 text-text-secondary">{KNOWN_CLASSES.includes(job.class) ? t(`watch.jobs.class.${job.class}`) : job.class}</span>
                  <span className="min-w-0 flex-1 truncate font-mono text-text-primary">{jobLine(job)}</span>
                  <span data-testid="host-jobs-queue-wait" className={`shrink-0 tabular-nums ${long ? 'text-status-warning-text' : 'text-text-muted'}`}>{mss(job.created_at, now)}</span>
                </li>
              ))}
              {queuedMore > 0 && (
                <li>
                  <button type="button" data-testid="host-jobs-queued-more" onClick={openQueue} className="py-1 text-xs text-text-secondary underline-offset-2 hover:text-text-primary hover:underline cursor-pointer">
                    {t('watch.host.jobs.queued_more', { n: queuedMore })}
                  </button>
                </li>
              )}
            </ul>
          )}
      </Block>
      <Block testId="host-jobs-ended" title={t('watch.host.jobs.ended')}>
        {endedShown.length === 0
          ? <p data-testid="host-jobs-ended-none" className="text-xs text-text-muted">{t('watch.host.jobs.ended_none')}</p>
          : (
            <ul>
              {endedShown.map((j) => (
                <li key={`${j.origin}/${j.id}`} data-testid="host-jobs-ended-row" data-job-id={j.id} data-state={j.state} title={jobLine(j)} className={ROW}>
                  <span className="shrink-0 text-text-secondary">{KNOWN_STATES.includes(j.state) ? t(`watch.jobs.state.${j.state}`) : j.state}</span>
                  <span className="min-w-0 flex-1 truncate font-mono text-text-primary">{jobLine(j)}</span>
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
  )
}
