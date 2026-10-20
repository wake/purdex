// spa/src/lib/pool/jobs-view.test.ts — what the jobs tab derives from the jobs the store keeps (plan "WWD-7 design", Jobs tab): the queue's display order, the lanes, the red line, the history and the result column.
import { describe, it, expect } from 'vitest'
import type { PoolJob } from './jobs-types'
import { baseScore, displayOrder, hostCanTake, lanes, noHostToGoTo, historyOf, historyHosts, historyRepos, whenText, resultWord, resultText, type PoolHostLabels } from './jobs-view'

const job = (id: string, over: Partial<PoolJob> = {}): PoolJob => ({
  id, origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: 't', args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: '', ref: '', team: '', host: '' }, created_at: 1, state: 'queued',
  claim: null, result: null, manual: '', manual_seq: 0, ...over,
})
const MIN = 60_000
const idsOf = (jobs: PoolJob[]) => jobs.map((j) => j.id)
const noZone = () => undefined

describe('the host-independent part of the score (spec 4.6)', () => {
  it('1000 per class rank (release 3, merge 2, dev 1; an unknown class is dev), plus the minutes waited', () => {
    expect(baseScore(job('a', { class: 'release', created_at: 0 }), 5 * MIN, undefined, 0)).toBe(3005)
    expect(baseScore(job('a', { class: 'merge', created_at: 0 }), 0, undefined, 0)).toBe(2000)
    expect(baseScore(job('a', { class: 'dev', created_at: 0 }), 0, undefined, 0)).toBe(1000)
    expect(baseScore(job('a', { class: 'future', created_at: 0 }), 0, undefined, 0)).toBe(1000)
  })

  it('+200 when the origin is in warning, +400 in danger, nothing for the other zones', () => {
    const j = job('a', { created_at: 0 })
    expect(baseScore(j, 0, 'warning', 0)).toBe(1200)
    expect(baseScore(j, 0, 'danger', 0)).toBe(1400)
    expect(baseScore(j, 0, 'normal', 0)).toBe(1000)
    expect(baseScore(j, 0, 'unknown', 0)).toBe(1000)
  })

  it('−30 per earlier queued job of the same session, and a wait never counts below zero', () => {
    const j = job('a', { created_at: 10 * MIN })
    expect(baseScore(j, 10 * MIN, undefined, 2)).toBe(940)
    expect(baseScore(j, 0, undefined, 0)).toBe(1000)
  })
})

describe('the queue in display order', () => {
  const now = 100 * MIN
  it('front first by manual_seq, then the score, then back by manual_seq, hold last', () => {
    const jobs = [
      job('hold', { manual: 'hold', manual_seq: 1, class: 'release' }),
      job('back2', { manual: 'back', manual_seq: 6 }),
      job('score-low', { class: 'dev', created_at: now }),
      job('front2', { manual: 'front', manual_seq: 4, class: 'dev' }),
      job('score-high', { class: 'release', created_at: now }),
      job('back1', { manual: 'back', manual_seq: 5 }),
      job('front1', { manual: 'front', manual_seq: 3 }),
    ]
    expect(idsOf(displayOrder(jobs, now, noZone))).toEqual(['front1', 'front2', 'score-high', 'score-low', 'back1', 'back2', 'hold'])
  })

  it('pins with the same manual_seq (each origin counts its own) go by created_at, then id, as the worker does', () => {
    const jobs = [
      job('a-late', { manual: 'front', manual_seq: 1, origin: 'x', created_at: 9 }),
      job('z-early', { manual: 'front', manual_seq: 1, origin: 'y', created_at: 3 }),
      job('b-back-late', { manual: 'back', manual_seq: 2, created_at: 8 }),
      job('y-back-early', { manual: 'back', manual_seq: 2, created_at: 2 }),
      job('m-same-1', { manual: 'hold', manual_seq: 4, created_at: 5 }),
      job('l-same-2', { manual: 'hold', manual_seq: 4, created_at: 5 }),
    ]
    expect(idsOf(displayOrder(jobs, now, noZone))).toEqual(['z-early', 'a-late', 'y-back-early', 'b-back-late', 'l-same-2', 'm-same-1'])
  })

  it('only queued jobs are in the queue', () => {
    const jobs = [job('q'), job('c', { state: 'claimed' }), job('r', { state: 'running' }), job('d', { state: 'succeeded' })]
    expect(idsOf(displayOrder(jobs, now, noZone))).toEqual(['q'])
  })

  it('equal scores go by created_at, then by id', () => {
    const jobs = [job('b', { created_at: 5 }), job('a', { created_at: 5 }), job('c', { created_at: 4 })]
    // the waits differ by less than a minute: whole minutes only
    expect(idsOf(displayOrder(jobs, 5, noZone))).toEqual(['c', 'a', 'b'])
  })

  it('the class weight decides over a longer wait', () => {
    const jobs = [job('dev-old', { class: 'dev', created_at: 0 }), job('merge-new', { class: 'merge', created_at: 99 * MIN })]
    expect(idsOf(displayOrder(jobs, now, noZone))).toEqual(['merge-new', 'dev-old'])
  })

  it('an origin in danger lifts its jobs over another origin\'s of the same class', () => {
    const jobs = [job('calm', { origin: 'a', created_at: now }), job('hot', { origin: 'b', created_at: now })]
    expect(idsOf(displayOrder(jobs, now, (o) => (o === 'b' ? 'danger' : 'normal')))).toEqual(['hot', 'calm'])
  })

  it('the same session\'s later jobs are pushed back by the earlier ones; a job with no session is never penalised', () => {
    const s = { session: 's1', ref: '', team: '', host: '' }
    const jobs = [
      job('first', { submitter: s, created_at: 1 }),
      job('second', { submitter: s, created_at: 2 }),
      job('third', { submitter: s, created_at: 3 }),
      job('other', { created_at: 3 }),
    ]
    // first 1000, second 970, third 940, other 1000: 'other' ties with 'first' on score and was queued later
    expect(idsOf(displayOrder(jobs, 3, noZone))).toEqual(['first', 'other', 'second', 'third'])
  })
})

