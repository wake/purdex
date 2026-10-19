// spa/src/lib/pool/jobs-store.test.ts — the jobs of each origin, kept with the cursor and epoch they were read at (plan "WWD-7 design", Data flow: Jobs). The first read of an origin pages `after=0` until a page is shorter than the limit;
// every later read is `after=<cursor>`; a job that comes again replaces its copy by id; an empty cursor keeps the previous one; `cursor_ahead` or another epoch drops that origin's jobs and reads from 0; the newest 200 ended jobs
// and every job not ended are kept; the daemon that is the origin's own is preferred over forwarding.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import type { JobsPage, PoolJob } from './jobs-types'

const fetchJobsPage = vi.fn()
const fetchJob = vi.fn()
vi.mock('./jobs-api', () => ({ fetchJobsPage: (...a: unknown[]) => fetchJobsPage(...a), fetchJob: (...a: unknown[]) => fetchJob(...a) }))

import { useJobsStore, jobSourceFor, FIRST_PAGE_LIMIT, ENDED_KEPT, MAX_PAGES } from './jobs-store'
import { PoolReadError } from './api'
import { classify } from './errors'

const E1 = '0123456789abcdef0123456789abcdef'
const E2 = 'fedcba9876543210fedcba9876543210'
const job = (id: string, over: Partial<PoolJob> = {}): PoolJob => ({
  id, origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: 't', args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: '', ref: '', team: '', host: '' }, created_at: 1, state: 'queued',
  claim: null, result: null, manual: '', manual_seq: 0, ...over,
})
const page = (jobs: PoolJob[], cursor: string, epoch = E1): JobsPage => ({ jobs, cursor, epoch })
const readErr = (status: number | null, body: unknown = null) => new PoolReadError('/api/pool/jobs', status, classify(status, body))
const src = { daemonId: 'd1' }
const get = () => useJobsStore.getState()
const idsOf = (origin = 'mlab') => Object.keys(get().byOrigin[origin]?.jobs ?? {}).sort()

beforeEach(() => {
  fetchJobsPage.mockReset()
  fetchJob.mockReset()
  useJobsStore.getState().reset()
})

describe('the first read of an origin', () => {
  it('asks after=0 with the page limit, and a short page ends it', async () => {
    fetchJobsPage.mockResolvedValue(page([job('a'), job('b')], '5'))
    expect(await get().read('mlab', src)).toEqual({ ok: true })
    expect(fetchJobsPage).toHaveBeenCalledTimes(1)
    expect(fetchJobsPage).toHaveBeenCalledWith('d1', { host: undefined, after: '0', limit: FIRST_PAGE_LIMIT })
    expect(idsOf()).toEqual(['a', 'b'])
    expect(get().byOrigin.mlab).toMatchObject({ cursor: '5', epoch: E1 })
  })

  it('page after page, each from the cursor the last one gave, until a page is shorter than the limit', async () => {
    const full = Array.from({ length: FIRST_PAGE_LIMIT }, (_, i) => job(`p${i}`))
    fetchJobsPage.mockResolvedValueOnce(page(full, '1000')).mockResolvedValueOnce(page([job('last')], '1001'))
    await get().read('mlab', src)
    expect(fetchJobsPage.mock.calls.map((c) => c[1].after)).toEqual(['0', '1000'])
    expect(idsOf()).toHaveLength(FIRST_PAGE_LIMIT + 1)
    expect(get().byOrigin.mlab.cursor).toBe('1001')
  })

  it('a daemon that always answers a full page with no new cursor does not loop forever', async () => {
    fetchJobsPage.mockResolvedValue(page(Array.from({ length: FIRST_PAGE_LIMIT }, (_, i) => job(`p${i}`)), '7'))
    await get().read('mlab', src)
    expect(fetchJobsPage.mock.calls.length).toBeLessThan(5)
  })

  it('reads another host through the pool daemon with ?host=', async () => {
    fetchJobsPage.mockResolvedValue(page([job('x', { origin: 'air19' })], '1'))
    await get().read('air19', { daemonId: 'd1', host: 'air19' })
    expect(fetchJobsPage).toHaveBeenCalledWith('d1', { host: 'air19', after: '0', limit: FIRST_PAGE_LIMIT })
  })
})

