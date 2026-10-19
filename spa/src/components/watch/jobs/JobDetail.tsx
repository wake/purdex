// spa/src/components/watch/jobs/JobDetail.tsx — the detail of the selected job in the jobs tab (plan "WWD-7 design", Jobs tab, detail panel): the result line, the failures and the tail first, then the log (the detail's own up to 1 MiB; a larger one
// loads on demand; a running job says it comes at the end), then who and where: id, repo @ commit, class, origin, submitter, host and attempt, queued and run time, labels, cache hit.
//
// Tab-hosted checklist: the pane unmounts when another tab is shown, so the log's scroll position lives in `lib/pool/jobs-view-memory` (the selection is there too); the log itself is read again when the panel comes back.
import { useCallback, useState, type ReactNode } from 'react'
import { JobMenu, type MenuNotice } from './JobMenu'
import { useI18nStore } from '../../../stores/useI18nStore'
import { mss } from '../../../lib/watch/queue-time'
import { jobLine } from '../../../lib/pool/jobs-lanes'
import { jobRanOn, resultText, resultWord, whenText } from '../../../lib/pool/jobs-view'
import { jobKey, readLogScroll, writeLogScroll } from '../../../lib/pool/jobs-view-memory'
import { logNote, useJobDetail } from '../../../lib/pool/job-detail'
import { showSubmitterTab, useSubmitterTab } from '../../../lib/pool/submitter-tab'
import { errorTextKey } from '../../../lib/pool/words'
import { isEnded, type PoolJob } from '../../../lib/pool/jobs-types'
import type { PoolError } from '../../../lib/pool/errors'

const KNOWN_STATES = ['succeeded', 'failed', 'cancelled', 'superseded', 'expired']
const KNOWN_CLASSES = ['release', 'merge', 'dev']
const LOG_BOX = 'max-h-72 overflow-auto rounded border border-border-default bg-surface-secondary p-2 font-mono text-[11px] leading-4 text-text-primary whitespace-pre-wrap break-all'

function Row({ label, testId, children }: { label: string; testId: string; children: ReactNode }) {
  return (
    <div className="flex gap-2 text-xs">
      <dt className="w-20 shrink-0 text-text-muted">{label}</dt>
      <dd data-testid={testId} className="min-w-0 break-words text-text-primary">{children}</dd>
    </div>
  )
}

/** The log text with its scroll position kept: the box is new each time the panel opens, so the memory's position is put back when it appears. */
function LogText({ id, text }: { id: string; text: string }) {
  const boxRef = useCallback((el: HTMLPreElement | null) => { if (el) el.scrollTop = readLogScroll(id) }, [id])
  return <pre ref={boxRef} data-testid="job-log" onScroll={(e) => writeLogScroll(id, e.currentTarget.scrollTop)} className={LOG_BOX}>{text}</pre>
}

