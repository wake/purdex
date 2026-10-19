// spa/src/lib/pool/jobs-driver.test.ts — what drives the jobs store (plan "WWD-7 design", Data flow: Jobs): which daemon answers for an origin, a read that is not finished goes on at once, a failure is acted on by the action of its error
// (next source, the same one again after 1–2 s, back-off 2/4/8 … 30 s, drop the origin), a `jobs.changed` re-reads that one job, a host-events reconnect reads after the cursor, and an origin no daemon announces is polled every 10 s while a view watches.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createJobsDriver, POLL_MS, type JobsTopology, type JobsStoreLike } from './jobs-driver'
import type { ReadOutcome } from './jobs-store'
import { classify } from './errors'

const ok: ReadOutcome = { ok: true }
const more: ReadOutcome = { ok: true, more: true }
const fail = (status: number | null, body: unknown = null): ReadOutcome => ({ ok: false, error: classify(status, body) })
const unavailable = fail(503, { error: 'wwd_unavailable' }) // next_source
const busy = fail(429) // retry_same
const failing = fail(502, { error: 'wwd_error' }) // backoff
const cursorAhead = fail(409) // reset_origin

let topo: JobsTopology
let seen: string[]
let store: JobsStoreLike & { read: ReturnType<typeof vi.fn>; readJob: ReturnType<typeof vi.fn>; drop: ReturnType<typeof vi.fn> }
let have: Set<string>
let hidden: boolean

const driver = () => createJobsDriver({ store, topology: () => topo, visible: () => !hidden, rand: () => 0, known: (o) => have.has(o) })
const src = (c: unknown[]) => `${(c[1] as { daemonId: string }).daemonId}${(c[1] as { host?: string }).host ? `>${(c[1] as { host?: string }).host}` : ''}`

beforeEach(() => {
  vi.useFakeTimers()
  hidden = false
  have = new Set()
  seen = []
  topo = { origins: ['mlab', 'mserv'], ownDaemon: { mlab: 'h-mlab' }, forwarders: ['h-mlab', 'h-air'] }
  store = {
    read: vi.fn(async (o: string) => { seen.push(`read ${o}`); return ok }),
    readJob: vi.fn(async (o: string, _s: unknown, id: string) => { seen.push(`job ${o} ${id}`); return ok }),
    drop: vi.fn(),
  }
})
afterEach(() => { vi.useRealTimers() })

describe('which daemon answers', () => {
  it('an origin the App has a daemon for is read from it, without ?host=; another through the first forwarding daemon, named', async () => {
    const d = driver()
    await d.jobChanged('mlab', 'a')
    await d.jobChanged('mserv', 'b')
    expect(store.readJob.mock.calls.map((c) => [c[0], c[1], c[2]])).toEqual([
      ['mlab', { daemonId: 'h-mlab' }, 'a'],
      ['mserv', { daemonId: 'h-mlab', host: 'mserv' }, 'b'],
    ])
  })

  it('nothing to read through: nothing is asked, nothing throws', async () => {
    topo = { origins: ['mserv'], ownDaemon: {}, forwarders: [] }
    const d = driver()
    await d.jobChanged('mserv', 'b')
    d.sync()
    await vi.advanceTimersByTimeAsync(0)
    expect(store.read).not.toHaveBeenCalled()
    expect(store.readJob).not.toHaveBeenCalled()
  })
})

describe('a read that did not finish goes on at once', () => {
  it('while the store says there is more', async () => {
    topo = { origins: ['mlab'], ownDaemon: { mlab: 'h-mlab' }, forwarders: [] }
    store.read.mockResolvedValueOnce(more).mockResolvedValueOnce(more).mockResolvedValueOnce(ok)
    const d = driver()
    d.hostOpened('h-mlab')
    await vi.advanceTimersByTimeAsync(0)
    expect(store.read).toHaveBeenCalledTimes(3)
  })
})

