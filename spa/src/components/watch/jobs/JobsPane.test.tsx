// spa/src/components/watch/jobs/JobsPane.test.tsx — the 任務佇列 tab: the queue flow around the store, the selection and the detail, and 歷史. The jobs are the jobs store's and the hosts the pool's; the driver and the pool's poll are not started here.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react'

const release = vi.fn()
vi.mock('../../../lib/pool/jobs-driver-live', () => ({ watchJobs: vi.fn(() => release), jobsDaemons: () => [], liveTopology: () => ({ origins: ['mlab'], ownDaemon: { mlab: 'd-mlab' }, forwarders: ['d-mlab'] }) }))
vi.mock('../../../lib/pool/jobs-api', () => ({ fetchJob: vi.fn(() => new Promise(() => {})), fetchJobLog: vi.fn() }))
vi.mock('../../../lib/pool/use-cell-models', async (orig) => ({ ...(await orig<typeof import('../../../lib/pool/use-cell-models')>()), useRunPool: vi.fn() }))

import { JobsPane } from './JobsPane'
import { parseFacts } from '../../../lib/pool/parse'
import darwinJson from '../../../lib/pool/__fixtures__/facts_darwin.json'
import type { PaneRendererProps } from '../../../lib/module-registry'
import { watchJobs } from '../../../lib/pool/jobs-driver-live'
import { useJobsStore } from '../../../lib/pool/jobs-store'
import { usePoolStore } from '../../../lib/pool/store'
import { clearJobsViewMemory, readJobsMode, readSelectedJob, writeSelectedJob } from '../../../lib/pool/jobs-view-memory'
import type { PoolJob } from '../../../lib/pool/jobs-types'
import type { PoolHost } from '../../../lib/pool/types'
import { useI18nStore } from '../../../stores/useI18nStore'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const NOW = new Date(2026, 9, 19, 12, 0).getTime()

const job = (id: string, over: Partial<PoolJob> = {}): PoolJob => ({
  id, origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: 'test', args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: '', ref: '', team: '', host: '' }, created_at: NOW - 1000, state: 'queued',
  claim: null, result: null, manual: '', manual_seq: 0, ...over,
})
const factsOf = (alias: string, worker: PoolHost['worker']) => ({ ...parseFacts(darwinJson)!, alias, at: NOW, worker: { ...worker, running: { heavy: 0, light: 0 } }, exceptions: [] })
const host = (alias: string, over: Partial<PoolHost> = {}): PoolHost => {
  const worker = over.worker ?? { enabled: true, slots: { heavy: 2, light: 2 }, owner_rule: '' }
  return { host_id: `id-${alias}`, alias, version: '1', proto: 1, platform: 'darwin', arch: 'arm64', facts: factsOf(alias, worker), reachable: true, labels: ['os:darwin'], worker, ...over }
}
const queued = () => Array.from(document.querySelectorAll('[data-q]')).map((e) => e.getAttribute('data-q'))
const q = (key: string) => document.querySelector(`[data-q="${key}"]`) as HTMLElement
const jcard = (key: string) => document.querySelector(`[data-j="${key}"]`) as HTMLElement
const claim = (worker: string, since = NOW - 65_000) => ({ worker, attempt: 1, since })
const withJobs = (...jobs: PoolJob[]) => {
  const byOrigin: Record<string, { jobs: Record<string, PoolJob>; cursor: string; epoch: string; via: string }> = {}
  for (const j of jobs) (byOrigin[j.origin] ??= { jobs: {}, cursor: '1', epoch: 'e', via: 'v' }).jobs[j.id] = j
  useJobsStore.setState({ byOrigin })
}
const noProps = {} as PaneRendererProps
const mount = () => render(<JobsPane {...noProps} />)

beforeEach(() => {
  cleanup(); clearJobsViewMemory()
  vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] }); vi.setSystemTime(NOW)
  useJobsStore.getState().reset()
  usePoolStore.getState().reset()
  usePoolStore.setState({ hosts: [host('mlab'), host('mserv', { labels: ['os:linux'], worker: { enabled: false, slots: { heavy: 1, light: 1 }, owner_rule: '' } })] })
  release.mockClear()
  vi.mocked(watchJobs).mockClear()
})
afterEach(() => { vi.useRealTimers(); cleanup(); clearJobsViewMemory() })

