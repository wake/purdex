// spa/src/lib/pool/queue-flow.test.ts — the task queue tab's data (monitoring tabs spec M24, M26–M34): the worker cells, the queue pool cells with why they wait, the summary.
import { describe, it, expect } from 'vitest'
import type { PoolJob } from './jobs-types'
import type { PoolFacts, PoolHost } from './types'
import { poolHostState } from './host-state'
import { queueEntries } from './jobs-lanes'
import { parseFacts } from './parse'
import { busyOf, elapsedSince, flowSummaryOf, poolCellsOf, recentEndedOf, workerCellsOf, type FlowHost } from './queue-flow'
import { WAIT_LONG_S } from '../watch/queue-time'
import darwinJson from './__fixtures__/facts_darwin.json'

const base = parseFacts(darwinJson)!
const NOW = base.at + 1_000

const job = (id: string, over: Partial<PoolJob> = {}): PoolJob => ({
  id, origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: 't', args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: '', ref: '', team: '', host: '' }, created_at: NOW - 1000, state: 'queued',
  claim: null, result: null, manual: '', manual_seq: 0, ...over,
})
const claim = (worker: string, since = 1) => ({ worker, attempt: 1, since })
const running = (id: string, worker: string, over: Partial<PoolJob> = {}) => job(id, { state: 'running', claim: claim(worker), ...over })

interface Spec {
  enabled?: boolean
  slots?: { heavy: number; light: number }
  reasons?: { heavy: string[]; light: string[] }
  exceptions?: PoolFacts['exceptions']
  liveness?: PoolHost['liveness']
  labels?: string[]
}
function flowHost(alias: string, s: Spec = {}): FlowHost {
  const worker = { enabled: s.enabled ?? true, slots: s.slots ?? { heavy: 2, light: 2 }, owner_rule: '' }
  const facts: PoolFacts = { ...base, alias, host_id: `id-${alias}`, worker: { ...worker, running: { heavy: 0, light: 0 } }, exceptions: s.exceptions ?? [] }
  const pool: PoolHost = {
    host_id: `id-${alias}`, alias, version: '1', proto: 1, platform: 'darwin', arch: 'arm64', facts, reachable: true, labels: s.labels ?? ['os:darwin'], worker,
    ...(s.reasons ? { headroom_reasons: s.reasons } : {}), liveness: s.liveness ?? 'ok',
  }
  const state = poolHostState(pool, base.at, NOW)
  return { hostId: pool.host_id, alias, state }
}

describe('busyOf', () => {
  it('a host that is not ok is unreadable, whatever else is true', () => {
    const h = flowHost('mserv', { liveness: 'wwd_down', enabled: false })
    expect(busyOf(h, 'heavy', [])).toEqual({ kind: 'unreadable', state: 'wwd_down' })
  })
  it('worker off', () => {
    expect(busyOf(flowHost('a', { enabled: false }), 'heavy', [])).toEqual({ kind: 'worker_off' })
  })
  it('the first exception reason is a pause, with its until from the facts (M31)', () => {
    const until = NOW + 600_000
    const h = flowHost('mlab', { reasons: { heavy: ['exception:watcher-766', 'zone_warning'], light: [] }, exceptions: [{ name: 'other', since: 1, until: 5 }, { name: 'watcher-766', since: 1, until }] })
    expect(busyOf(h, 'heavy', [])).toEqual({ kind: 'paused', name: 'watcher-766', until })
  })
  it('a pause the facts do not list has no until', () => {
    expect(busyOf(flowHost('mlab', { reasons: { heavy: ['exception:x'], light: [] } }), 'heavy', [])).toEqual({ kind: 'paused', name: 'x', until: null })
  })
  it('another reason code is passed through (worker_off and exception: skipped)', () => {
    expect(busyOf(flowHost('a', { reasons: { heavy: ['gate_full'], light: [] } }), 'heavy', [])).toEqual({ kind: 'code', code: 'gate_full' })
  })
  it('the slots all used is full; one more than the slots too', () => {
    const h = flowHost('a', { slots: { heavy: 1, light: 1 } })
    expect(busyOf(h, 'heavy', [running('r', 'a')])).toEqual({ kind: 'full' })
    expect(busyOf(h, 'light', [running('r', 'a')])).toBeNull()
    expect(busyOf(flowHost('a', { slots: { heavy: 2, light: 2 } }), 'heavy', [running('r', 'a')])).toBeNull()
  })
  it('a daemon with no reasons is judged by the slots alone', () => {
    expect(busyOf(flowHost('a'), 'heavy', [])).toBeNull()
  })
})