describe('a failure is acted on by its action', () => {
  it('next_source: the next daemon that can answer for the origin, at once; all tried → back-off', async () => {
    store.read.mockResolvedValue(unavailable)
    const d = driver()
    d.sync()
    await vi.advanceTimersByTimeAsync(0)
    // mserv: only forwarders (h-mlab, then h-air); mlab: its own, then the forwarders that are not that same daemon
    const mserv = store.read.mock.calls.filter((c) => c[0] === 'mserv').map(src)
    expect(mserv).toEqual(['h-mlab>mserv', 'h-air>mserv'])
    const mlab = store.read.mock.calls.filter((c) => c[0] === 'mlab').map(src)
    expect(mlab).toEqual(['h-mlab', 'h-air>mlab'])
    store.read.mockClear()
    await vi.advanceTimersByTimeAsync(1999)
    expect(store.read).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(store.read).toHaveBeenCalled()
  })

  it('retry_same: the same daemon again after 1–2 s', async () => {
    topo = { origins: ['mlab'], ownDaemon: { mlab: 'h-mlab' }, forwarders: ['h-mlab', 'h-air'] }
    store.read.mockResolvedValueOnce(busy).mockResolvedValue(ok)
    const d = driver()
    d.sync()
    await vi.advanceTimersByTimeAsync(0)
    expect(store.read).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(999)
    expect(store.read).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(store.read).toHaveBeenCalledTimes(2)
    expect(src(store.read.mock.calls[1])).toBe('h-mlab')
  })

  it('backoff: 2, 4, 8 … s on the same daemon, and a good read starts it over', async () => {
    topo = { origins: ['mlab'], ownDaemon: { mlab: 'h-mlab' }, forwarders: [] }
    store.read.mockResolvedValue(failing)
    const d = driver()
    d.sync()
    await vi.advanceTimersByTimeAsync(0)
    expect(store.read).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(2000)
    expect(store.read).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(3999)
    expect(store.read).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1)
    expect(store.read).toHaveBeenCalledTimes(3)
    await vi.advanceTimersByTimeAsync(8000)
    expect(store.read).toHaveBeenCalledTimes(4)
    store.read.mockResolvedValue(ok)
    await vi.advanceTimersByTimeAsync(16000)
    expect(store.read).toHaveBeenCalledTimes(5)
    // good: a new failure waits 2 s again
    store.read.mockResolvedValue(failing)
    d.hostOpened('h-mlab')
    await vi.advanceTimersByTimeAsync(0)
    const n = store.read.mock.calls.length
    await vi.advanceTimersByTimeAsync(2000)
    expect(store.read.mock.calls.length).toBe(n + 1)
  })

  it('reset_origin (a cursor from another database that came again): that origin\'s jobs are dropped, then read again after a back-off', async () => {
    topo = { origins: ['mlab'], ownDaemon: { mlab: 'h-mlab' }, forwarders: [] }
    store.read.mockResolvedValueOnce(cursorAhead).mockResolvedValue(ok)
    const d = driver()
    d.sync()
    await vi.advanceTimersByTimeAsync(0)
    expect(store.drop).toHaveBeenCalledWith('mlab')
    expect(store.read).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(2000)
    expect(store.read).toHaveBeenCalledTimes(2)
  })

  it('a job that could not be read is read again as a whole list, not as that one job', async () => {
    store.readJob.mockResolvedValueOnce(failing)
    const d = driver()
    await d.jobChanged('mlab', 'a')
    await vi.advanceTimersByTimeAsync(2000)
    expect(store.readJob).toHaveBeenCalledTimes(1)
    expect(store.read).toHaveBeenCalledTimes(1)
    expect(store.read.mock.calls[0][0]).toBe('mlab')
  })

  it('two failures waiting for their time are one read, not two', async () => {
    topo = { origins: ['mlab'], ownDaemon: { mlab: 'h-mlab' }, forwarders: [] }
    store.readJob.mockResolvedValue(failing)
    store.read.mockResolvedValue(ok)
    const d = driver()
    await d.jobChanged('mlab', 'a')
    await d.jobChanged('mlab', 'b')
    await vi.advanceTimersByTimeAsync(2000)
    expect(store.read).toHaveBeenCalledTimes(1)
  })
})

describe('a host-events connection that opened (or came back)', () => {
  it('reads again after the cursor the origins that daemon answers for, and no others', async () => {
    topo = { origins: ['mlab', 'mserv', 'air19'], ownDaemon: { mlab: 'h-mlab', air19: 'h-air' }, forwarders: ['h-mlab', 'h-air'] }
    const d = driver()
    d.hostOpened('h-air')
    await vi.advanceTimersByTimeAsync(0)
    expect(store.read.mock.calls.map((c) => c[0]).sort()).toEqual(['air19'])
  })
})

describe('what appears later is read', () => {
  it('an origin the store has not got is read when the topology shows it; one it has is left alone', async () => {
    have.add('mlab')
    const d = driver()
    d.sync()
    await vi.advanceTimersByTimeAsync(0)
    expect(store.read.mock.calls.map((c) => c[0])).toEqual(['mserv'])
  })
})

describe('polling the origins no daemon announces', () => {
  it('every 10 s while something watches, only the origins without a daemon of their own', async () => {
    have.add('mlab'); have.add('mserv')
    const d = driver()
    const release = d.watch()
    await vi.advanceTimersByTimeAsync(0)
    expect(store.read).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(POLL_MS)
    expect(store.read.mock.calls.map((c) => c[0])).toEqual(['mserv'])
    await vi.advanceTimersByTimeAsync(POLL_MS)
    expect(store.read.mock.calls.map((c) => c[0])).toEqual(['mserv', 'mserv'])
    release()
    await vi.advanceTimersByTimeAsync(POLL_MS * 3)
    expect(store.read).toHaveBeenCalledTimes(2)
  })

  it('two watchers: the poll stops with the last one', async () => {
    have.add('mlab'); have.add('mserv')
    const d = driver()
    const a = d.watch()
    const b = d.watch()
    a()
    await vi.advanceTimersByTimeAsync(POLL_MS)
    expect(store.read).toHaveBeenCalledTimes(1)
    b()
    b() // a second release of the same watcher does nothing
    await vi.advanceTimersByTimeAsync(POLL_MS * 2)
    expect(store.read).toHaveBeenCalledTimes(1)
  })

  it('a hidden window is not polled; turning visible reads at once', async () => {
    have.add('mlab'); have.add('mserv')
    const d = driver()
    d.watch()
    hidden = true
    await vi.advanceTimersByTimeAsync(POLL_MS * 2)
    expect(store.read).not.toHaveBeenCalled()
    hidden = false
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(0)
    expect(store.read.mock.calls.map((c) => c[0])).toEqual(['mserv'])
  })
})