describe('the live view', () => {
  it('is the queue flow and the last jobs that ended; the lanes and the old queue list are gone', () => {
    withJobs(job('q1'))
    mount()
    expect(screen.getByTestId('queue-flow')).toBeInTheDocument()
    expect(screen.getByTestId('flow-recent')).toBeInTheDocument()
    expect(screen.queryByTestId('job-lanes')).toBeNull()
    expect(screen.queryByTestId('jobs-count')).toBeNull()
    expect(queued()).toEqual(['mlab/q1'])
  })
})

describe('歷史 and the mode', () => {
  it('the toggle shows the ended jobs, newest first, with the result in words, and the choice goes to the view memory', () => {
    withJobs(
      job('a', { state: 'failed', result: { exit: 1, summary: { passed: 3, failed: 2, skipped: 0, failures: [], tail: [] }, log: '', host: 'mlab', started_at: 1, finished_at: NOW - 10_000, cache_hit: false, reason: '', superseded_by: null } }),
      job('b', { state: 'succeeded', result: { exit: 0, summary: null, log: '', host: 'mlab', started_at: 1, finished_at: NOW - 60_000, cache_hit: false, reason: 'oom', superseded_by: null } }),
      job('q'),
    )
    mount()
    fireEvent.click(screen.getByRole('button', { name: t('watch.jobs.mode.history') }))
    expect(readJobsMode()).toBe('history')
    expect(screen.queryByTestId('queue-flow')).toBeNull()
    const rows = screen.getAllByTestId('job-history-row')
    expect(rows.map((r) => r.getAttribute('data-state'))).toEqual(['failed', 'succeeded'])
    expect(rows[0]).toHaveTextContent(t('pool.job.result.failed', { n: 2 }))
    expect(rows[1]).toHaveTextContent(t('pool.job.result.exit', { code: 0 }))
  })

  it('no ended job: it says so', () => {
    mount()
    fireEvent.click(screen.getByRole('button', { name: t('watch.jobs.mode.history') }))
    expect(screen.getByTestId('job-history-none')).toBeInTheDocument()
  })
})

describe('the pane around the store', () => {
  it('the driver watches while it is mounted and lets go when it unmounts', () => {
    const view = mount()
    expect(watchJobs).toHaveBeenCalledTimes(1)
    view.unmount()
    expect(release).toHaveBeenCalledTimes(1)
  })

  it('jobs of an origin that has left the pool are not shown (the store keeps them; the pane shows the pool\'s hosts only)', () => {
    withJobs(job('gone', { origin: 'air19' }), job('here', { origin: 'mlab' }))
    mount()
    expect(queued()).toEqual(['mlab/here'])
  })

  it('no daemon offers the pool any more: the old jobs are not shown, and the pane says so', () => {
    withJobs(job('stale'))
    usePoolStore.setState({ hosts: [], oldDaemon: true })
    mount()
    expect(screen.getByTestId('jobs-no-pool')).toBeInTheDocument()
    expect(queued()).toEqual([])
  })

  it('the pool could not be read for a moment (no hosts, but not an old daemon): the jobs are kept in the store, but no cell draws them (M24) and the pane does not call it a missing pool', () => {
    withJobs(job('kept'))
    usePoolStore.setState({ hosts: [], oldDaemon: false })
    mount()
    expect(queued()).toEqual([])
    expect(screen.getByTestId('queue-flow')).toBeInTheDocument()
    expect(screen.queryByTestId('jobs-no-pool')).toBeNull()
  })

  it('the pool answered with no host at all (the last one was removed): none of the old jobs is shown, and it is no failure', () => {
    withJobs(job('stale'))
    usePoolStore.setState({ hosts: [], oldDaemon: false, sourceHostId: 'h1' })
    mount()
    expect(queued()).toEqual([])
    expect(screen.queryByTestId('jobs-no-pool')).toBeNull()
  })

  it('the error of an origin that has left the pool is not shown; one that is still in it is', () => {
    useJobsStore.setState({ errors: { air19: { code: 'host_unreachable', action: 'backoff' }, mserv: { code: 'host_unreachable', action: 'backoff' } } })
    mount()
    const lines = screen.getAllByTestId('jobs-error')
    expect(lines).toHaveLength(1)
    expect(lines[0]).toHaveTextContent('mserv')
  })

  it('no daemon offers the pool and nothing was read: it says so', () => {
    usePoolStore.setState({ hosts: [], oldDaemon: true })
    mount()
    expect(screen.getByTestId('jobs-no-pool')).toHaveTextContent(t('watch.jobs.no_pool'))
  })

  it('an origin whose read failed says how, in the error table\'s words', () => {
    useJobsStore.setState({ errors: { mserv: { code: 'host_unreachable', action: 'backoff' } } })
    mount()
    expect(screen.getByTestId('jobs-error')).toHaveTextContent(`mserv：${t('pool.error.host_failing')}`)
  })
})

