import { describe, it, expect } from 'vitest'
import { useI18nStore } from '../../stores/useI18nStore'
import { queuedJobsBySession, sharedQueuedJobs, jobsTooltip, type QueuedJobMark } from './queued-sessions'
import type { PoolJob } from '../pool/jobs-types'
import type { PoolHost } from '../pool/types'

const t = (k: string, p?: Record<string, string | number>) => `${k}|${JSON.stringify(p ?? {})}`

// WWD-7b1-6: the pool's jobs light the hourglass of the tab whose session submitted them (`submitter.session`)
const job = (id: string, over: Partial<PoolJob> = {}): PoolJob => ({
  id, origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: 'spa-tsc', args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: 'S-ONE', ref: '', team: '', host: '' }, created_at: 1000, state: 'queued',
  claim: null, result: null, manual: '', manual_seq: 0, ...over,
})
const claim = (worker: string, since = 5000) => ({ worker, attempt: 1, since })
const hosts: PoolHost[] = []

describe('queuedJobsBySession', () => {
  it('queued, claimed and running jobs of a session light it, by lowercase session id; running first, then claimed, then queued', () => {
    const m = queuedJobsBySession([
      job('q', { created_at: 3000 }),
      job('c', { state: 'claimed', claim: claim('air26', 4000) }),
      job('r', { state: 'running', claim: claim('mlab', 2000) }),
    ], 10_000, hosts)
    expect([...m.keys()]).toEqual(['s-one'])
    expect(m.get('s-one')!.map((x) => [x.jobId, x.state])).toEqual([['r', 'running'], ['c', 'claimed'], ['q', 'queued']])
  })

  it('an ended job lights nothing, and neither does a job with no session (every job from before #3477)', () => {
    const m = queuedJobsBySession([
      job('done', { state: 'succeeded' }), job('bad', { state: 'failed' }), job('old', { submitter: { session: '', ref: '', team: '', host: '' } }),
    ], 10_000, hosts)
    expect(m.size).toBe(0)
  })

  it('running elsewhere: the mark names the worker and counts from the claim', () => {
    const m = queuedJobsBySession([job('r', { state: 'running', origin: 'mlab', claim: claim('air26', 7000) })], 10_000, hosts)
    expect(m.get('s-one')![0]).toMatchObject({ state: 'running', worker: 'air26', since: 7000, place: 0, line: 'purdex spa-tsc' })
  })

  it('the place is the place in the display order of ALL queued jobs, not among the session\'s own', () => {
    // a release job of another session goes first (class), then this session's two dev jobs by age
    const m = queuedJobsBySession([
      job('mine-old', { created_at: 1000 }), job('mine-new', { created_at: 2000 }),
      job('other', { class: 'release', created_at: 3000, submitter: { session: 'S-TWO', ref: '', team: '', host: '' } }),
    ], 4000, hosts)
    expect(m.get('s-one')!.map((x) => [x.jobId, x.place])).toEqual([['mine-old', 2], ['mine-new', 3]])
    expect(m.get('s-two')![0].place).toBe(1)
  })

  it('a queued job with no session still holds a place in the display order (it is in the queue, only no tab shows it)', () => {
    const m = queuedJobsBySession([
      job('old', { class: 'release', created_at: 3000, submitter: { session: '', ref: '', team: '', host: '' } }),
      job('mine', { created_at: 1000 }),
    ], 4000, hosts)
    expect(m.get('s-one')!.map((x) => [x.jobId, x.place])).toEqual([['mine', 2]])
  })

  it('two sessions do not see each other\'s jobs', () => {
    const m = queuedJobsBySession([job('a'), job('b', { submitter: { session: 'S-TWO', ref: '', team: '', host: '' } })], 5000, hosts)
    expect(m.get('s-one')!.map((x) => x.jobId)).toEqual(['a'])
    expect(m.get('s-two')!.map((x) => x.jobId)).toEqual(['b'])
  })
})