describe('the lanes: a claimed or running job sits with the worker that holds it', () => {
  it('running and claimed by claim.worker, running ones first, each by when it was claimed', () => {
    const claim = (worker: string, since: number) => ({ worker, attempt: 1, since })
    const jobs = [
      job('c1', { state: 'claimed', claim: claim('mserv', 5) }),
      job('r2', { state: 'running', claim: claim('mserv', 9) }),
      job('r1', { state: 'running', claim: claim('mserv', 7) }),
      job('other', { state: 'running', claim: claim('air26', 1) }),
      job('q', { state: 'queued' }),
      job('done', { state: 'succeeded', claim: claim('mserv', 1) }),
    ]
    const l = lanes(jobs)
    expect(idsOf(l.get('mserv') ?? [])).toEqual(['r1', 'r2', 'c1'])
    expect(idsOf(l.get('air26') ?? [])).toEqual(['other'])
    expect(l.size).toBe(2)
  })

  it('a live job with no claim is not lost: it sits in the lane named \'\'', () => {
    expect(idsOf(lanes([job('x', { state: 'running' })]).get('') ?? [])).toEqual(['x'])
  })
})

describe('"no host to go to": judged against the hosts whose worker is on', () => {
  const hosts: PoolHostLabels[] = [
    { alias: 'mlab', labels: ['os:darwin', 'arch:arm64'], workerEnabled: true },
    { alias: 'mserv', labels: ['os:linux'], workerEnabled: false },
  ]
  it('is null when an enabled host holds every label of the job', () => {
    expect(noHostToGoTo(job('a', { labels: ['os:darwin'] }), hosts)).toBeNull()
    expect(noHostToGoTo(job('a', { labels: [] }), hosts)).toBeNull()
  })
  it('names the job\'s labels when only a host whose worker is off holds them', () => {
    expect(noHostToGoTo(job('a', { labels: ['os:linux'] }), hosts)).toEqual({ labels: ['os:linux'] })
  })
  it('one host must hold all of them: two hosts holding one each is not enough', () => {
    expect(noHostToGoTo(job('a', { labels: ['os:darwin', 'os:linux'] }), [{ alias: 'a', labels: ['os:darwin'], workerEnabled: true }, { alias: 'b', labels: ['os:linux'], workerEnabled: true }])).toEqual({ labels: ['os:darwin', 'os:linux'] })
  })
  it('nothing is said while no host is known (the pool has not been read)', () => {
    expect(noHostToGoTo(job('a', { labels: ['os:linux'] }), [])).toBeNull()
  })
  it('with hosts known and none of them taking work, the red line says so', () => {
    expect(noHostToGoTo(job('a'), [{ alias: 'x', labels: [], workerEnabled: false }])).toEqual({ labels: [] })
  })
})