export function JobDetail({ job, now, onClose }: { job: PoolJob; now: number; onClose: () => void }) {
  const t = useI18nStore((s) => s.t)
  const { detail, full, loadFull } = useJobDetail(job)
  const submitterTab = useSubmitterTab(job.submitter.session)
  const key = jobKey(job.origin, job.id)
  // What a change that did not go through said, for the job it was made on: another job's panel does not show it.
  const [notice, setNotice] = useState<(MenuNotice & { key: string }) | null>(null)
  const r = job.result
  const inline = detail.phase === 'ready' ? detail.log : null
  const note = logNote(job, inline, now)
  const host = jobRanOn(job)
  const result = resultText(resultWord(job), t)
  // `pool.error.unavailable` names the daemon the pool falls back to — not a thing a job's detail has — so the detail says it in its own words; the host-failing line names the job's origin.
  const errorLine = (e: PoolError): string => {
    if (e.code === 'no_answer' || e.code === 'wwd_unavailable') return t('watch.jobs.detail.read_unavailable')
    const k = errorTextKey(e.code)
    return k ? t(k, { host: job.origin }) : t('watch.jobs.detail.read_error')
  }
  const ranFor = r?.started_at != null && r.finished_at != null ? mss(r.started_at, r.finished_at) : isEnded(job.state) ? '—' : mss(job.claim?.since ?? null, now)

  return (
    <section data-testid="job-detail" aria-label={t('watch.jobs.detail.title')} className="flex min-h-0 flex-col gap-3 overflow-y-auto rounded-md border border-border-default bg-surface-primary p-3">
      <header className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <div data-testid="job-detail-line" className="break-all font-mono text-sm text-text-primary">{jobLine(job)}</div>
          <div data-testid="job-detail-state" className="text-xs text-text-secondary">{KNOWN_STATES.includes(job.state) ? t(`watch.jobs.state.${job.state}`) : job.state}{result !== '' && ` · ${result}`}</div>
        </div>
        <JobMenu job={job} onNotice={(n) => setNotice(n === null ? null : { key, ...n })} />
        <button type="button" onClick={onClose} aria-label={t('watch.jobs.detail.close')} className="shrink-0 rounded px-1.5 text-xs text-text-muted hover:text-text-primary">✕</button>
      </header>

      {notice?.key === key && (
        <p data-testid="job-menu-notice" className="flex items-center gap-2 text-xs text-status-warning-text">
          <span className="min-w-0 break-words">{notice.text}</span>
          {notice.retry && <button type="button" data-testid="job-menu-retry" onClick={notice.retry} className="shrink-0 rounded border border-current px-1.5">{t('watch.jobs.menu.retry')}</button>}
        </p>
      )}
      {detail.phase === 'error' && <p data-testid="job-detail-error" className="text-xs text-status-warning-text">{errorLine(detail.error)}</p>}

      {r?.summary && r.summary.failures.length > 0 && (
        <div data-testid="job-failures" className="flex flex-col gap-1">
          <h4 className="text-xs font-medium text-text-secondary">{t('watch.jobs.detail.failures')}</h4>
          {r.summary.failures.map((f, i) => (
            <div key={i} className="text-xs">
              <span className="font-mono text-status-error-text">{f.name}</span>
              {f.file !== '' && <span className="text-text-muted"> {f.file}</span>}
              {f.message !== '' && <div className="break-words text-text-secondary">{f.message}</div>}
            </div>
          ))}
        </div>
      )}
      {r?.summary && r.summary.tail.length > 0 && (
        <div className="flex flex-col gap-1">
          <h4 className="text-xs font-medium text-text-secondary">{t('watch.jobs.detail.tail')}</h4>
          <pre data-testid="job-tail" className={LOG_BOX}>{r.summary.tail.join('\n')}</pre>
        </div>
      )}

      <div className="flex flex-col gap-1">
        <h4 className="text-xs font-medium text-text-secondary">{t('watch.jobs.detail.log')}</h4>
        {note === 'running' && <p data-testid="job-log-note" className="text-xs text-text-muted">{t('watch.jobs.detail.log_running')}</p>}
        {note === 'uploading' && <p data-testid="job-log-note" className="text-xs text-text-muted">{t('watch.jobs.detail.log_uploading')}</p>}
        {note === 'expired' && <p data-testid="job-log-note" className="text-xs text-text-muted">{t('watch.jobs.detail.log_expired')}</p>}
        {note === 'none' && <p data-testid="job-log-note" className="text-xs text-text-muted">—</p>}
        {note === 'text' && inline !== null && <LogText id={key} text={inline} />}
        {note === 'on_demand' && (
          full.phase === 'ready'
            ? <LogText id={key} text={full.text} />
            : (
              <div className="flex items-center gap-2 text-xs">
                <button type="button" data-testid="job-log-load" disabled={full.phase === 'loading'} onClick={loadFull} className="rounded border border-border-default px-2 py-0.5 text-text-primary hover:bg-surface-secondary disabled:opacity-50">
                  {t(full.phase === 'loading' ? 'watch.jobs.detail.log_loading' : 'watch.jobs.detail.log_load')}
                </button>
                {full.phase === 'error' && <span data-testid="job-log-error" className="text-status-warning-text">{errorLine(full.error)}</span>}
              </div>
            )
        )}
      </div>

      <dl className="flex flex-col gap-1">
        <Row label={t('watch.jobs.detail.id')} testId="job-id"><span className="font-mono">{job.id}</span></Row>
        <Row label={t('watch.jobs.detail.repo')} testId="job-repo"><span className="font-mono">{job.repo.name}@{job.snapshot.commit.slice(0, 8)}</span></Row>
        <Row label={t('watch.jobs.detail.class')} testId="job-class-row">{KNOWN_CLASSES.includes(job.class) ? t(`watch.jobs.class.${job.class}`) : job.class}</Row>
        <Row label={t('watch.jobs.detail.origin')} testId="job-origin">{job.origin}</Row>
        <Row label={t('watch.jobs.detail.submitter')} testId="job-submitter">
          {job.submitter.session === ''
            ? t('watch.jobs.session_none')
            : submitterTab
              ? <button type="button" data-testid="job-submitter-link" title={t('watch.jobs.detail.submitter_open')} onClick={() => showSubmitterTab(submitterTab)} className="cursor-pointer text-accent underline decoration-dotted underline-offset-2 hover:decoration-solid">{job.submitter.session}</button>
              : job.submitter.session}
        </Row>
        <Row label={t('watch.jobs.detail.host')} testId="job-host">
          {host === '' ? '—' : host}{job.claim ? ` · ${t('watch.jobs.detail.attempt', { n: job.claim.attempt })}` : ''}
        </Row>
        <Row label={t('watch.jobs.detail.queued')} testId="job-queued-at">{whenText(job.created_at, now)}</Row>
        <Row label={t('watch.jobs.detail.ran')} testId="job-ran">{ranFor}</Row>
        {job.labels.length > 0 && <Row label={t('watch.jobs.detail.labels')} testId="job-labels">{job.labels.join(' ')}</Row>}
        {r?.cache_hit && <Row label={t('watch.jobs.detail.cache')} testId="job-cache">{t('watch.jobs.detail.cache_hit')}</Row>}
      </dl>
    </section>
  )
}