describe('jobsTooltip', () => {
  const mark = (over: Partial<QueuedJobMark> = {}): QueuedJobMark => ({ jobId: 'j', origin: 'mlab', line: 'purdex spa-tsc', state: 'queued', place: 1, since: 0, worker: '', ...over })
  it('queued: the place and the whole minutes waited', () => {
    expect(jobsTooltip([mark({ place: 2, since: 1000 })], 1000 + 125_000, t)).toBe('tab.queued.job.queued|{"job":"purdex spa-tsc","place":2,"m":2}')
  })
  it('running: where and for how long, in minutes and seconds', () => {
    expect(jobsTooltip([mark({ state: 'running', worker: 'air26', since: 0 })], 84_000, t))
      .toBe('tab.queued.job.running|{"job":"purdex spa-tsc","host":"air26","d":"tab.queued.job.dur.ms|{\\"m\\":1,\\"s\\":24}"}')
    expect(jobsTooltip([mark({ state: 'running', worker: 'air26', since: 0 })], 24_000, t)).toContain('tab.queued.job.dur.s|{\\"s\\":24}')
  })
  it('a worker the daemon did not name falls back to the origin', () => {
    expect(jobsTooltip([mark({ state: 'claimed', worker: '', origin: 'mserv' })], 0, t)).toBe('tab.queued.job.claimed|{"job":"purdex spa-tsc","host":"mserv"}')
  })
  it('two or more: the count and the first three', () => {
    const out = jobsTooltip([mark({ line: 'a' }), mark({ line: 'b' }), mark({ line: 'c' }), mark({ line: 'd' })], 0, t)
    expect(out).toBe('tab.queued.job.many|{"n":4,"list":"a、b、c"}')
  })
  it('real translator (zh-TW): the three spec examples', () => {
    useI18nStore.getState().setLocale('zh-TW')
    const tt = useI18nStore.getState().t
    expect(jobsTooltip([mark({ line: 'spa-tsc', place: 1, since: 0 })], 125_000, tt)).toBe('等測試結果：spa-tsc（排隊第 1 位・已等 2 分鐘）')
    expect(jobsTooltip([mark({ line: 'spa-tsc', state: 'running', worker: 'air26', since: 0 })], 84_000, tt)).toBe('等測試結果：spa-tsc 在 air26 跑了 1 分 24 秒')
    expect(jobsTooltip([mark({ line: 'spa-tsc' }), mark({ line: 'go-test' })], 0, tt)).toBe('等測試結果：2 件（spa-tsc、go-test）')
  })
})

describe('sharedQueuedJobs', () => {
  const origins = (...jobs: PoolJob[]) => ({ mlab: { jobs: Object.fromEntries(jobs.map((j) => [j.id, j])) } })
  const host = (alias: string, zone: string) => ({ alias, labels: [], worker: { enabled: true }, facts: { derived: { zone } } }) as unknown as PoolHost

  it('the same jobs, zones and minute give the SAME result object for every tab that asks', () => {
    const by = origins(job('a'))
    const first = sharedQueuedJobs(by, [host('mlab', 'ok')], 60_000)
    expect(sharedQueuedJobs(by, [host('mlab', 'ok')], 60_000)).toBe(first)
    expect(first.get('s-one')!.map((x) => x.jobId)).toEqual(['a'])
  })

  it('is worked out again when the jobs store holds a new object, a zone moves, or the minute turns', () => {
    const by = origins(job('a'))
    const first = sharedQueuedJobs(by, [host('mlab', 'ok')], 60_000)
    expect(sharedQueuedJobs(origins(job('a')), [host('mlab', 'ok')], 60_000)).not.toBe(first) // jobs
    const second = sharedQueuedJobs(by, [host('mlab', 'ok')], 60_000)
    expect(sharedQueuedJobs(by, [host('mlab', 'danger')], 60_000)).not.toBe(second) // a zone
    const third = sharedQueuedJobs(by, [host('mlab', 'danger')], 60_000)
    expect(sharedQueuedJobs(by, [host('mlab', 'danger')], 120_000)).not.toBe(third) // the minute
  })

  it('counts whole minutes waited at the minute it is given, so the tooltip place and the jobs tab agree', () => {
    const by = origins(
      job('old', { class: 'dev', created_at: 0 }),
      job('new', { class: 'dev', created_at: 119_000, submitter: { session: 'S-TWO', ref: '', team: '', host: '' } }),
    )
    const m = sharedQueuedJobs(by, [], 180_000)
    expect(m.get('s-one')![0].place).toBe(1)
    expect(m.get('s-two')![0].place).toBe(2)
  })
})
