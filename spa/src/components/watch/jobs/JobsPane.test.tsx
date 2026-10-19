// spa/src/components/watch/jobs/JobsPane.test.tsx — the 工作 tab: lanes per pool host, the queue in display order with its red line, and 歷史. The jobs are the jobs store's and the hosts the pool's; the driver and the pool's poll are not started here.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react'

const release = vi.fn()
vi.mock('../../../lib/pool/jobs-driver-live', () => ({ watchJobs: vi.fn(() => release), jobsDaemons: () => [] }))
vi.mock('../../../lib/pool/use-cell-models', () => ({ useRunPool: vi.fn() }))

import { JobsPane } from './JobsPane'
import type { PaneRendererProps } from '../../../lib/module-registry'
import { watchJobs } from '../../../lib/pool/jobs-driver-live'
import { useJobsStore } from '../../../lib/pool/jobs-store'
import { usePoolStore } from '../../../lib/pool/store'
import { clearJobsViewMemory, readJobsMode } from '../../../lib/pool/jobs-view-memory'
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
const host = (alias: string, over: Partial<PoolHost> = {}): PoolHost => ({
  host_id: `id-${alias}`, alias, version: '1', proto: 1, platform: 'darwin', arch: 'arm64', facts: null, reachable: true, labels: ['os:darwin'],
  worker: { enabled: true, slots: { heavy: 2, light: 2 }, owner_rule: '' }, ...over,
})
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

describe('the lanes', () => {
  it('a lane per pool host with its heavy slots: the running job, then the free ones dashed', () => {
    withJobs(job('r1', { state: 'running', claim: claim('mlab') }))
    mount()
    const lane = screen.getByTestId('job-lane-mlab')
    expect(within(lane).getByText(t('watch.queue.slots', { used: 1, total: 2 }))).toBeInTheDocument()
    expect(within(lane).getAllByTestId('job-running')).toHaveLength(1)
    expect(within(lane).getAllByTestId('job-free')).toHaveLength(1)
    expect(within(lane).getByTestId('job-dot')).toBeInTheDocument() // the running light breathes
  })

  it('a claimed job sits in its host\'s lane as a 「準備中」 card — no breathing dot — and is not in the queue', () => {
    withJobs(job('c1', { state: 'claimed', claim: claim('mlab') }))
    mount()
    const card = within(screen.getByTestId('job-lane-mlab')).getByTestId('job-preparing')
    expect(card).toHaveTextContent(t('watch.jobs.preparing'))
    expect(within(card).queryByTestId('job-dot')).toBeNull()
    expect(card.className).toContain('border-dashed')
    expect(screen.queryByTestId('job-queued')).toBeNull()
    expect(screen.getByTestId('job-queue-none')).toBeInTheDocument()
  })

  it('a host whose worker is off is one line: 「不接工作」', () => {
    mount()
    const lane = screen.getByTestId('job-lane-mserv')
    expect(lane).toHaveAttribute('data-off', 'true')
    expect(within(lane).getByTestId('job-lane-off')).toHaveTextContent(t('watch.jobs.lane.off'))
    expect(within(lane).queryByTestId('job-free')).toBeNull()
  })

  it('says why a host takes no more while a slot is free', () => {
    usePoolStore.setState({ hosts: [host('mlab', { headroom_reasons: { heavy: ['owner_active'], light: [] } })] })
    mount()
    expect(within(screen.getByTestId('job-lane-mlab')).getByTestId('job-gate')).toHaveTextContent(t('pool.reason.owner_active'))
  })

  it('a fast-lane job is marked', () => {
    withJobs(job('f', { state: 'running', claim: claim('mlab'), local: true }))
    mount()
    expect(screen.getByTestId('job-fast-lane')).toHaveTextContent(t('watch.jobs.fast_lane'))
  })
})