describe('hostCanTake', () => {
  const j = job('a', { labels: ['os:darwin'] })
  it.each([
    [true, ['os:darwin'], true],
    [true, ['os:linux'], false],
    [false, ['os:darwin'], false],
    [false, ['os:linux'], false],
  ])('worker on=%s, labels %j → %s', (workerEnabled, labels, want) => {
    expect(hostCanTake(j, { alias: 'x', labels, workerEnabled })).toBe(want)
  })
})

describe('the history', () => {
  const ended = (id: string, finished: number, over: Partial<PoolJob> = {}): PoolJob =>
    job(id, { state: 'succeeded', result: { exit: 0, summary: null, log: '', host: 'mserv', started_at: 1, finished_at: finished, cache_hit: false, reason: '', superseded_by: null }, ...over })

  it('only ended jobs, the newest first (by when they finished, else when they were queued)', () => {
    const jobs = [ended('old', 10), ended('new', 30), job('live'), ended('unfinished', 0, { state: 'cancelled', result: null, created_at: 20 })]
    expect(idsOf(historyOf(jobs, {}))).toEqual(['new', 'unfinished', 'old'])
  })

  it('filters by state, by repo and by the host it ran on; a job that never ran has no host and shows only unfiltered', () => {
    const jobs = [
      ended('a', 3, { repo: { name: 'purdex', root: '', remote: '' } }),
      ended('b', 2, { state: 'failed', repo: { name: 'mlab', root: '', remote: '' } }),
      ended('c', 1, { state: 'superseded', result: null, origin: 'air26', repo: { name: 'aigora', root: '', remote: '' } }),
    ]
    expect(idsOf(historyOf(jobs, { group: 'failed' }))).toEqual(['b'])
    expect(idsOf(historyOf(jobs, { repo: 'purdex' }))).toEqual(['a'])
    expect(idsOf(historyOf(jobs, { host: 'mserv' }))).toEqual(['a', 'b'])
    expect(idsOf(historyOf(jobs, { host: 'air26' }))).toEqual([])
    expect(idsOf(historyOf(jobs, {}))).toEqual(['a', 'b', 'c'])
    expect(idsOf(historyOf(jobs, { group: 'succeeded', repo: 'mlab' }))).toEqual([])
  })

  it('the state groups: cancelled takes superseded with it; an expired job is its own; unfiltered shows them all', () => {
    const jobs = [
      ended('s', 6), ended('f', 5, { state: 'failed' }), ended('c', 4, { state: 'cancelled' }), ended('u', 3, { state: 'superseded', result: null }),
      ended('x', 2, { state: 'expired', result: null }),
    ]
    expect(idsOf(historyOf(jobs, { group: 'succeeded' }))).toEqual(['s'])
    expect(idsOf(historyOf(jobs, { group: 'failed' }))).toEqual(['f'])
    expect(idsOf(historyOf(jobs, { group: 'cancelled' }))).toEqual(['c', 'u'])
    expect(idsOf(historyOf(jobs, { group: 'expired' }))).toEqual(['x'])
    // the two with no result are ordered by when they were queued (the same here), then by id
    expect(idsOf(historyOf(jobs, {}))).toEqual(['s', 'f', 'c', 'u', 'x'])
  })
})

describe('the history\'s filter choices', () => {
  const ended = (id: string, over: Partial<PoolJob> = {}): PoolJob =>
    job(id, { state: 'succeeded', result: { exit: 0, summary: null, log: '', host: 'mserv', started_at: 1, finished_at: 2, cache_hit: false, reason: '', superseded_by: null }, ...over })

  it('the hosts jobs ran on and the repos, each once, in order; a job that never ran adds no host; live jobs add nothing', () => {
    const jobs = [
      ended('a', { repo: { name: 'purdex', root: '', remote: '' } }),
      ended('b', { result: null, claim: { worker: 'air26', attempt: 1, since: 1 }, repo: { name: 'aigora', root: '', remote: '' } }),
      ended('c', { state: 'superseded', result: null, repo: { name: 'purdex', root: '', remote: '' } }),
      job('live', { repo: { name: 'live-repo', root: '', remote: '' }, claim: { worker: 'mlab', attempt: 1, since: 1 }, state: 'running' }),
    ]
    expect(historyHosts(jobs)).toEqual(['air26', 'mserv'])
    expect(historyRepos(jobs)).toEqual(['aigora', 'purdex'])
  })
})

