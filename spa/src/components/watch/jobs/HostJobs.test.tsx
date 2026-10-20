// spa/src/components/watch/jobs/HostJobs.test.tsx — the monitoring tab's 工作 column (monitoring tabs spec M41-3, plan MON-6e task 2): on this host now (a claimed one included), queued from here (three at most, then 「還有 n 件」),
// the last jobs that ended here (as many as keep the column about as tall as the process tables), and the link to the jobs tab's 歷史 filtered to this host.
// The jobs are the jobs store's and the hosts the pool's; the driver is not started here.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react'

const release = vi.fn()
vi.mock('../../../lib/pool/jobs-driver-live', () => ({ watchJobs: vi.fn(() => release), jobsDaemons: () => [] }))
const openWatchArea = vi.fn()
vi.mock('../../../features/workspace/lib/open-watch-area', () => ({ openWatchArea: (...a: unknown[]) => openWatchArea(...a) }))

import { HostJobs } from './HostJobs'
import { watchJobs } from '../../../lib/pool/jobs-driver-live'
import { useJobsStore } from '../../../lib/pool/jobs-store'
import { usePoolStore } from '../../../lib/pool/store'
import { clearJobsViewMemory, readHistoryFilter, readJobsMode, writeJobsMode } from '../../../lib/pool/jobs-view-memory'
import type { PoolJob } from '../../../lib/pool/jobs-types'
import type { PoolHost } from '../../../lib/pool/types'
import { useI18nStore } from '../../../stores/useI18nStore'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const NOW = new Date(2026, 9, 19, 12, 0).getTime()

const job = (id: string, over: Partial<PoolJob> = {}): PoolJob => ({
  id, origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: `task-${id}`, args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: '', ref: '', team: '', host: '' }, created_at: NOW - 1000, state: 'queued',
  claim: null, result: null, manual: '', manual_seq: 0, ...over,
})
const host = (alias: string, over: Partial<PoolHost> = {}): PoolHost => ({
  host_id: `id-${alias}`, alias, version: '1', proto: 1, platform: 'darwin', arch: 'arm64', facts: null, reachable: true, labels: ['os:darwin'],
  worker: { enabled: true, slots: { heavy: 2, light: 2 }, owner_rule: '' }, ...over,
})
const claim = (worker: string, since = NOW - 65_000) => ({ worker, attempt: 1, since })
const running = (id: string, over: Partial<PoolJob> = {}): PoolJob => job(id, { state: 'running', claim: claim('mlab'), ...over })
const ended = (id: string, worker: string, finishedAt: number, over: Partial<PoolJob> = {}): PoolJob => job(id, {
  state: 'succeeded', claim: claim(worker),
  result: { exit: 0, summary: null, log: '', host: worker, started_at: finishedAt - 5000, finished_at: finishedAt, cache_hit: false, reason: '', superseded_by: null }, ...over,
})
const withJobs = (...jobs: PoolJob[]) => {
  const byOrigin: Record<string, { jobs: Record<string, PoolJob>; cursor: string; epoch: string; via: string }> = {}
  for (const j of jobs) (byOrigin[j.origin] ??= { jobs: {}, cursor: '1', epoch: 'e', via: 'v' }).jobs[j.id] = j
  useJobsStore.setState({ byOrigin })
}
const MLAB = host('mlab')
const mount = () => render(<HostJobs host={MLAB} />)
const idsIn = (testId: string, row: string) => within(screen.getByTestId(testId)).queryAllByTestId(row).map((e) => e.getAttribute('data-job-id'))
const endedIds = () => idsIn('host-jobs-ended', 'host-jobs-ended-row')
/** Ended on this host, newest is the highest number. */
const endedN = (n: number) => Array.from({ length: n }, (_, i) => ended(`e${i + 1}`, 'mlab', NOW - (n - i) * 60_000))

beforeEach(() => {
  cleanup(); clearJobsViewMemory()
  vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] }); vi.setSystemTime(NOW)
  useJobsStore.getState().reset()
  usePoolStore.getState().reset()
  usePoolStore.setState({ hosts: [MLAB, host('air26')] })
  release.mockClear(); openWatchArea.mockClear()
  vi.mocked(watchJobs).mockClear()
})
afterEach(() => { vi.useRealTimers(); cleanup(); clearJobsViewMemory() })