describe('later reads', () => {
  it('ask after=<cursor>; a job that comes again replaces its copy by id; a new one is added', async () => {
    fetchJobsPage.mockResolvedValueOnce(page([job('a'), job('b')], '5'))
    await get().read('mlab', src)
    fetchJobsPage.mockResolvedValueOnce(page([job('a', { state: 'running' }), job('c')], '8'))
    await get().read('mlab', src)
    expect(fetchJobsPage.mock.calls[1][1].after).toBe('5')
    expect(idsOf()).toEqual(['a', 'b', 'c'])
    expect(get().byOrigin.mlab.jobs.a.state).toBe('running')
    expect(get().byOrigin.mlab.cursor).toBe('8')
  })

  it('an answer whose cursor is empty keeps the previous cursor', async () => {
    fetchJobsPage.mockResolvedValueOnce(page([job('a')], '5'))
    await get().read('mlab', src)
    fetchJobsPage.mockResolvedValueOnce(page([], ''))
    await get().read('mlab', src)
    expect(get().byOrigin.mlab.cursor).toBe('5')
    fetchJobsPage.mockResolvedValueOnce(page([], '5'))
    await get().read('mlab', src)
    expect(fetchJobsPage.mock.calls[2][1].after).toBe('5')
  })
})

describe('an origin that restarted or went back', () => {
  async function seeded() {
    fetchJobsPage.mockResolvedValueOnce(page([job('old1'), job('old2')], '9'))
    await get().read('mlab', src)
    fetchJobsPage.mockClear()
  }

  it('cursor_ahead (409) drops that origin\'s jobs and reads from 0', async () => {
    await seeded()
    fetchJobsPage.mockRejectedValueOnce(readErr(409, { reason: 'cursor_ahead', head: '3', epoch: E2 })).mockResolvedValueOnce(page([job('new')], '2', E2))
    expect(await get().read('mlab', src)).toEqual({ ok: true })
    expect(fetchJobsPage.mock.calls.map((c) => c[1].after)).toEqual(['9', '0'])
    expect(idsOf()).toEqual(['new'])
    expect(get().byOrigin.mlab).toMatchObject({ cursor: '2', epoch: E2 })
  })

  it('another epoch on an ordinary page does the same: nothing of the old database is kept', async () => {
    await seeded()
    fetchJobsPage.mockResolvedValueOnce(page([job('other')], '4', E2)).mockResolvedValueOnce(page([job('other'), job('fresh')], '6', E2))
    await get().read('mlab', src)
    expect(fetchJobsPage.mock.calls.map((c) => c[1].after)).toEqual(['9', '0'])
    expect(idsOf()).toEqual(['fresh', 'other'])
    expect(get().byOrigin.mlab.epoch).toBe(E2)
  })

  it('an empty epoch is no epoch: it neither drops nor is compared', async () => {
    fetchJobsPage.mockResolvedValueOnce(page([job('a')], '1', ''))
    await get().read('mlab', src)
    fetchJobsPage.mockResolvedValueOnce(page([job('b')], '2', E1))
    await get().read('mlab', src)
    expect(idsOf()).toEqual(['a', 'b'])
    expect(get().byOrigin.mlab.epoch).toBe(E1)
  })

  it('it drops only that origin: the other origins keep theirs', async () => {
    fetchJobsPage.mockResolvedValueOnce(page([job('m')], '1')).mockResolvedValueOnce(page([job('a', { origin: 'air26' })], '1'))
    await get().read('mlab', src)
    await get().read('air26', src)
    fetchJobsPage.mockRejectedValueOnce(readErr(409, { reason: 'cursor_ahead' })).mockResolvedValueOnce(page([], '0'))
    await get().read('mlab', src)
    expect(idsOf('mlab')).toEqual([])
    expect(idsOf('air26')).toEqual(['a'])
  })

  it('a second 409 in the same read is an error, not a loop', async () => {
    await seeded()
    fetchJobsPage.mockRejectedValue(readErr(409, { reason: 'cursor_ahead' }))
    const r = await get().read('mlab', src)
    expect(r).toMatchObject({ ok: false, error: { code: 'cursor_ahead' } })
    expect(fetchJobsPage.mock.calls.length).toBe(2)
  })
})

