// spa/src/components/watch/jobs/JobDetail.test.tsx — the detail of one job: the summary first (result line, failures, tail), then the log (inline, on demand over 1 MiB, or the reason there is none), then who and where. The daemon reads are mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, waitFor, within, act } from '@testing-library/react'

vi.mock('../../../lib/pool/jobs-api', () => ({ fetchJob: vi.fn(), fetchJobLog: vi.fn() }))
vi.mock('../../../lib/pool/jobs-driver-live', () => ({ liveTopology: vi.fn() }))
vi.mock('../../../lib/open-session-tab', () => ({ activateTabPane: vi.fn() }))
// a busy daemon (1–2 s) or a failing host (2 s, 4 s) is asked again after a wait; the tests do not wait
vi.mock('../../../lib/pool/errors', async (orig) => ({ ...(await orig<typeof import('../../../lib/pool/errors')>()), busyDelayMs: () => 0, backoffMs: () => 0 }))

import { JobDetail } from './JobDetail'
import { fetchJob, fetchJobLog } from '../../../lib/pool/jobs-api'
import { liveTopology } from '../../../lib/pool/jobs-driver-live'
import { activateTabPane } from '../../../lib/open-session-tab'
import { useTabStore } from '../../../stores/useTabStore'
import { usePoolStore } from '../../../lib/pool/store'
import { PoolReadError } from '../../../lib/pool/api'
import { clearJobsViewMemory, readLogScroll, writeLogScroll } from '../../../lib/pool/jobs-view-memory'
import type { JobResult, PoolJob } from '../../../lib/pool/jobs-types'
import { useI18nStore } from '../../../stores/useI18nStore'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const NOW = new Date(2026, 9, 20, 12, 0).getTime()
const at = (h: number, m = 0, s = 0) => new Date(2026, 9, 20, h, m, s).getTime()

const result = (over: Partial<JobResult> = {}): JobResult => (
  { exit: 1, summary: null, log: 'j1/log', host: 'air26', started_at: at(10, 0), finished_at: at(10, 3, 20), cache_hit: false, reason: '', superseded_by: null, ...over })
const job = (over: Partial<PoolJob> = {}): PoolJob => ({
  id: 'j1', origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c0ffee1234567', tree: '', base: '', rel_cwd: '' }, task: 'spa-related', args: ['src/a.ts'], class: 'merge',
  labels: ['os:darwin', 'repo:purdex'], locks: [], cacheable: false, timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false,
  submitter: { session: 'sess-1', ref: '_abc123', team: 'wwd', host: 'mlab' }, created_at: at(9, 58), state: 'failed', claim: { worker: 'air26', attempt: 2, since: at(10, 0) },
  result: result(), manual: '', manual_seq: 0, ...over,
})
const ready = (j: PoolJob, log: string | null) => vi.mocked(fetchJob).mockResolvedValue({ job: j, log })
const mount = (j: PoolJob) => render(<JobDetail job={j} now={NOW} onClose={() => {}} />)
const gone = () => new PoolReadError('/api/pool/jobs/j1', 404, { code: 'not_found', action: 'none' })

beforeEach(() => {
  cleanup(); clearJobsViewMemory()
  useTabStore.setState({ tabs: {} } as never); vi.mocked(activateTabPane).mockClear()
  vi.mocked(fetchJob).mockReset(); vi.mocked(fetchJobLog).mockReset()
  vi.mocked(liveTopology).mockReturnValue({ origins: ['mlab', 'air26'], ownDaemon: { mlab: 'd-mlab' }, forwarders: ['d-mlab'] })
})
afterEach(() => { cleanup(); clearJobsViewMemory() })