describe('on this host now', () => {
  it('a running job and a claimed one of this worker, whichever host queued them; another worker\'s job is not here', () => {
    withJobs(
      running('run-own'),
      job('claimed-from-air', { origin: 'air26', state: 'claimed', claim: claim('mlab') }),
      running('run-elsewhere', { claim: claim('air26') }),
    )
    mount()
    expect(idsIn('host-jobs-running', 'host-jobs-run-row').sort()).toEqual(['claimed-from-air', 'run-own'])
  })

  it('the section is not there when nothing runs here', () => {
    withJobs(job('q1'))
    mount()
    expect(screen.queryByTestId('host-jobs-running')).toBeNull()
  })

  it('a row says running or preparing, the job, and the lane (重 or 輕; heavy when the wire does not say)', () => {
    withJobs(running('r1', { slot: 'light' }), running('r2', { slot: 'heavy' }), running('r3'), job('c1', { state: 'claimed', claim: claim('mlab') }))
    mount()
    const row = (id: string) => within(screen.getByTestId('host-jobs-running')).getAllByTestId('host-jobs-run-row').find((e) => e.getAttribute('data-job-id') === id)!
    expect(row('r1')).toHaveTextContent(t('watch.host.jobs.state_running'))
    expect(row('r1')).toHaveTextContent('purdex task-r1')
    expect(within(row('r1')).getByTestId('host-jobs-run-slot')).toHaveTextContent(t('watch.jobs.flow.class.light'))
    expect(within(row('r2')).getByTestId('host-jobs-run-slot')).toHaveTextContent(t('watch.jobs.flow.class.heavy'))
    expect(within(row('r3')).getByTestId('host-jobs-run-slot')).toHaveTextContent(t('watch.jobs.flow.class.heavy'))
    expect(row('c1')).toHaveTextContent(t('watch.jobs.preparing'))
  })

  it('the elapsed time runs from started_at, else from the claim; a claimed job has none yet', () => {
    withJobs(running('with-start', { started_at: NOW - 30_000, claim: claim('mlab', NOW - 90_000) }), running('claim-only', { claim: claim('mlab', NOW - 65_000) }), job('c1', { state: 'claimed', claim: claim('mlab', NOW - 5000) }))
    mount()
    const time = (id: string) => within(screen.getByTestId('host-jobs-running')).getAllByTestId('host-jobs-run-row').find((e) => e.getAttribute('data-job-id') === id)!.querySelector('[data-testid="host-jobs-run-time"]')
    expect(time('with-start')).toHaveTextContent('0:30')
    expect(time('claim-only')).toHaveTextContent('1:05')
    expect(time('c1')).toBeNull()
  })

  it('a job another host sent carries that host\'s badge; one sent from this host has none', () => {
    withJobs(running('own'), running('from-air', { origin: 'air26' }))
    mount()
    const rows = within(screen.getByTestId('host-jobs-running')).getAllByTestId('host-jobs-run-row')
    const own = rows.find((e) => e.getAttribute('data-job-id') === 'own')!
    const air = rows.find((e) => e.getAttribute('data-job-id') === 'from-air')!
    expect(within(own).queryByTestId('host-jobs-run-origin')).toBeNull()
    expect(within(air).getByTestId('host-jobs-run-origin')).toHaveAttribute('title', t('watch.jobs.from', { origin: 'air26' }))
  })
})

describe('queued from here', () => {
  it('the queued jobs of this origin, wherever they will run; another host\'s queue and the running ones are not here', () => {
    withJobs(job('mine-1'), job('mine-held', { manual: 'hold' }), job('theirs', { origin: 'air26' }), running('mine-running', { claim: claim('air26') }))
    mount()
    expect(idsIn('host-jobs-queued', 'host-jobs-queue-row').sort()).toEqual(['mine-1', 'mine-held'])
    expect(screen.getByTestId('host-jobs-queued-count')).toHaveTextContent('2')
  })

  it('a row says the class, the job and how long it has waited, yellow from 2 minutes', () => {
    withJobs(job('fresh', { created_at: NOW - 30_000, class: 'merge' }), job('long', { created_at: NOW - 125_000 }))
    mount()
    const rows = within(screen.getByTestId('host-jobs-queued')).getAllByTestId('host-jobs-queue-row')
    const fresh = rows.find((e) => e.getAttribute('data-job-id') === 'fresh')!
    const long = rows.find((e) => e.getAttribute('data-job-id') === 'long')!
    expect(fresh).toHaveTextContent(t('watch.jobs.class.merge'))
    expect(fresh).toHaveTextContent('purdex task-fresh')
    expect(within(fresh).getByTestId('host-jobs-queue-wait')).toHaveTextContent('0:30')
    expect(within(fresh).getByTestId('host-jobs-queue-wait').className).not.toContain('text-status-warning-text')
    expect(within(long).getByTestId('host-jobs-queue-wait')).toHaveTextContent('2:05')
    expect(within(long).getByTestId('host-jobs-queue-wait').className).toContain('text-status-warning-text')
  })

  it('three at most, then 「還有 n 件在排」', () => {
    withJobs(...[1, 2, 3, 4, 5].map((n) => job(`q${n}`, { created_at: NOW - n * 1000 })))
    mount()
    expect(idsIn('host-jobs-queued', 'host-jobs-queue-row')).toHaveLength(3)
    expect(screen.getByTestId('host-jobs-queued-count')).toHaveTextContent('5')
    expect(screen.getByTestId('host-jobs-queued-more')).toHaveTextContent(t('watch.host.jobs.queued_more', { n: 2 }))
  })

  it('exactly three: no 「還有」', () => {
    withJobs(job('q1'), job('q2'), job('q3'))
    mount()
    expect(screen.queryByTestId('host-jobs-queued-more')).toBeNull()
  })

  it('「還有 n 件」 opens the jobs tab on its live view, unfiltered', () => {
    writeJobsMode('history')
    withJobs(...[1, 2, 3, 4].map((n) => job(`q${n}`)))
    mount()
    fireEvent.click(screen.getByTestId('host-jobs-queued-more'))
    expect(readJobsMode()).toBe('live')
    expect(openWatchArea).toHaveBeenCalledWith({ tab: 'jobs' })
  })

  it('says so when nothing is queued from here', () => {
    withJobs(job('theirs', { origin: 'air26' }))
    mount()
    expect(screen.getByTestId('host-jobs-queued-none')).toHaveTextContent(t('watch.jobs.queue.none'))
  })
})

