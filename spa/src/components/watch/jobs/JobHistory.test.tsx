// spa/src/components/watch/jobs/JobHistory.test.tsx — 歷史: the ended jobs as a table (newest first), filtered by state group, the host a job ran on and its repo; a row can be selected. The filters, the selection and the scroll are the view memory's.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react'
import { JobHistory } from './JobHistory'
import { clearJobsViewMemory, readHistoryFilter, readSelectedJob, readHistoryScroll, writeHistoryFilter } from '../../../lib/pool/jobs-view-memory'
import type { PoolJob } from '../../../lib/pool/jobs-types'
import { useI18nStore } from '../../../stores/useI18nStore'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const NOW = new Date(2026, 9, 20, 12, 0).getTime()
const at = (h: number, m = 0) => new Date(2026, 9, 20, h, m).getTime()

const job = (id: string, over: Partial<PoolJob> = {}): PoolJob => ({
  id, origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: 'test', args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: '', ref: '', team: '', host: '' }, created_at: at(8), state: 'succeeded',
  claim: null, result: null, manual: '', manual_seq: 0, ...over,
})
const result = (over: Partial<NonNullable<PoolJob['result']>> = {}): NonNullable<PoolJob['result']> => (
  { exit: 0, summary: null, log: '', host: 'mserv', started_at: at(9), finished_at: at(9, 3), cache_hit: false, reason: '', superseded_by: null, ...over })
const ids = () => screen.getAllByTestId('job-history-row').map((r) => r.getAttribute('data-job-id'))

const JOBS = [
  job('ok', { result: result({ finished_at: at(11) }) }),
  job('bad', { state: 'failed', result: result({ exit: 1, summary: { passed: 4, failed: 2, skipped: 0, failures: [], tail: [] }, host: 'air26', started_at: at(9, 57), finished_at: at(10) }), repo: { name: 'mlab', root: '', remote: '' } }),
  job('cx', { state: 'cancelled', result: result({ exit: null, finished_at: at(9) }) }),
  job('sup', { state: 'superseded', origin: 'air26', result: null, created_at: at(7) }),
  job('late', { state: 'expired', result: result({ exit: null, reason: 'queue_timeout', host: '', finished_at: at(6) }), repo: { name: 'aigora', root: '', remote: '' } }),
  job('run', { state: 'running', claim: { worker: 'mlab', attempt: 1, since: at(11) } }),
]

beforeEach(() => { cleanup(); clearJobsViewMemory() })
afterEach(() => { cleanup(); clearJobsViewMemory() })

describe('the table', () => {
  it('the ended jobs, newest first; a job that is still going is not in it', () => {
    render(<JobHistory jobs={JOBS} now={NOW} />)
    expect(ids()).toEqual(['ok', 'bad', 'cx', 'sup', 'late'])
  })

  it('a row says the state, the job, the host it ran on, when it ended, how long it took and the result in words', () => {
    render(<JobHistory jobs={JOBS} now={NOW} />)
    const bad = screen.getAllByTestId('job-history-row').find((r) => r.getAttribute('data-job-id') === 'bad')!
    expect(bad).toHaveAttribute('data-state', 'failed')
    expect(within(bad).getByTestId('job-history-state')).toHaveTextContent(t('watch.jobs.state.failed'))
    expect(bad).toHaveTextContent('mlab test')
    expect(within(bad).getByTestId('job-history-host')).toHaveTextContent('air26')
    expect(within(bad).getByTestId('job-history-when')).toHaveTextContent('10:00')
    expect(within(bad).getByTestId('job-history-took')).toHaveTextContent('3:00')
    expect(within(bad).getByTestId('job-history-result')).toHaveTextContent(t('pool.job.result.failed', { n: 2 }))
  })

  it('a job that never ran has no host and no duration: 「—」', () => {
    render(<JobHistory jobs={JOBS} now={NOW} />)
    const sup = screen.getAllByTestId('job-history-row').find((r) => r.getAttribute('data-job-id') === 'sup')!
    expect(within(sup).getByTestId('job-history-host')).toHaveTextContent('—')
    expect(within(sup).getByTestId('job-history-took')).toHaveTextContent('—')
    expect(within(sup).getByTestId('job-history-result')).toHaveTextContent(t('pool.job.result.superseded'))
  })

  it('says how many there are; with none, that there are none', () => {
    const { rerender } = render(<JobHistory jobs={JOBS} now={NOW} />)
    expect(screen.getByTestId('job-history-count')).toHaveTextContent(t('watch.jobs.history.count', { n: 5 }))
    rerender(<JobHistory jobs={[]} now={NOW} />)
    expect(screen.getByTestId('job-history-none')).toHaveTextContent(t('watch.jobs.history.none'))
  })
})