describe('the summary comes first', () => {
  const failing = job({ result: result({ summary: { passed: 4, failed: 1, skipped: 0, failures: [{ name: 'a.test', file: 'src/a.test.ts', message: 'expected 1' }], tail: ['tail line 1', 'tail line 2'] } }) })

  it('the result line and the failures and the tail are above the log, and the log above the facts', async () => {
    ready(failing, 'the whole log')
    mount(failing)
    expect(await screen.findByTestId('job-log')).toHaveTextContent('the whole log')
    expect(screen.getByTestId('job-detail-state')).toHaveTextContent(t('pool.job.result.failed', { n: 1 }))
    expect(screen.getByTestId('job-failures')).toHaveTextContent('a.test')
    expect(screen.getByTestId('job-failures')).toHaveTextContent('expected 1')
    expect(screen.getByTestId('job-tail')).toHaveTextContent('tail line 1')
    const order = (a: HTMLElement, b: HTMLElement) => Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)
    expect(order(screen.getByTestId('job-failures'), screen.getByTestId('job-tail'))).toBe(true)
    expect(order(screen.getByTestId('job-tail'), screen.getByTestId('job-log'))).toBe(true)
    expect(order(screen.getByTestId('job-log'), screen.getByTestId('job-id'))).toBe(true)
  })

  it('a job without failures shows no failures block', async () => {
    const ok = job({ state: 'succeeded', result: result({ exit: 0, summary: { passed: 9, failed: 0, skipped: 0, failures: [], tail: [] } }) })
    ready(ok, 'x')
    mount(ok)
    await screen.findByTestId('job-log')
    expect(screen.queryByTestId('job-failures')).toBeNull()
    expect(screen.queryByTestId('job-tail')).toBeNull()
  })
})

describe('the log', () => {
  it('the detail\'s own log is shown without another read', async () => {
    const j = job()
    ready(j, 'inline log')
    mount(j)
    expect(await screen.findByTestId('job-log')).toHaveTextContent('inline log')
    expect(fetchJobLog).not.toHaveBeenCalled()
    expect(screen.queryByTestId('job-log-load')).toBeNull()
  })

  it('a stored log the detail did not carry (over 1 MiB) is read from /log only when asked', async () => {
    const j = job()
    ready(j, null)
    vi.mocked(fetchJobLog).mockResolvedValue('big log')
    mount(j)
    const load = await screen.findByTestId('job-log-load')
    expect(fetchJobLog).not.toHaveBeenCalled()
    fireEvent.click(load)
    expect(await screen.findByTestId('job-log')).toHaveTextContent('big log')
    expect(fetchJobLog).toHaveBeenCalledWith('d-mlab', 'j1', undefined)
    expect(screen.queryByTestId('job-log-load')).toBeNull()
  })

  it('a log that could not be read says why, and can be asked for again', async () => {
    const j = job()
    ready(j, null)
    vi.mocked(fetchJobLog).mockRejectedValueOnce(new PoolReadError('/log', 404, { code: 'not_found', action: 'none' })).mockResolvedValueOnce('second try')
    mount(j)
    fireEvent.click(await screen.findByTestId('job-log-load'))
    expect(await screen.findByTestId('job-log-error')).toHaveTextContent(t('pool.error.not_found'))
    fireEvent.click(screen.getByTestId('job-log-load'))
    expect(await screen.findByTestId('job-log')).toHaveTextContent('second try')
  })

  it('a job still going says the log comes when it ends', async () => {
    const j = job({ state: 'running', result: null })
    ready(j, null)
    mount(j)
    expect(screen.getByTestId('job-log-note')).toHaveTextContent(t('watch.jobs.detail.log_running'))
    expect(screen.queryByTestId('job-log-load')).toBeNull()
  })

  it('no stored log: just ended it is still uploading, long after it is past the keeping time', async () => {
    const fresh = job({ result: result({ log: '', finished_at: NOW - 30_000 }) })
    ready(fresh, null)
    const view = mount(fresh)
    expect(await screen.findByTestId('job-log-note')).toHaveTextContent(t('watch.jobs.detail.log_uploading'))
    const old = job({ result: result({ log: '', finished_at: at(8) }) })
    ready(old, null)
    view.rerender(<JobDetail job={old} now={NOW} onClose={() => {}} />)
    expect(await screen.findByText(t('watch.jobs.detail.log_expired'))).toBeInTheDocument()
  })

  it('the log\'s scroll position is the memory\'s: written on scroll, put back when the panel comes again', async () => {
    const j = job()
    ready(j, 'a\nb\nc')
    const first = mount(j)
    const box = await screen.findByTestId('job-log')
    box.scrollTop = 40
    fireEvent.scroll(box)
    expect(readLogScroll('mlab/j1')).toBe(40)
    first.unmount()
    mount(j)
    expect((await screen.findByTestId('job-log')).scrollTop).toBe(40)
  })

  it('another job\'s log starts at the top', async () => {
    writeLogScroll('mlab/other', 99)
    const j = job()
    ready(j, 'text')
    mount(j)
    expect((await screen.findByTestId('job-log')).scrollTop).toBe(0)
  })
})