describe('what is kept', () => {
  it('every job not ended, and the newest 200 ended ones', async () => {
    const ended = Array.from({ length: ENDED_KEPT + 30 }, (_, i) => job(`e${i}`, { state: 'succeeded', created_at: 1000 + i, result: { exit: 0, summary: null, log: '', host: '', started_at: null, finished_at: 1000 + i, cache_hit: false, reason: '', superseded_by: null } }))
    const live = [job('q', { state: 'queued' }), job('c', { state: 'claimed' }), job('r', { state: 'running', created_at: 1 })]
    fetchJobsPage.mockResolvedValue(page([...ended, ...live], '9'))
    await get().read('mlab', src)
    const ids = idsOf()
    expect(ids).toHaveLength(ENDED_KEPT + 3)
    expect(ids).toEqual(expect.arrayContaining(['q', 'c', 'r']))
    expect(ids).toContain(`e${ENDED_KEPT + 29}`) // the newest ended one
    expect(ids).not.toContain('e0') // the oldest ended one went
    expect(ids).not.toContain('e29')
    expect(ids).toContain('e30')
  })

  it('a job that ended since it was kept as live is now judged as ended', async () => {
    fetchJobsPage.mockResolvedValueOnce(page([job('a', { state: 'running' })], '1'))
    await get().read('mlab', src)
    fetchJobsPage.mockResolvedValueOnce(page([job('a', { state: 'failed' })], '2'))
    await get().read('mlab', src)
    expect(get().byOrigin.mlab.jobs.a.state).toBe('failed')
  })
})

describe('failures and races', () => {
  it('a failed read keeps what was there, says why, and a good one clears it', async () => {
    fetchJobsPage.mockResolvedValueOnce(page([job('a')], '1'))
    await get().read('mlab', src)
    fetchJobsPage.mockRejectedValueOnce(readErr(503, { error: 'wwd_unavailable' }))
    expect(await get().read('mlab', src)).toEqual({ ok: false, error: { code: 'wwd_unavailable', action: 'next_source' } })
    expect(idsOf()).toEqual(['a'])
    expect(get().errors.mlab).toEqual({ code: 'wwd_unavailable', action: 'next_source' })
    fetchJobsPage.mockResolvedValueOnce(page([], '1'))
    await get().read('mlab', src)
    expect(get().errors.mlab).toBeUndefined()
  })

  it('a thrown error that is no PoolReadError is unknown, not a crash', async () => {
    fetchJobsPage.mockRejectedValueOnce(new TypeError('boom'))
    expect(await get().read('mlab', src)).toMatchObject({ ok: false, error: { code: 'unknown' } })
  })

  it('two reads of one origin at once are one read', async () => {
    let release!: (p: JobsPage) => void
    fetchJobsPage.mockReturnValueOnce(new Promise<JobsPage>((r) => { release = r }))
    const a = get().read('mlab', src)
    const b = get().read('mlab', src)
    release(page([job('a')], '1'))
    await Promise.all([a, b])
    expect(fetchJobsPage).toHaveBeenCalledTimes(1)
  })

  it('an origin dropped while its read is out takes nothing from the answer', async () => {
    let release!: (p: JobsPage) => void
    fetchJobsPage.mockReturnValueOnce(new Promise<JobsPage>((r) => { release = r }))
    const pending = get().read('mlab', src)
    get().drop('mlab')
    release(page([job('a')], '1'))
    await pending
    expect(get().byOrigin.mlab).toBeUndefined()
  })
})

describe('reset while the first read of an origin is out', () => {
  it('takes nothing from the answer that comes after, also for an origin nothing had touched yet, for a read and for a job read waiting behind it', async () => {
    let release!: (p: JobsPage) => void
    fetchJobsPage.mockReturnValueOnce(new Promise<JobsPage>((r) => { release = r }))
    fetchJob.mockResolvedValue({ job: job('a') })
    const pending = get().read('never-seen', src)
    const behind = get().readJob('never-seen', src, 'a')
    get().reset()
    release(page([job('a')], '1'))
    await Promise.all([pending, behind])
    expect(get().byOrigin['never-seen']).toBeUndefined()
  })
})