describe('the filters', () => {
  const pick = (testId: string, name: string) => fireEvent.click(within(screen.getByTestId(testId)).getByRole('button', { name }))

  it('the state groups: 失敗 / 成功 / 取消與取代 / 逾時 / 全部', () => {
    render(<JobHistory jobs={JOBS} now={NOW} />)
    pick('job-filter-group', t('watch.jobs.filter.group.failed'))
    expect(ids()).toEqual(['bad'])
    pick('job-filter-group', t('watch.jobs.filter.group.cancelled'))
    expect(ids()).toEqual(['cx', 'sup'])
    pick('job-filter-group', t('watch.jobs.filter.group.expired'))
    expect(ids()).toEqual(['late'])
    pick('job-filter-group', t('watch.jobs.filter.group.succeeded'))
    expect(ids()).toEqual(['ok'])
    pick('job-filter-group', t('watch.jobs.filter.group.all'))
    expect(ids()).toHaveLength(5)
    expect(within(screen.getByTestId('job-filter-group')).getByRole('button', { name: t('watch.jobs.filter.group.all') })).toHaveAttribute('aria-pressed', 'true')
  })

  it('the host is the one the job ran on (jobs that never ran are in 全部 only); the repo is the repo', () => {
    render(<JobHistory jobs={JOBS} now={NOW} />)
    const host = screen.getByLabelText(t('watch.jobs.filter.host')) as HTMLSelectElement
    expect(Array.from(host.options).map((o) => o.value)).toEqual(['', 'air26', 'mserv'])
    fireEvent.change(host, { target: { value: 'mserv' } })
    expect(ids()).toEqual(['ok', 'cx'])
    fireEvent.change(host, { target: { value: '' } })
    const repo = screen.getByLabelText(t('watch.jobs.filter.repo')) as HTMLSelectElement
    expect(Array.from(repo.options).map((o) => o.value)).toEqual(['', 'aigora', 'mlab', 'purdex'])
    fireEvent.change(repo, { target: { value: 'mlab' } })
    expect(ids()).toEqual(['bad'])
  })

  it('the three together; nothing matching says so, and the filter can be taken off', () => {
    render(<JobHistory jobs={JOBS} now={NOW} />)
    pick('job-filter-group', t('watch.jobs.filter.group.failed'))
    fireEvent.change(screen.getByLabelText(t('watch.jobs.filter.host')), { target: { value: 'mserv' } })
    expect(screen.queryAllByTestId('job-history-row')).toHaveLength(0)
    expect(screen.getByTestId('job-history-none')).toHaveTextContent(t('watch.jobs.history.none_filtered'))
    pick('job-filter-group', t('watch.jobs.filter.group.all'))
    expect(ids()).toEqual(['ok', 'cx'])
  })

  it('a choice goes to the view memory at once', () => {
    render(<JobHistory jobs={JOBS} now={NOW} />)
    pick('job-filter-group', t('watch.jobs.filter.group.failed'))
    fireEvent.change(screen.getByLabelText(t('watch.jobs.filter.repo')), { target: { value: 'mlab' } })
    expect(readHistoryFilter()).toEqual({ group: 'failed', host: '', repo: 'mlab' })
  })

  it('a remembered filter is applied when the table comes back; one whose host has gone from the jobs is still shown as chosen, with an empty table', () => {
    writeHistoryFilter({ group: 'all', host: 'gone-host', repo: '' })
    render(<JobHistory jobs={JOBS} now={NOW} />)
    const host = screen.getByLabelText(t('watch.jobs.filter.host')) as HTMLSelectElement
    expect(host.value).toBe('gone-host')
    expect(screen.queryAllByTestId('job-history-row')).toHaveLength(0)
    fireEvent.change(host, { target: { value: '' } })
    expect(ids()).toHaveLength(5)
  })
})

describe('the selection', () => {
  it('a click selects a row (and goes to the view memory); a click on the selected one lets go; only one is selected', () => {
    render(<JobHistory jobs={JOBS} now={NOW} />)
    const row = (id: string) => screen.getAllByTestId('job-history-row').find((r) => r.getAttribute('data-job-id') === id)!
    fireEvent.click(row('bad'))
    expect(row('bad')).toHaveAttribute('aria-selected', 'true')
    expect(readSelectedJob()).toBe('mlab/bad')
    fireEvent.click(row('ok'))
    expect(row('bad')).toHaveAttribute('aria-selected', 'false')
    expect(row('ok')).toHaveAttribute('aria-selected', 'true')
    fireEvent.click(row('ok'))
    expect(row('ok')).toHaveAttribute('aria-selected', 'false')
    expect(readSelectedJob()).toBeNull()
  })

  it('the same id from two origins are two jobs', () => {
    const twins = [job('same', { origin: 'mlab', result: result() }), job('same', { origin: 'air26', result: result({ finished_at: at(8) }) })]
    render(<JobHistory jobs={twins} now={NOW} />)
    fireEvent.click(screen.getAllByTestId('job-history-row')[1])
    expect(readSelectedJob()).toBe('air26/same')
    expect(screen.getAllByTestId('job-history-row').map((r) => r.getAttribute('aria-selected'))).toEqual(['false', 'true'])
  })

  it('a remembered selection is shown when the table comes back', () => {
    const { unmount } = render(<JobHistory jobs={JOBS} now={NOW} />)
    fireEvent.click(screen.getAllByTestId('job-history-row')[2])
    unmount()
    render(<JobHistory jobs={JOBS} now={NOW} />)
    expect(screen.getAllByTestId('job-history-row')[2]).toHaveAttribute('aria-selected', 'true')
  })
})

describe('the scroll', () => {
  it('the scroll position goes to the view memory and is put back when the table comes back', () => {
    const first = render(<JobHistory jobs={JOBS} now={NOW} />)
    const box = screen.getByTestId('job-history-scroll')
    box.scrollTop = 80
    fireEvent.scroll(box)
    expect(readHistoryScroll()).toBe(80)
    first.unmount()
    render(<JobHistory jobs={JOBS} now={NOW} />)
    expect(screen.getByTestId('job-history-scroll').scrollTop).toBe(80)
  })
})