describe('whenText', () => {
  const NOW = new Date(2026, 9, 20, 12, 0).getTime()
  it('today it is the time; another day it is the date and the time', () => {
    expect(whenText(new Date(2026, 9, 20, 9, 5).getTime(), NOW)).toBe('09:05')
    expect(whenText(new Date(2026, 9, 19, 23, 59).getTime(), NOW)).toBe('10/19 23:59')
    expect(whenText(new Date(2025, 11, 31, 8, 0).getTime(), NOW)).toBe('2025/12/31 08:00')
  })
  it('a time that is not known reads 「—」', () => {
    expect(whenText(null, NOW)).toBe('—')
  })
})

describe('the result column', () => {
  const res = (over: Partial<NonNullable<PoolJob['result']>> = {}) => ({ exit: null, summary: null, log: '', host: '', started_at: null, finished_at: null, cache_hit: false, reason: '', superseded_by: null, ...over })
  const ended = (state: string, result: PoolJob['result']) => job('j', { state, result })
  const t = (key: string, opts?: Record<string, string | number>) => (opts ? `${key}:${JSON.stringify(opts)}` : key)

  it('a summary says how many passed, or how many failed when any did', () => {
    const s = (passed: number, failed: number) => ({ passed, failed, skipped: 0, failures: [], tail: [] })
    expect(resultWord(ended('succeeded', res({ exit: 0, summary: s(12, 0) })))).toEqual({ kind: 'summary', passed: 12, failed: 0 })
    expect(resultWord(ended('failed', res({ exit: 1, summary: s(10, 2) })))).toEqual({ kind: 'summary', passed: 10, failed: 2 })
    expect(resultText({ kind: 'summary', passed: 12, failed: 0 }, t)).toBe('pool.job.result.passed:{"n":12}')
    expect(resultText({ kind: 'summary', passed: 10, failed: 2 }, t)).toBe('pool.job.result.failed:{"n":2}')
  })

  it('without a summary the exit code, then the reason in words', () => {
    expect(resultWord(ended('failed', res({ exit: 2 })))).toEqual({ kind: 'exit', code: 2 })
    expect(resultText({ kind: 'exit', code: 2 }, t)).toBe('pool.job.result.exit:{"code":2}')
    for (const [reason, key] of [['lost', 'lost'], ['oom', 'oom'], ['timeout', 'timeout'], ['infra', 'infra'], ['queue_timeout', 'queue_timeout']] as const) {
      expect(resultText(resultWord(ended('failed', res({ reason }))), t)).toBe(`pool.job.result.reason.${key}`)
    }
  })

  it('a reason the App does not know is shown as it is', () => {
    expect(resultText(resultWord(ended('failed', res({ reason: 'disk_full' }))), t)).toBe('disk_full')
  })

  it('"superseded_by:<id>" in the reason, or a set superseded_by, is 被較新的取代 with the id to link, whatever the state', () => {
    for (const state of ['cancelled', 'succeeded', 'failed']) {
      expect(resultWord(ended(state, res({ reason: 'superseded_by:j9', exit: 0 })))).toEqual({ kind: 'superseded_by', by: 'j9' })
    }
    expect(resultWord(ended('cancelled', res({ superseded_by: 'j7' })))).toEqual({ kind: 'superseded_by', by: 'j7' })
    expect(resultText({ kind: 'superseded_by', by: 'j9' }, t)).toBe('pool.job.result.superseded_by')
  })

  it('state superseded (it was still queued) is 被取代; a job with nothing to say says nothing', () => {
    expect(resultWord(ended('superseded', null))).toEqual({ kind: 'superseded' })
    expect(resultText({ kind: 'superseded' }, t)).toBe('pool.job.result.superseded')
    expect(resultWord(ended('succeeded', null))).toEqual({ kind: 'none' })
    expect(resultWord(job('q'))).toEqual({ kind: 'none' })
    expect(resultText({ kind: 'none' }, t)).toBe('')
  })
})