describe('one job again (jobs.changed)', () => {
  it('a good read of it clears the error an earlier failure left, also when the job turns out to be gone', async () => {
    fetchJobsPage.mockResolvedValue(page([job('a')], '1'))
    await get().read('mlab', src)
    fetchJob.mockRejectedValueOnce(readErr(503, { error: 'wwd_unavailable' }))
    await get().readJob('mlab', src, 'a')
    expect(get().errors.mlab).toBeDefined()
    fetchJob.mockResolvedValueOnce({ job: job('a', { state: 'running' }) })
    await get().readJob('mlab', src, 'a')
    expect(get().errors.mlab).toBeUndefined()
    fetchJob.mockRejectedValueOnce(readErr(503, { error: 'wwd_unavailable' }))
    await get().readJob('mlab', src, 'a')
    fetchJob.mockRejectedValueOnce(readErr(404, { error: 'not_found' }))
    await get().readJob('mlab', src, 'zzz')
    expect(get().errors.mlab).toBeUndefined()
  })

  it('replaces that job by id and leaves the cursor and the others alone', async () => {
    fetchJobsPage.mockResolvedValueOnce(page([job('a'), job('b')], '5'))
    await get().read('mlab', src)
    fetchJob.mockResolvedValueOnce({ job: job('a', { state: 'succeeded' }), log: null })
    expect(await get().readJob('mlab', src, 'a')).toEqual({ ok: true })
    expect(fetchJob).toHaveBeenCalledWith('d1', 'a', undefined)
    expect(get().byOrigin.mlab.jobs.a.state).toBe('succeeded')
    expect(idsOf()).toEqual(['a', 'b'])
    expect(get().byOrigin.mlab.cursor).toBe('5')
  })

  it('a job that is gone (404) leaves the list; another failure keeps it and says why', async () => {
    fetchJobsPage.mockResolvedValueOnce(page([job('a'), job('b')], '5'))
    await get().read('mlab', src)
    fetchJob.mockRejectedValueOnce(readErr(404, { error: 'not_found' }))
    expect(await get().readJob('mlab', src, 'a')).toEqual({ ok: true })
    expect(idsOf()).toEqual(['b'])
    fetchJob.mockRejectedValueOnce(readErr(502, { error: 'wwd_error' }))
    expect(await get().readJob('mlab', src, 'b')).toMatchObject({ ok: false, error: { code: 'wwd_error' } })
    expect(idsOf()).toEqual(['b'])
  })

  it('an origin never read is read whole instead: one job is no baseline', async () => {
    fetchJobsPage.mockResolvedValueOnce(page([job('a')], '1'))
    await get().readJob('mlab', src, 'a')
    expect(fetchJob).not.toHaveBeenCalled()
    expect(idsOf()).toEqual(['a'])
  })
})

describe('a read that ran out of pages', () => {
  it('commits what it reached with its cursor and says there is more, so the next read goes on from there and nothing is missed', async () => {
    let n = 0
    fetchJobsPage.mockImplementation(async () => { n++; return page(Array.from({ length: FIRST_PAGE_LIMIT }, (_, i) => job(`p${n}-${i}`, { state: 'succeeded', created_at: n })), String(n * 1000)) })
    expect(await get().read('mlab', src)).toEqual({ ok: true, more: true })
    expect(fetchJobsPage).toHaveBeenCalledTimes(MAX_PAGES)
    expect(get().byOrigin.mlab.cursor).toBe(String(MAX_PAGES * 1000))
    fetchJobsPage.mockReset()
    fetchJobsPage.mockResolvedValueOnce(page([job('tail')], String(MAX_PAGES * 1000 + 1)))
    expect(await get().read('mlab', src)).toEqual({ ok: true })
    expect(fetchJobsPage.mock.calls[0][1].after).toBe(String(MAX_PAGES * 1000))
  })
})

describe('reads and single-job reads of one origin do not interleave', () => {
  it('a job event that comes while a list is out is applied after it: the list\'s older picture does not undo it', async () => {
    fetchJobsPage.mockResolvedValueOnce(page([job('a', { state: 'running' })], '1'))
    await get().read('mlab', src)
    let release!: (p: JobsPage) => void
    fetchJobsPage.mockReturnValueOnce(new Promise<JobsPage>((r) => { release = r }))
    const list = get().read('mlab', src)
    fetchJob.mockResolvedValueOnce({ job: job('a', { state: 'succeeded' }), log: null })
    const one = get().readJob('mlab', src, 'a')
    await Promise.resolve()
    expect(fetchJob).not.toHaveBeenCalled() // waits its turn
    release(page([job('a', { state: 'running' })], '2')) // the list still carries the older picture
    await Promise.all([list, one])
    expect(get().byOrigin.mlab.jobs.a.state).toBe('succeeded')
  })

  it('a job that was found gone while a list was out stays gone', async () => {
    fetchJobsPage.mockResolvedValueOnce(page([job('a'), job('b')], '1'))
    await get().read('mlab', src)
    let release!: (p: JobsPage) => void
    fetchJobsPage.mockReturnValueOnce(new Promise<JobsPage>((r) => { release = r }))
    const list = get().read('mlab', src)
    fetchJob.mockRejectedValueOnce(readErr(404, { error: 'not_found' }))
    const one = get().readJob('mlab', src, 'a')
    release(page([], '1'))
    await Promise.all([list, one])
    expect(idsOf()).toEqual(['b'])
  })
})