describe('workerCellsOf', () => {
  it('a worker that is on has a cell; one that is off with no job is in `off` (M27)', () => {
    const r = workerCellsOf([flowHost('mlab'), flowHost('mserv', { enabled: false })], [])
    expect(r.cells.map((c) => c.alias)).toEqual(['mlab'])
    expect(r.off).toEqual(['mserv'])
  })
  it('a worker that is off but still holds a job keeps its cell, busy worker_off', () => {
    const r = workerCellsOf([flowHost('mserv', { enabled: false })], [running('r', 'mserv')])
    expect(r.off).toEqual([])
    expect(r.cells[0].heavy.jobs.map((j) => j.id)).toEqual(['r'])
    expect(r.cells[0].heavy.busy).toEqual({ kind: 'worker_off' })
  })
  it('slot decides the row; a job without one is heavy; running before claimed', () => {
    const r = workerCellsOf([flowHost('a')], [
      job('c', { state: 'claimed', claim: claim('a', 1) }), running('r', 'a'), running('l', 'a', { slot: 'light' }),
    ])
    expect(r.cells[0].heavy.jobs.map((j) => j.id)).toEqual(['r', 'c'])
    expect(r.cells[0].light.jobs.map((j) => j.id)).toEqual(['l'])
    expect(r.cells[0].heavy.total).toBe(2)
  })
  it('a host that is not ok holding a job: a cell, total unknown, unreadable (M24)', () => {
    const r = workerCellsOf([flowHost('mserv', { liveness: 'wwd_down' })], [running('r', 'mserv')])
    expect(r.cells[0].heavy).toMatchObject({ total: null, busy: { kind: 'unreadable', state: 'wwd_down' } })
  })
  it('a worker the hosts do not list still gets a cell for the job it holds, after the others, the unnamed one last; it is not in `off`', () => {
    const r = workerCellsOf([flowHost('a')], [running('z', 'gone'), running('u', ''), job('c', { state: 'claimed', claim: claim('air19') })])
    expect(r.cells.map((c) => c.alias)).toEqual(['a', 'air19', 'gone', ''])
    expect(r.off).toEqual([])
    const gone = r.cells.find((c) => c.alias === 'gone')!
    expect(gone).toMatchObject({ state: null, hostId: '', heavy: { total: null, busy: null } })
    expect(gone.heavy.jobs.map((j) => j.id)).toEqual(['z'])
  })
  it('queued jobs are not on a worker', () => {
    expect(workerCellsOf([flowHost('a')], [job('q', { claim: claim('a') })]).cells[0].heavy.jobs).toEqual([])
  })
})

