// spa/src/lib/pool/queue-strip.test.ts — what the sidebar's queue strip draws (monitoring tabs MON-3a; M1–M5, M25).
import { describe, it, expect } from 'vitest'
import { queueStripOf, QUEUE_STRIP_TOP } from './queue-strip'
import type { QueueEntry } from './jobs-lanes'

const entry = (id: string, over: { slot?: 'heavy' | 'light'; held?: boolean } = {}): QueueEntry =>
  ({ job: { id, origin: 'mlab', ...(over.slot ? { slot: over.slot } : {}) }, waitS: 0, long: false, held: over.held ?? false, noHost: null }) as unknown as QueueEntry
const many = (n: number, slot?: 'heavy' | 'light') => Array.from({ length: n }, (_, i) => entry(`j${i}`, { slot }))

describe('queueStripOf — the segments (M1–M4)', () => {
  it('3 queued: 3 lit, the other segments are the component\'s to grey', () => {
    const s = queueStripOf(many(3), 10, true)
    expect(s.segments).toEqual(['heavy', 'heavy', 'heavy'])
    expect(s.overflow).toBe(0)
    expect(s.counts).toEqual({ heavy: 3, light: 0 })
  })
  it('14 queued with 10 segments: 10 lit and +4', () => {
    const s = queueStripOf(many(14), 10, true)
    expect(s.segments).toHaveLength(10)
    expect(s.overflow).toBe(4)
    expect(s.counts.heavy).toBe(14)
  })
  it('exactly as many as segments: no overflow', () => {
    expect(queueStripOf(many(10), 10, true).overflow).toBe(0)
    expect(queueStripOf(many(11), 10, true).overflow).toBe(1)
  })
  it('a hold does not count: not in the segments, the overflow, the counts or the list (M3)', () => {
    const s = queueStripOf([entry('a'), entry('b', { held: true }), entry('c')], 10, true)
    expect(s.segments).toHaveLength(2)
    expect(s.counts).toEqual({ heavy: 2, light: 0 })
    expect(s.top.map((e) => e.job.id)).toEqual(['a', 'c'])
  })
  it('a job without a slot is heavy; light is light, in the display order', () => {
    const s = queueStripOf([entry('a', { slot: 'light' }), entry('b'), entry('c', { slot: 'heavy' })], 10, true)
    expect(s.segments).toEqual(['light', 'heavy', 'heavy'])
    expect(s.counts).toEqual({ heavy: 2, light: 1 })
  })
  it('nothing queued: no segments, nothing over, zero counts (M4)', () => {
    expect(queueStripOf([], 10, true)).toMatchObject({ readable: true, segments: [], overflow: 0, counts: { heavy: 0, light: 0 }, top: [] })
  })
  it.each([6, 8, 12])('%s segments', (nseg) => {
    const s = queueStripOf(many(14), nseg, true)
    expect(s.segments).toHaveLength(nseg)
    expect(s.overflow).toBe(14 - nseg)
  })
})

describe('queueStripOf — the list (M5)', () => {
  it('at most the first 10', () => {
    const s = queueStripOf(many(14), 10, true)
    expect(s.top).toHaveLength(QUEUE_STRIP_TOP)
    expect(s.top[0].job.id).toBe('j0')
  })
  it('counts are over all of them, not only the lit segments or the list', () => {
    const s = queueStripOf([...many(9), ...many(7, 'light')].map((e, i) => ({ ...e, job: { ...e.job, id: `k${i}` } })) as QueueEntry[], 10, true)
    expect(s.counts).toEqual({ heavy: 9, light: 7 })
  })
})

describe('queueStripOf — a pool nobody can read (M25)', () => {
  it('draws nothing, whatever the jobs store still holds: 14 old queued jobs are no segments, no +N, counts 0/0, no list', () => {
    const s = queueStripOf(many(14), 10, false)
    expect(s).toEqual({ readable: false, segments: [], overflow: 0, counts: { heavy: 0, light: 0 }, top: [] })
  })
})
