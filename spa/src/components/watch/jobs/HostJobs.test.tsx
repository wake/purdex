// spa/src/components/watch/jobs/HostJobs.test.tsx — the host tab's 工作 section (plan 7b1-4): on this host now (a claimed one included), queued from here, the last five ended here, and the link to the jobs tab's 歷史 filtered to this host.
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
import { clearJobsViewMemory, readHistoryFilter, readJobsMode } from '../../../lib/pool/jobs-view-memory'
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
      job('run-own', { state: 'running', claim: claim('mlab') }),
      job('claimed-from-air', { origin: 'air26', state: 'claimed', claim: claim('mlab') }),
      job('run-elsewhere', { origin: 'mlab', state: 'running', claim: claim('air26') }),
    )
    mount()
    const block = screen.getByTestId('host-jobs-running')
    expect(within(block).getAllByTestId('job-running').map((e) => e.getAttribute('data-job-id'))).toEqual(['run-own'])
    expect(within(block).getAllByTestId('job-preparing').map((e) => e.getAttribute('data-job-id'))).toEqual(['claimed-from-air'])
    expect(within(block).getByText(t('watch.queue.slots', { used: 2, total: 2 }))).toBeInTheDocument()
  })
})

describe('queued from here', () => {
  it('the queued jobs of this origin, wherever they will run; another host\'s queue and the running ones are not here', () => {
    withJobs(
      job('mine-1'), job('mine-held', { manual: 'hold' }),
      job('theirs', { origin: 'air26' }),
      job('mine-running', { state: 'running', claim: claim('air26') }),
    )
    mount()
    expect(idsIn('host-jobs-queued', 'job-queued').sort()).toEqual(['mine-1', 'mine-held'])
  })

  it('says so when nothing is queued from here', () => {
    withJobs(job('theirs', { origin: 'air26' }))
    mount()
    expect(within(screen.getByTestId('host-jobs-queued')).getByTestId('job-queue-none')).toBeInTheDocument()
  })
})

describe('the last five that ended here', () => {
  it('those that ran on this host, newest first, five at most; the ones that ran elsewhere are not here', () => {
    withJobs(
      ...[1, 2, 3, 4, 5, 6].map((n) => ended(`e${n}`, 'mlab', NOW - (10 - n) * 60_000)),
      ended('ran-on-air', 'air26', NOW - 1000),
      job('running', { state: 'running', claim: claim('mlab') }),
    )
    mount()
    expect(idsIn('host-jobs-ended', 'host-jobs-ended-row')).toEqual(['e6', 'e5', 'e4', 'e3', 'e2'])
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
