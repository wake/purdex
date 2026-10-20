// spa/src/lib/pool/host-counts.test.ts — the number at a host cell's top right and its colour (monitoring tabs MON-3a; M6, M7).
import { describe, it, expect } from 'vitest'
import { hostCountsOf } from './host-counts'
import type { QueueEntry } from './jobs-lanes'
import type { PoolJob } from './jobs-types'

const queued = (id: string, origin: string, over: Partial<QueueEntry> = {}): QueueEntry =>
  ({ job: { id, origin, state: 'queued' }, waitS: 10, long: false, held: false, noHost: null, ...over }) as unknown as QueueEntry
const running = (id: string, origin: string, worker: string | null, state: 'running' | 'claimed' = 'running'): PoolJob =>
  ({ id, origin, state, claim: worker === null ? null : { worker, attempt: 1, since: 0 } }) as unknown as PoolJob

describe('hostCountsOf — the count (M6)', () => {
  it('queued jobs the host sent plus jobs its worker has claimed, whoever sent them', () => {
    // mlab sent 2 queued; air26 is running one mlab sent: mlab 2, air26 1
    const entries = [queued('a', 'mlab'), queued('b', 'mlab')]
    const jobs = [running('c', 'mlab', 'air26')]
    expect(hostCountsOf(entries, jobs)).toEqual({ mlab: { n: 2, tone: 'neutral', queued: 2, running: 0 }, air26: { n: 1, tone: 'neutral', queued: 0, running: 1 } })
  })
  it('claimed (workspace being prepared) counts like running', () => {
    expect(hostCountsOf([], [running('a', 'mlab', 'air26', 'claimed')])).toEqual({ air26: { n: 1, tone: 'neutral', queued: 0, running: 1 } })
  })
  it('a host that sends and runs counts both', () => {
    expect(hostCountsOf([queued('a', 'mlab')], [running('b', 'air26', 'mlab')])).toEqual({ mlab: { n: 2, tone: 'neutral', queued: 1, running: 1 } })
  })
  it('a hold does not count', () => {
    expect(hostCountsOf([queued('a', 'mlab', { held: true })], [])).toEqual({})
  })
  it('a running job with no claim counts for no host', () => {
    expect(hostCountsOf([], [running('a', 'mlab', null)])).toEqual({})
  })
  it('a host with nothing has no entry (0 is not shown)', () => {
    expect(hostCountsOf([], [])).toEqual({})
  })
})

describe('hostCountsOf — the colour (M7)', () => {
  it('a queued job of this host waiting long: warn', () => {
    expect(hostCountsOf([queued('a', 'mlab', { long: true })], []).mlab.tone).toBe('warn')
  })
  it('a queued job of this host no worker host can take: error', () => {
    expect(hostCountsOf([queued('a', 'mlab', { noHost: { labels: ['os:linux'] } })], []).mlab.tone).toBe('error')
  })
  it('red before yellow', () => {
    const entries = [queued('a', 'mlab', { long: true }), queued('b', 'mlab', { noHost: { labels: ['os:linux'] } })]
    expect(hostCountsOf(entries, []).mlab.tone).toBe('error')
  })
  it('the colour looks at what the host SENT: a host running someone else\'s long job stays neutral', () => {
    expect(hostCountsOf([queued('a', 'mlab', { long: true })], [running('b', 'mlab', 'air26')]).air26.tone).toBe('neutral')
  })
  it('a hold, even a long one with no host, does not colour', () => {
    expect(hostCountsOf([queued('a', 'mlab', { held: true, long: true, noHost: { labels: ['x'] } })], [])).toEqual({})
  })
})