describe('the last jobs that ended here', () => {
  it('those that ran on this host, newest first; the ones that ran elsewhere are not here', () => {
    withJobs(...endedN(4), ended('ran-on-air', 'air26', NOW - 1000), running('now'))
    mount()
    // one running, none queued: 9 − 1 − 0 = 8, held to the most it shows (5); four ended here
    expect(endedIds()).toEqual(['e4', 'e3', 'e2', 'e1'])
  })

  it('how many: 9 minus the running and the queued rows shown (and the 「還有」 row), kept between 3 and 5', () => {
    // nothing running or queued: 9 → 5
    withJobs(...endedN(6)); mount()
    expect(endedIds()).toHaveLength(5)
    cleanup()
    // 2 running, 3 queued: 9 − 2 − 3 = 4
    withJobs(...endedN(6), running('r1'), running('r2'), job('q1'), job('q2'), job('q3')); mount()
    expect(endedIds()).toHaveLength(4)
    cleanup()
    // 3 running, 5 queued (3 shown + 「還有」): 9 − 3 − 3 − 1 = 2 → 3
    withJobs(...endedN(6), running('r1'), running('r2'), running('r3'), ...[1, 2, 3, 4, 5].map((n) => job(`q${n}`))); mount()
    expect(endedIds()).toHaveLength(3)
  })

  it('a row says the state, the job, when it ended and the result in words', () => {
    withJobs(ended('e1', 'mlab', NOW - 60_000, { state: 'failed', result: { exit: 1, summary: { passed: 3, failed: 2, skipped: 0, failures: [], tail: [] }, log: '', host: 'mlab', started_at: NOW - 120_000, finished_at: NOW - 60_000, cache_hit: false, reason: '', superseded_by: null } }))
    mount()
    const row = screen.getByTestId('host-jobs-ended-row')
    expect(row).toHaveTextContent(t('watch.jobs.state.failed'))
    expect(row).toHaveTextContent('purdex task-e1')
    expect(row).toHaveTextContent('11:59')
    expect(row).toHaveTextContent(t('pool.job.result.failed', { n: 2 }))
  })

  it('says so when nothing has ended here', () => {
    withJobs(ended('e1', 'air26', NOW - 1000))
    mount()
    expect(screen.getByTestId('host-jobs-ended-none')).toHaveTextContent(t('watch.host.jobs.ended_none'))
  })
})

describe('a host that left the pool', () => {
  it('its jobs the store still keeps are not shown: not on this host, not ended', () => {
    withJobs(
      running('gone-running', { origin: 'gone' }),
      ended('gone-ended', 'mlab', NOW - 1000, { origin: 'gone' }),
      running('kept-running'),
    )
    mount()
    expect(idsIn('host-jobs-running', 'host-jobs-run-row')).toEqual(['kept-running'])
    expect(endedIds()).toEqual([])
    expect(screen.getByTestId('host-jobs-ended-none')).toBeInTheDocument()
  })
})

describe('the link to the jobs tab', () => {
  it('sets the history filter to this host, the mode to 歷史, then opens the jobs tab', () => {
    mount()
    fireEvent.click(screen.getByTestId('host-jobs-all'))
    expect(readHistoryFilter()).toEqual({ group: 'all', host: 'mlab', repo: '' })
    expect(readJobsMode()).toBe('history')
    expect(openWatchArea).toHaveBeenCalledWith({ tab: 'jobs' })
  })
})

describe('the jobs are kept while it is shown', () => {
  it('watches the jobs while mounted and lets go on unmount', () => {
    const { unmount } = mount()
    expect(watchJobs).toHaveBeenCalledTimes(1)
    unmount()
    expect(release).toHaveBeenCalledTimes(1)
  })
})