describe('who and where', () => {
  it('id, repo at the commit, class, origin, submitter, host and attempt, times, labels', async () => {
    const j = job()
    ready(j, 'x')
    mount(j)
    await screen.findByTestId('job-log')
    expect(screen.getByTestId('job-id')).toHaveTextContent('j1')
    expect(screen.getByTestId('job-repo')).toHaveTextContent('purdex@c0ffee12')
    expect(screen.getByTestId('job-class-row')).toHaveTextContent(t('watch.jobs.class.merge'))
    expect(screen.getByTestId('job-origin')).toHaveTextContent('mlab')
    expect(screen.getByTestId('job-submitter')).toHaveTextContent('sess-1')
    expect(screen.getByTestId('job-host')).toHaveTextContent(`air26 · ${t('watch.jobs.detail.attempt', { n: 2 })}`)
    expect(screen.getByTestId('job-queued-at')).toHaveTextContent('09:58')
    expect(screen.getByTestId('job-ran')).toHaveTextContent('3:20')
    expect(screen.getByTestId('job-labels')).toHaveTextContent('os:darwin repo:purdex')
    expect(screen.queryByTestId('job-cache')).toBeNull()
  })

  it('a cache hit is said', async () => {
    const j = job({ state: 'succeeded', result: result({ exit: 0, cache_hit: true }) })
    ready(j, 'x')
    mount(j)
    expect(await screen.findByTestId('job-cache')).toHaveTextContent(t('watch.jobs.detail.cache_hit'))
  })

  const heldBy = (sessionId: string) => useTabStore.setState({
    tabs: { t9: { id: 't9', layout: { type: 'leaf', pane: { id: 'p9', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'a', mode: 'terminal', cachedName: '', tmuxInstance: '',
      rebuild: { sessionName: 'a', tmuxInstance: '', agent: { type: 'cc', sessionId, updatedAt: 1 }, capturedAt: 1 } } } } } },
  } as never)

  it('a submitter session some tab holds is a link to that tab (the case of the id ignored); a press shows the tab and its pane', async () => {
    heldBy('SESS-1')
    const j = job() // session 'sess-1'
    ready(j, 'x')
    mount(j)
    await screen.findByTestId('job-log')
    const link = screen.getByTestId('job-submitter-link')
    expect(link).toHaveTextContent('sess-1')
    fireEvent.click(link)
    expect(activateTabPane).toHaveBeenCalledWith('t9', 'p9')
  })

  it('a session no tab holds is plain text, nothing to press', async () => {
    heldBy('another-session')
    const j = job()
    ready(j, 'x')
    mount(j)
    await screen.findByTestId('job-log')
    expect(screen.getByTestId('job-submitter')).toHaveTextContent('sess-1')
    expect(screen.queryByTestId('job-submitter-link')).toBeNull()
  })

  it('an empty submitter session is 「—」 with nothing to press', async () => {
    heldBy('sess-1')
    const j = job({ submitter: { session: '', ref: '', team: '', host: '' } })
    ready(j, 'x')
    mount(j)
    await screen.findByTestId('job-log')
    const row = screen.getByTestId('job-submitter')
    expect(row).toHaveTextContent('—')
    expect(within(row).queryByRole('button')).toBeNull()
    expect(within(row).queryByRole('link')).toBeNull()
  })

  it('a job that never ran has no host and no run time', async () => {
    const j = job({ state: 'superseded', claim: null, result: null })
    ready(j, null)
    mount(j)
    expect(screen.getByTestId('job-host')).toHaveTextContent('—')
    expect(screen.getByTestId('job-ran')).toHaveTextContent('—')
  })
})