describe('the source of an origin changes (forwarded ⇄ its own daemon)', () => {
  it('a read through another source does not join the one in flight, and starts over: the two views differ', async () => {
    let release!: (p: JobsPage) => void
    fetchJobsPage.mockReturnValueOnce(new Promise<JobsPage>((r) => { release = r }))
    const forwarded = get().read('mlab', { daemonId: 'd1', host: 'mlab' })
    fetchJobsPage.mockResolvedValueOnce(page([job('own'), job('fast', { local: true })], '9'))
    const own = get().read('mlab', { daemonId: 'd-mlab' })
    release(page([job('peer')], '5'))
    await Promise.all([forwarded, own])
    expect(fetchJobsPage.mock.calls.map((c) => [c[0], c[1].host, c[1].after])).toEqual([['d1', 'mlab', '0'], ['d-mlab', undefined, '0']])
    expect(idsOf()).toEqual(['fast', 'own'])
    expect(get().byOrigin.mlab.cursor).toBe('9')
  })

  it('after a completed read, another source does not continue the first one\'s cursor', async () => {
    fetchJobsPage.mockResolvedValueOnce(page([job('peer')], '5'))
    await get().read('mlab', { daemonId: 'd1', host: 'mlab' })
    fetchJobsPage.mockResolvedValueOnce(page([job('own')], '2'))
    await get().read('mlab', { daemonId: 'd-mlab' })
    expect(fetchJobsPage.mock.calls[1][1].after).toBe('0')
    expect(idsOf()).toEqual(['own'])
    // the same source goes on from its cursor as before
    fetchJobsPage.mockResolvedValueOnce(page([], '2'))
    await get().read('mlab', { daemonId: 'd-mlab' })
    expect(fetchJobsPage.mock.calls[2][1].after).toBe('2')
  })
})

describe('an origin named like an Object.prototype member', () => {
  it.each(['toString', 'constructor', '__proto__', 'hasOwnProperty'])('%s has no baseline until it is read: a job event reads it whole', async (origin) => {
    fetchJobsPage.mockResolvedValueOnce(page([job('a', { origin })], '1'))
    await get().readJob(origin, src, 'a')
    expect(fetchJob).not.toHaveBeenCalled()
    expect(fetchJobsPage).toHaveBeenCalledTimes(1)
    expect(Object.keys(get().byOrigin[origin]?.jobs ?? {})).toEqual(['a'])
  })
})

describe('jobSourceFor: the origin\'s own daemon first, forwarding second', () => {
  it('the daemon the App is connected to for that origin answers for itself, with no ?host=', () => {
    expect(jobSourceFor('mlab', { mlab: 'd-mlab', air26: 'd-air26' }, 'd-mlab')).toEqual({ daemonId: 'd-mlab' })
    expect(jobSourceFor('air26', { mlab: 'd-mlab', air26: 'd-air26' }, 'd-mlab')).toEqual({ daemonId: 'd-air26' })
  })
  it('an origin with no daemon of its own is read through the pool daemon, named', () => {
    expect(jobSourceFor('mserv', { mlab: 'd-mlab' }, 'd-mlab')).toEqual({ daemonId: 'd-mlab', host: 'mserv' })
  })
  it('nothing to read through: null', () => {
    expect(jobSourceFor('mserv', {}, null)).toBeNull()
  })
  it('an alias that is an Object.prototype member is no daemon', () => {
    expect(jobSourceFor('toString', {}, 'd1')).toEqual({ daemonId: 'd1', host: 'toString' })
    expect(jobSourceFor('constructor', { mlab: 'd' }, null)).toBeNull()
  })
})