describe('the detail beside the list', () => {
  const finished = (id: string) => job(id, { state: 'failed', result: { exit: 1, summary: null, log: '', host: 'mlab', started_at: NOW - 60_000, finished_at: NOW - 1000, cache_hit: false, reason: '', superseded_by: null } })

  it('a queued card, a running card and a history row each open the job\'s detail; the same press lets go', () => {
    withJobs(job('q1'), job('r1', { state: 'running', claim: claim('mlab') }), finished('h1'))
    mount()
    expect(screen.queryByTestId('job-detail')).toBeNull()
    fireEvent.click(q('mlab/q1'))
    expect(within(screen.getByTestId('job-detail')).getByTestId('job-id')).toHaveTextContent('q1')
    fireEvent.click(jcard('mlab/r1'))
    expect(within(screen.getByTestId('job-detail')).getByTestId('job-id')).toHaveTextContent('r1')
    fireEvent.click(jcard('mlab/r1'))
    expect(screen.queryByTestId('job-detail')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: t('watch.jobs.mode.history') }))
    fireEvent.click(screen.getByTestId('job-history-row'))
    expect(within(screen.getByTestId('job-detail')).getByTestId('job-id')).toHaveTextContent('h1')
  })

  it('the same id from two origins is two cards (an id is only unique within the host that queued it): each opens its own job, and no key is duplicated', () => {
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    usePoolStore.setState({ hosts: [host('mlab'), host('mserv')] })
    withJobs(job('same', { origin: 'mlab' }), job('same', { origin: 'mserv', created_at: NOW - 500 }), job('run', { origin: 'mlab', state: 'running', claim: claim('mlab') }), job('run', { origin: 'mserv', state: 'running', claim: claim('mlab') }))
    mount()
    expect(queued()).toEqual(['mlab/same', 'mserv/same'])
    fireEvent.click(q('mserv/same'))
    expect(within(screen.getByTestId('job-detail')).getByTestId('job-origin')).toHaveTextContent('mserv')
    expect(document.querySelectorAll('[data-j]')).toHaveLength(2)
    fireEvent.click(jcard('mlab/run'))
    expect(within(screen.getByTestId('job-detail')).getByTestId('job-origin')).toHaveTextContent('mlab')
    expect(errors.mock.calls.flat().join(' ')).not.toContain('same key')
    errors.mockRestore()
  })

  it('the close button closes it, and the selection goes with it', () => {
    withJobs(job('q1'))
    mount()
    fireEvent.click(q('mlab/q1'))
    fireEvent.click(screen.getByRole('button', { name: t('watch.jobs.detail.close') }))
    expect(screen.queryByTestId('job-detail')).toBeNull()
    expect(readSelectedJob()).toBeNull()
  })

  it('a selected job the store no longer has opens nothing (and is not an error)', () => {
    withJobs(job('q1'))
    writeSelectedJob('mlab/gone')
    mount()
    expect(screen.queryByTestId('job-detail')).toBeNull()
    expect(screen.queryByTestId('jobs-error')).toBeNull()
  })
})
