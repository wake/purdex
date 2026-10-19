// spa/src/components/watch/jobs/JobHistory.tsx — 歷史 of the jobs tab (plan "WWD-7 design", Jobs tab): the newest ended jobs of every origin as a table — the state, the job, the host it ran on, when it ended, how long it took, and the result in words — under
// three filters: the state group (全部／失敗／成功／取消與取代／逾時), the host a job ran on and its repo. A row can be selected (7b1-3 opens its detail).
//
// Tab-hosted checklist: the pane unmounts when another tab is shown (keepAliveCount 0), so the filters, the selected job and the scroll position live in `lib/pool/jobs-view-memory`, not in component state.
import { useCallback, useMemo, useRef, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { mss } from '../../../lib/watch/queue-time'
import { STATE_GROUPS, historyHosts, historyOf, historyRepos, jobRanOn, resultText, resultWord, whenText } from '../../../lib/pool/jobs-view'
import { jobLine } from '../../../lib/pool/jobs-lanes'
import {
  jobKey, readHistoryFilter, readHistoryScroll, readSelectedJob, writeHistoryFilter, writeHistoryScroll, writeSelectedJob, type HistoryFilterChoice,
} from '../../../lib/pool/jobs-view-memory'
import type { PoolJob } from '../../../lib/pool/jobs-types'

const KNOWN_STATES = ['succeeded', 'failed', 'cancelled', 'superseded', 'expired']
const CHIP = 'px-2.5 py-0.5 text-xs rounded-full border cursor-pointer transition-colors'
const chipClass = (on: boolean): string => `${CHIP} ${on
  ? 'bg-surface-elevated border-border-active text-text-primary'
  : 'bg-transparent border-border-default text-text-muted hover:text-text-primary hover:border-text-muted'}`
const SELECT = 'rounded border border-border-default bg-surface-secondary px-2 py-0.5 text-xs text-text-primary'
const COLS = 'grid grid-cols-[4.5rem_minmax(0,1fr)_5rem_6.5rem_3.5rem_9rem] items-center gap-2'

/** The options of a select: any, then the choices; a remembered value no job has any more stays, so the reader sees what is chosen. */
function withCurrent(choices: string[], current: string): string[] {
  return current === '' || choices.includes(current) ? choices : [...choices, current]
}

export function JobHistory({ jobs, now }: { jobs: PoolJob[]; now: number }) {
  const t = useI18nStore((s) => s.t)
  // The tab outlives this component: the memory is the source, the state only re-renders.
  const [choice, setChoiceState] = useState<HistoryFilterChoice>(() => readHistoryFilter())
  const [selected, setSelectedState] = useState<string | null>(() => readSelectedJob())
  // The box is only there once there are rows, which may be after the tab was shown (the jobs arrive by polling): the saved position is put back when it first appears, once.
  const restored = useRef(false)
  const boxRef = useCallback((el: HTMLDivElement | null) => {
    if (el && !restored.current) { restored.current = true; el.scrollTop = readHistoryScroll() }
  }, [])
  const setChoice = (next: HistoryFilterChoice) => { writeHistoryFilter(next); setChoiceState(next) }
  const select = (key: string | null) => { writeSelectedJob(key); setSelectedState(key) }

  const rows = useMemo(() => historyOf(jobs, {
    group: choice.group === 'all' ? undefined : choice.group,
    host: choice.host === '' ? undefined : choice.host,
    repo: choice.repo === '' ? undefined : choice.repo,
  }), [jobs, choice.group, choice.host, choice.repo])
  const hosts = useMemo(() => withCurrent(historyHosts(jobs), choice.host), [jobs, choice.host])
  const repos = useMemo(() => withCurrent(historyRepos(jobs), choice.repo), [jobs, choice.repo])
  const filtered = choice.group !== 'all' || choice.host !== '' || choice.repo !== ''

  return (
    <div data-testid="job-history" className="flex flex-1 flex-col gap-2 min-h-0">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5">
        <div data-testid="job-filter-group" role="group" aria-label={t('watch.jobs.filter.group_label')} className="flex flex-wrap items-center gap-1.5">
          {(['all', ...STATE_GROUPS] as const).map((g) => (
            <button key={g} type="button" aria-pressed={choice.group === g} onClick={() => setChoice({ ...choice, group: g })} className={chipClass(choice.group === g)}>
              {t(`watch.jobs.filter.group.${g}`)}
            </button>
          ))}
        </div>
        <label className="flex items-center gap-1.5 text-xs text-text-muted">
          {t('watch.jobs.filter.host')}
          <select value={choice.host} onChange={(e) => setChoice({ ...choice, host: e.target.value })} className={SELECT}>
            <option value="">{t('watch.jobs.filter.any')}</option>
            {hosts.map((h) => <option key={h} value={h}>{h}</option>)}
          </select>
        </label>
        <label className="flex items-center gap-1.5 text-xs text-text-muted">
          {t('watch.jobs.filter.repo')}
          <select value={choice.repo} onChange={(e) => setChoice({ ...choice, repo: e.target.value })} className={SELECT}>
            <option value="">{t('watch.jobs.filter.any')}</option>
            {repos.map((r) => <option key={r} value={r}>{r}</option>)}
          </select>
        </label>
        {rows.length > 0 && <span data-testid="job-history-count" className="text-xs tabular-nums text-text-secondary">{t('watch.jobs.history.count', { n: rows.length })}</span>}
      </div>

      {rows.length === 0
        ? <p data-testid="job-history-none" className="text-xs text-text-muted">{t(filtered ? 'watch.jobs.history.none_filtered' : 'watch.jobs.history.none')}</p>
        : (
          <>
            <div className={`${COLS} px-2 text-[10px] text-text-muted`} aria-hidden="true">
              <span>{t('watch.jobs.history.col.state')}</span><span>{t('watch.jobs.history.col.job')}</span><span>{t('watch.jobs.history.col.host')}</span>
              <span>{t('watch.jobs.history.col.when')}</span><span>{t('watch.jobs.history.col.took')}</span><span>{t('watch.jobs.history.col.result')}</span>
            </div>
            <div ref={boxRef} data-testid="job-history-scroll" onScroll={(e) => writeHistoryScroll(e.currentTarget.scrollTop)} className="min-h-0 overflow-y-auto">
              <ul aria-label={t('watch.jobs.history.title')} className="flex flex-col">
                {rows.map((j) => {
                  const key = jobKey(j.origin, j.id)
                  const on = selected === key
                  const host = jobRanOn(j)
                  const r = j.result
                  return (
                    <li key={key}>
                    <button
                      type="button" data-testid="job-history-row" data-job-id={j.id} data-state={j.state} aria-pressed={on}
                      onClick={() => select(on ? null : key)}
                      className={`${COLS} w-full cursor-pointer border-b border-border-subtle/50 px-2 py-1.5 text-left text-xs ${on ? 'bg-surface-elevated' : 'hover:bg-surface-secondary'}`}
                    >
                      <span data-testid="job-history-state" className="text-text-secondary">{KNOWN_STATES.includes(j.state) ? t(`watch.jobs.state.${j.state}`) : j.state}</span>
                      <span className="min-w-0 truncate font-mono text-text-primary" title={jobLine(j)}>{jobLine(j)}</span>
                      <span data-testid="job-history-host" className="truncate text-text-secondary">{host === '' ? '—' : host}</span>
                      <span data-testid="job-history-when" className="tabular-nums text-text-muted">{whenText(r?.finished_at ?? null, now)}</span>
                      <span data-testid="job-history-took" className="tabular-nums text-text-muted">{r?.started_at != null && r.finished_at != null ? mss(r.started_at, r.finished_at) : '—'}</span>
                      <span data-testid="job-history-result" className="truncate text-text-muted">{resultText(resultWord(j), t)}</span>
                    </button>
                    </li>
                  )
                })}
              </ul>
            </div>
          </>
        )}
    </div>
  )
}