describe('the queue', () => {
  it('a card says the class, repo task args, where it was sent from, by which session and how long it has waited; an empty session is 「—」', () => {
    withJobs(
      job('q1', { class: 'release', args: ['--all'], created_at: NOW - 65_000, submitter: { session: 'purdex-b0', ref: '', team: '', host: 'mlab' } }),
      job('q2', { origin: 'mserv', class: 'dev' }),
    )
    mount()
    const cards = screen.getAllByTestId('job-queued')
    expect(cards.map((c) => c.getAttribute('data-job-id'))).toEqual(['q1', 'q2'])
    expect(within(cards[0]).getByTestId('job-class')).toHaveTextContent(t('watch.jobs.class.release'))
    expect(cards[0]).toHaveTextContent('purdex test --all')
    expect(cards[0]).toHaveTextContent(t('watch.jobs.from', { origin: 'mlab' }))
    expect(within(cards[0]).getByTestId('job-session')).toHaveTextContent('purdex-b0')
    expect(within(cards[0]).getByTestId('job-wait')).toHaveTextContent('1:05')
    expect(within(cards[1]).getByTestId('job-session')).toHaveTextContent('—')
    expect(screen.getByTestId('jobs-count')).toHaveTextContent(t('watch.jobs.queue.count', { n: 2 }))
  })

  it('a wait past two minutes turns yellow', () => {
    withJobs(job('old', { created_at: NOW - 125_000 }), job('new', { created_at: NOW - 5_000 }))
    mount()
    const [old, fresh] = screen.getAllByTestId('job-wait')
    expect(old.className).toContain('text-status-warning-text')
    expect(fresh.className).not.toContain('text-status-warning-text')
  })

  it('a held job is dimmed and tagged; a pinned one says so', () => {
    withJobs(job('h', { manual: 'hold' }), job('f', { manual: 'front', manual_seq: 1 }))
    mount()
    const cards = screen.getAllByTestId('job-queued')
    expect(cards.map((c) => c.getAttribute('data-job-id'))).toEqual(['f', 'h'])
    expect(cards[1]).toHaveAttribute('data-held', 'true')
    expect(within(cards[1]).getByTestId('job-tag')).toHaveTextContent(t('watch.jobs.tag.hold'))
    expect(within(cards[0]).getByTestId('job-tag')).toHaveTextContent(t('watch.jobs.tag.front'))
  })

  it('the red line: only on a job no host with its worker on can take', () => {
    withJobs(job('php', { labels: ['os:linux'] }), job('ok', { labels: ['os:darwin'] }))
    mount()
    const php = screen.getAllByTestId('job-queued').find((c) => c.getAttribute('data-job-id') === 'php')!
    const ok = screen.getAllByTestId('job-queued').find((c) => c.getAttribute('data-job-id') === 'ok')!
    expect(within(php).getByTestId('job-no-host')).toHaveTextContent(t('watch.jobs.no_host', { labels: 'os:linux' }))
    expect(within(ok).queryByTestId('job-no-host')).toBeNull()
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
    expect(screen.queryByTestId('job-lanes')).toBeNull()
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
    expect(screen.getAllByTestId('job-queued').map((c) => c.getAttribute('data-job-id'))).toEqual(['here'])
  })

  it('no daemon offers the pool any more: the old jobs are not shown, and the pane says so', () => {
    withJobs(job('stale'))
    usePoolStore.setState({ hosts: [], oldDaemon: true })
    mount()
    expect(screen.getByTestId('jobs-no-pool')).toBeInTheDocument()
    expect(screen.queryByTestId('job-queued')).toBeNull()
  })

  it('the pool could not be read for a moment (no hosts, but not an old daemon): what was read stays on the screen', () => {
    withJobs(job('kept'))
    usePoolStore.setState({ hosts: [], oldDaemon: false })
    mount()
    expect(screen.getAllByTestId('job-queued').map((c) => c.getAttribute('data-job-id'))).toEqual(['kept'])
    expect(screen.queryByTestId('jobs-no-pool')).toBeNull()
  })

  it('the pool answered with no host at all (the last one was removed): none of the old jobs is shown, and it is no failure', () => {
    withJobs(job('stale'))
    usePoolStore.setState({ hosts: [], oldDaemon: false, sourceHostId: 'h1' })
    mount()
    expect(screen.queryByTestId('job-queued')).toBeNull()
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