describe('the read', () => {
  it('the origin\'s own daemon is asked for itself; any other origin goes through the first daemon with its name', async () => {
    const own = job()
    ready(own, 'x')
    mount(own)
    await screen.findByTestId('job-log')
    expect(fetchJob).toHaveBeenLastCalledWith('d-mlab', 'j1', undefined)
    cleanup()
    const peer = job({ origin: 'mserv' })
    ready(peer, 'x')
    mount(peer)
    await screen.findByTestId('job-log')
    expect(fetchJob).toHaveBeenLastCalledWith('d-mlab', 'j1', 'mserv')
  })

  it('a job the daemon no longer has says so; the facts the App holds stay', async () => {
    vi.mocked(fetchJob).mockRejectedValue(gone())
    mount(job())
    expect(await screen.findByTestId('job-detail-error')).toHaveTextContent(t('pool.error.not_found'))
    expect(screen.getByTestId('job-id')).toHaveTextContent('j1')
  })

  it('no daemon to ask says so, and asks nothing', async () => {
    vi.mocked(liveTopology).mockReturnValue({ origins: ['mlab'], ownDaemon: {}, forwarders: [] })
    mount(job())
    expect(await screen.findByTestId('job-detail-error')).toBeInTheDocument()
    expect(fetchJob).not.toHaveBeenCalled()
  })

  const failing = (code: 'no_answer' | 'busy' | 'wwd_unavailable' | 'not_found' | 'wwd_error', action: 'next_source' | 'retry_same' | 'backoff' | 'none') => new PoolReadError('/api/pool/jobs/j1', code === 'no_answer' ? null : 503, { code, action })

  it('a source that does not answer is skipped: the next daemon that can forward is asked, in order', async () => {
    vi.mocked(liveTopology).mockReturnValue({ origins: ['mserv'], ownDaemon: {}, forwarders: ['d-1', 'd-2'] })
    const j = job({ origin: 'mserv' })
    vi.mocked(fetchJob).mockRejectedValueOnce(failing('no_answer', 'next_source')).mockResolvedValueOnce({ job: j, log: 'from the second' })
    mount(j)
    expect(await screen.findByTestId('job-log')).toHaveTextContent('from the second')
    expect(vi.mocked(fetchJob).mock.calls.map((c) => c[0])).toEqual(['d-1', 'd-2'])
    expect(vi.mocked(fetchJob).mock.calls[1][2]).toBe('mserv')
  })

  it('wwd not running on the first daemon (wwd_unavailable) is the same: the next one is asked; the last failure is the one shown when none answers', async () => {
    vi.mocked(liveTopology).mockReturnValue({ origins: ['mserv'], ownDaemon: {}, forwarders: ['d-1', 'd-2'] })
    vi.mocked(fetchJob).mockRejectedValue(failing('wwd_unavailable', 'next_source'))
    mount(job({ origin: 'mserv' }))
    expect(await screen.findByTestId('job-detail-error')).toHaveTextContent(t('watch.jobs.detail.read_unavailable'))
    expect(fetchJob).toHaveBeenCalledTimes(2)
  })

  it('a busy daemon is asked again (the same one), and the answer is then shown', async () => {
    const j = job()
    vi.mocked(fetchJob).mockRejectedValueOnce(failing('busy', 'retry_same')).mockResolvedValueOnce({ job: j, log: 'after busy' })
    mount(j)
    expect(await screen.findByTestId('job-log')).toHaveTextContent('after busy')
    expect(vi.mocked(fetchJob).mock.calls.map((c) => c[0])).toEqual(['d-mlab', 'd-mlab'])
  })

  it('a daemon that stays busy is asked three times in all, then the failure is shown', async () => {
    vi.mocked(fetchJob).mockRejectedValue(failing('busy', 'retry_same'))
    mount(job())
    expect(await screen.findByTestId('job-detail-error')).toHaveTextContent(t('pool.error.busy'))
    expect(fetchJob).toHaveBeenCalledTimes(3)
  })

  it('an answer that is not a failure of the source (404: the job is gone) is final: no other daemon is asked', async () => {
    vi.mocked(liveTopology).mockReturnValue({ origins: ['mserv'], ownDaemon: {}, forwarders: ['d-1', 'd-2'] })
    vi.mocked(fetchJob).mockRejectedValue(failing('not_found', 'none'))
    mount(job({ origin: 'mserv' }))
    expect(await screen.findByTestId('job-detail-error')).toHaveTextContent(t('pool.error.not_found'))
    expect(fetchJob).toHaveBeenCalledTimes(1)
  })

  it('the full log fails over the same way', async () => {
    vi.mocked(liveTopology).mockReturnValue({ origins: ['mserv'], ownDaemon: {}, forwarders: ['d-1', 'd-2'] })
    const j = job({ origin: 'mserv' })
    ready(j, null)
    vi.mocked(fetchJobLog).mockRejectedValueOnce(failing('no_answer', 'next_source')).mockResolvedValueOnce('big, from the second')
    mount(j)
    fireEvent.click(await screen.findByTestId('job-log-load'))
    expect(await screen.findByTestId('job-log')).toHaveTextContent('big, from the second')
    expect(vi.mocked(fetchJobLog).mock.calls.map((c) => c[0])).toEqual(['d-1', 'd-2'])
  })

  it('a host that fails for the moment (wwd_error, the table\'s back-off) is asked again, the same daemon, and the answer is then shown; one that keeps failing shows the failure after three asks', async () => {
    const j = job()
    vi.mocked(fetchJob).mockRejectedValueOnce(failing('wwd_error', 'backoff')).mockResolvedValueOnce({ job: j, log: 'recovered' })
    mount(j)
    expect(await screen.findByTestId('job-log')).toHaveTextContent('recovered')
    expect(vi.mocked(fetchJob).mock.calls.map((c) => c[0])).toEqual(['d-mlab', 'd-mlab'])
    cleanup()
    vi.mocked(fetchJob).mockReset(); vi.mocked(fetchJob).mockRejectedValue(failing('wwd_error', 'backoff'))
    mount(j)
    expect(await screen.findByTestId('job-detail-error')).toHaveTextContent('mlab')
    expect(fetchJob).toHaveBeenCalledTimes(3)
  })

  it('a daemon that arrives behind the first one (the first source stays the same) is asked too: the failure that stood is read again', async () => {
    vi.mocked(liveTopology).mockReturnValue({ origins: ['mserv'], ownDaemon: {}, forwarders: ['d-1'] })
    const j = job({ origin: 'mserv' })
    vi.mocked(fetchJob).mockRejectedValue(failing('no_answer', 'next_source'))
    mount(j)
    expect(await screen.findByTestId('job-detail-error')).toBeInTheDocument()
    vi.mocked(fetchJob).mockReset()
    vi.mocked(fetchJob).mockRejectedValueOnce(failing('no_answer', 'next_source')).mockResolvedValueOnce({ job: j, log: 'from the new one' })
    vi.mocked(liveTopology).mockReturnValue({ origins: ['mserv'], ownDaemon: {}, forwarders: ['d-1', 'd-2'] })
    act(() => { usePoolStore.setState({ hosts: [] }) })
    expect(await screen.findByTestId('job-log')).toHaveTextContent('from the new one')
    expect(vi.mocked(fetchJob).mock.calls.map((c) => c[0])).toEqual(['d-1', 'd-2'])
  })

  it('the full log: a request still running when the panel moves to another job does not answer for it', async () => {
    const a = job({ id: 'a' })
    const b = job({ id: 'b' })
    vi.mocked(fetchJob).mockImplementation(async (_d, id) => ({ job: id === 'a' ? a : b, log: null }))
    let resolveA: (t: string) => void = () => {}
    vi.mocked(fetchJobLog).mockImplementationOnce(() => new Promise<string>((r) => { resolveA = r }))
    const view = render(<JobDetail job={a} now={NOW} onClose={() => {}} />)
    fireEvent.click(await screen.findByTestId('job-log-load'))
    view.rerender(<JobDetail job={b} now={NOW} onClose={() => {}} />)
    expect(await screen.findByTestId('job-log-load')).not.toBeDisabled()
    // the new job's own request answers first; the old one answers after it, and must not take its place
    vi.mocked(fetchJobLog).mockResolvedValueOnce('B\'s big log')
    fireEvent.click(screen.getByTestId('job-log-load'))
    expect(await screen.findByTestId('job-log')).toHaveTextContent('B\'s big log')
    await act(async () => { resolveA('A\'s big log') })
    expect(screen.getByTestId('job-log')).toHaveTextContent('B\'s big log')
    expect(screen.queryByTestId('job-log-load')).toBeNull()
  })

  it('the full log: closing the panel while a busy daemon is being waited on stops the asking', async () => {
    const j = job()
    ready(j, null)
    vi.mocked(fetchJobLog).mockRejectedValue(failing('busy', 'retry_same'))
    const view = mount(j)
    fireEvent.click(await screen.findByTestId('job-log-load'))
    view.unmount()
    await new Promise((r) => setTimeout(r, 20))
    expect(fetchJobLog).toHaveBeenCalledTimes(1)
  })

  it('a daemon that arrives while the panel is open is asked: a read that had none does not stay failed', async () => {
    vi.mocked(liveTopology).mockReturnValue({ origins: ['mlab'], ownDaemon: {}, forwarders: [] })
    const j = job()
    ready(j, 'late log')
    mount(j)
    expect(await screen.findByTestId('job-detail-error')).toBeInTheDocument()
    vi.mocked(liveTopology).mockReturnValue({ origins: ['mlab'], ownDaemon: { mlab: 'd-mlab' }, forwarders: ['d-mlab'] })
    act(() => { usePoolStore.setState({ hosts: [] }) }) // the topology's inputs moved
    expect(await screen.findByTestId('job-log')).toHaveTextContent('late log')
    expect(screen.queryByTestId('job-detail-error')).toBeNull()
  })

  it('a change of the stores that leaves the source as it was reads nothing again', async () => {
    const j = job()
    ready(j, 'x')
    mount(j)
    await screen.findByTestId('job-log')
    expect(fetchJob).toHaveBeenCalledTimes(1)
    act(() => { usePoolStore.setState({ hosts: [] }) })
    expect(fetchJob).toHaveBeenCalledTimes(1)
  })

  it('is read again when the job changes (it ended, its log was stored) and not again when nothing changed', async () => {
    const running = job({ state: 'running', result: null })
    ready(running, null)
    const view = mount(running)
    await waitFor(() => expect(fetchJob).toHaveBeenCalledTimes(1))
    view.rerender(<JobDetail job={{ ...running }} now={NOW + 1000} onClose={() => {}} />)
    expect(fetchJob).toHaveBeenCalledTimes(1)
    const done = job()
    ready(done, 'finished log')
    view.rerender(<JobDetail job={done} now={NOW + 2000} onClose={() => {}} />)
    expect(await screen.findByTestId('job-log')).toHaveTextContent('finished log')
    expect(fetchJob).toHaveBeenCalledTimes(2)
  })

  it('the close button lets go of the selection', () => {
    const onClose = vi.fn()
    ready(job(), 'x')
    render(<JobDetail job={job()} now={NOW} onClose={onClose} />)
    fireEvent.click(screen.getByRole('button', { name: t('watch.jobs.detail.close') }))
    expect(onClose).toHaveBeenCalledOnce()
  })
})
