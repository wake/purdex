// spa/src/lib/pool/jobs-lanes.test.ts — what the jobs tab draws, as data: one lane per pool host (its heavy slots, the jobs it holds, why it takes no more) and the queue entries with their red line.
import { describe, it, expect } from 'vitest'
import type { PoolJob } from './jobs-types'
import type { PoolHost } from './types'
import { laneModels, queueEntries } from './jobs-lanes'
import { WAIT_LONG_S } from '../watch/queue-time'

const job = (id: string, over: Partial<PoolJob> = {}): PoolJob => ({
  id, origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: 't', args: [], class: 'dev', labels: [], locks: [], cacheable: false,
  timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: '', ref: '', team: '', host: '' }, created_at: 1, state: 'queued',
  claim: null, result: null, manual: '', manual_seq: 0, ...over,
})
const host = (alias: string, over: Partial<PoolHost> = {}): PoolHost => ({
  host_id: `id-${alias}`, alias, version: '1', proto: 1, platform: 'darwin', arch: 'arm64', facts: null, reachable: true, labels: ['os:darwin'],
  worker: { enabled: true, slots: { heavy: 2, light: 2 }, owner_rule: '' }, ...over,
})
const claim = (worker: string, since = 1) => ({ worker, attempt: 1, since })
const ids = (js: PoolJob[]) => js.map((j) => j.id)

describe('laneModels', () => {
  it('one lane per pool host, in the pool\'s order, with the jobs it holds (running first) and its heavy slots', () => {
    const jobs = [
      job('c', { state: 'claimed', claim: claim('mserv', 5) }),
      job('r', { state: 'running', claim: claim('mserv', 9) }),
      job('x', { state: 'running', claim: claim('mlab') }),
    ]
    const lanes = laneModels([host('mlab'), host('mserv', { worker: { enabled: true, slots: { heavy: 3, light: 1 }, owner_rule: '' } })], jobs)
    expect(lanes.map((l) => l.alias)).toEqual(['mlab', 'mserv'])
    expect(lanes[1]).toMatchObject({ off: false, total: 3, used: 2 })
    expect(ids(lanes[1].jobs)).toEqual(['r', 'c'])
    expect(lanes[0]).toMatchObject({ total: 2, used: 1 })
  })

  it('a host whose worker is off is one line: nothing taken (and a job it still holds is kept, not lost)', () => {
    const lanes = laneModels([host('mserv', { worker: { enabled: false, slots: { heavy: 1, light: 1 }, owner_rule: '' } })], [job('r', { state: 'running', claim: claim('mserv') })])
    expect(lanes[0]).toMatchObject({ off: true })
    expect(ids(lanes[0].jobs)).toEqual(['r'])
  })

  it('more jobs than slots draw more slots: a lane never hides a job', () => {
    const lanes = laneModels([host('mlab', { worker: { enabled: true, slots: { heavy: 1, light: 1 }, owner_rule: '' } })], [
      job('a', { state: 'running', claim: claim('mlab', 1) }), job('b', { state: 'running', claim: claim('mlab', 2) }),
    ])
    expect(lanes[0].total).toBe(2)
    expect(lanes[0].used).toBe(2)
  })

  it('a worker the pool does not list gets a lane of its own, after the others; a live job with no claim sits in the lane named \'\'', () => {
    const lanes = laneModels([host('mlab')], [
      job('a', { state: 'running', claim: claim('air19') }),
      job('b', { state: 'running' }),
    ])
    expect(lanes.map((l) => l.alias)).toEqual(['mlab', 'air19', ''])
    expect(lanes[1]).toMatchObject({ off: false, total: 1, used: 1 })
  })

  it('says why no more is taken only when a slot is free and the daemon gave a reason for heavy work', () => {
    const withReasons = (heavy: string[]) => host('mlab', { headroom_reasons: { heavy, light: [] } })
    expect(laneModels([withReasons(['owner_active'])], [])[0].reason).toBe('owner_active')
    expect(laneModels([withReasons(['zone_warning', 'owner_active'])], [])[0].reason).toBe('zone_warning')
    expect(laneModels([withReasons([])], [])[0].reason).toBeNull()
    expect(laneModels([host('mlab')], [])[0].reason).toBeNull()
    // slots full: the lane is full, which says it
    const full = laneModels([withReasons(['slots_full'])], [job('a', { state: 'running', claim: claim('mlab') }), job('b', { state: 'running', claim: claim('mlab') })])
    expect(full[0].reason).toBeNull()
  })
})

describe('queueEntries', () => {
  const now = 100 * 60_000
  it('the queued jobs in display order, each with how long it has waited', () => {
    const e = queueEntries([job('late', { created_at: now - 5_000 }), job('early', { created_at: now - 125_000 }), job('run', { state: 'running' })], now, [host('mlab')])
    expect(e.map((x) => x.job.id)).toEqual(['early', 'late']) // same class: the longer wait first
    const early = e.find((x) => x.job.id === 'early')!
    const late = e.find((x) => x.job.id === 'late')!
    expect(early.waitS).toBe(125)
    expect(early.long).toBe(125 > WAIT_LONG_S)
    expect(late.long).toBe(false)
  })

  it('a held job is dimmed', () => {
    const e = queueEntries([job('h', { manual: 'hold' }), job('n')], now, [host('mlab')])
    expect(e.find((x) => x.job.id === 'h')!.held).toBe(true)
    expect(e.find((x) => x.job.id === 'n')!.held).toBe(false)
  })

  it('the red line is on a job that no host with its worker on can take, and only then', () => {
    const hosts = [host('mlab'), host('mserv', { labels: ['os:linux'], worker: { enabled: false, slots: { heavy: 1, light: 1 }, owner_rule: '' } })]
    const e = queueEntries([job('php', { labels: ['os:linux'] }), job('ok', { labels: ['os:darwin'] })], now, hosts)
    expect(e.find((x) => x.job.id === 'php')!.noHost).toEqual({ labels: ['os:linux'] })
    expect(e.find((x) => x.job.id === 'ok')!.noHost).toBeNull()
  })

  it('an origin in danger lifts its jobs over another origin\'s (the zone comes from the pool host\'s facts)', () => {
    const hot = host('hot', { facts: { derived: { zone: 'danger' } } as never })
    const calm = host('calm', { facts: { derived: { zone: 'normal' } } as never })
    const e = queueEntries([job('c', { origin: 'calm', created_at: now }), job('h', { origin: 'hot', created_at: now })], now, [calm, hot])
    expect(e.map((x) => x.job.id)).toEqual(['h', 'c'])
  })
})