describe('poolCellsOf', () => {
  const hosts = [flowHost('mlab', { reasons: { heavy: ['exception:watcher-766'], light: [] } }), flowHost('air26', { reasons: { heavy: ['gate_full'], light: [] } })]
  const cells = (jobs: PoolJob[], hs = hosts) => poolCellsOf(hs, queueEntries(jobs, NOW, hs.map((h) => h.state.pool)), jobs)

  it('M32: eight jobs of mlab, both hosts busy → one line, n 8, both reasons', () => {
    const jobs = Array.from({ length: 8 }, (_, i) => job(`q${i}`, { created_at: NOW - 5000 - i }))
    const mlab = cells(jobs).find((c) => c.alias === 'mlab')!
    expect(mlab.rows).toHaveLength(8)
    expect(mlab.whys).toHaveLength(1)
    expect(mlab.whys[0]).toMatchObject({ n: 8, bad: false, why: { kind: 'busy', hosts: [{ alias: 'mlab', busy: { kind: 'paused', name: 'watcher-766' } }, { alias: 'air26', busy: { kind: 'code', code: 'gate_full' } }] } })
  })
  it('a host that can take it means no why line (it is only waiting its turn)', () => {
    const hs = [flowHost('mlab'), flowHost('air26', { reasons: { heavy: ['gate_full'], light: [] } })]
    expect(cells([job('q')], hs)[0].whys).toEqual([])
  })
  it('one busy candidate among several free ones is not busy (all must be)', () => {
    const hs = [flowHost('mlab', { reasons: { heavy: ['gate_full'], light: [] } }), flowHost('air26')]
    expect(cells([job('q')], hs)[0].whys).toEqual([])
  })
  it('the red line is a bad no_host with the labels', () => {
    const c = cells([job('q', { labels: ['os:linux'] })])[0]
    expect(c.whys).toEqual([{ why: { kind: 'no_host', labels: ['os:linux'] }, n: 1, bad: true }])
  })
  it('a hold is last, is held, and does not count for the longest wait', () => {
    const c = cells([job('h', { manual: 'hold', created_at: 1 }), job('q', { created_at: NOW - 30_000 })])[0]
    expect(c.rows.map((e) => e.job.id)).toEqual(['q', 'h'])
    expect(c.longestS).toBe(30)
    expect(c.whys.map((w) => w.why.kind)).toContain('held')
  })
  it('M24: a host that is not ok is not readable and shows nothing, whatever the store kept', () => {
    const hs = [flowHost('mserv', { liveness: 'wwd_down' }), flowHost('mlab')]
    const c = cells([job('old', { origin: 'mserv' })], hs)
    expect(c[0]).toMatchObject({ alias: 'mserv', readable: false, rows: [], whys: [], longestS: null })
    expect(c[1].readable).toBe(true)
  })
  it('away counts what this host sent that runs or is being prepared anywhere', () => {
    const c = cells([running('r', 'air26'), job('p', { state: 'claimed', claim: claim('mlab') }), running('x', 'air26', { origin: 'air26' })])
    expect(c.find((x) => x.alias === 'mlab')!.away).toBe(2)
    expect(c.find((x) => x.alias === 'air26')!.away).toBe(1)
  })
  it('the cells follow the hosts\' order; the rows follow queueEntries\' order', () => {
    const jobs = [job('b', { created_at: NOW - 1000 }), job('a', { created_at: NOW - 9000 })]
    const c = cells(jobs, [...hosts].reverse())
    expect(c.map((x) => x.alias)).toEqual(['air26', 'mlab'])
    const want = queueEntries(jobs, NOW, hosts.map((h) => h.state.pool)).map((e) => e.job.id)
    expect(c[1].rows.map((e) => e.job.id)).toEqual(want)
  })
})

describe('the summary, the clock and the recent ones', () => {
  const entries = (jobs: PoolJob[]) => queueEntries(jobs, NOW, [flowHost('mlab').state.pool])
  it('counts queued, heavy and light, running, preparing and held', () => {
    const jobs = [job('a'), job('b', { slot: 'light' }), job('h', { manual: 'hold' }), running('r', 'mlab'), job('p', { state: 'claimed', claim: claim('mlab') })]
    expect(flowSummaryOf(entries(jobs), jobs)).toMatchObject({ queued: 2, heavy: 1, light: 1, running: 1, preparing: 1, held: 1 })
  })
  it('the longest wait is long only past two minutes', () => {
    const at = (s: number) => [job('a', { created_at: NOW - s * 1000 })]
    expect(flowSummaryOf(entries(at(WAIT_LONG_S)), at(WAIT_LONG_S))).toMatchObject({ longestS: WAIT_LONG_S, long: false })
    expect(flowSummaryOf(entries(at(WAIT_LONG_S + 1)), at(WAIT_LONG_S + 1))).toMatchObject({ longestS: WAIT_LONG_S + 1, long: true })
    expect(flowSummaryOf([], [])).toMatchObject({ longestS: null, long: false })
  })
  it('elapsedSince: started_at, else the claim; a job being prepared has none', () => {
    expect(elapsedSince(running('r', 'a', { started_at: 77 }))).toBe(77)
    expect(elapsedSince(running('r', 'a'))).toBe(1)
    expect(elapsedSince(job('c', { state: 'claimed', claim: claim('a', 9) }))).toBeNull()
  })
  it('recentEndedOf: five, newest first', () => {
    const ended = (i: number) => job(`e${i}`, { state: 'succeeded', result: { exit: 0, summary: null, log: '', host: 'a', started_at: 1, finished_at: 100 + i, cache_hit: false, reason: '', superseded_by: null } })
    const r = recentEndedOf(Array.from({ length: 7 }, (_, i) => ended(i)))
    expect(r.map((j) => j.id)).toEqual(['e6', 'e5', 'e4', 'e3', 'e2'])
  })
})
