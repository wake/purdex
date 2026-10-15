import { describe, it, expect, beforeEach, vi } from 'vitest'
import { FleetHistory } from './history'
import { fast } from './fixtures'

const S = 1000
let h: FleetHistory
beforeEach(() => { h = new FleetHistory() })

const feed = (id: string, from: number, seconds: number, cpu: (i: number) => number) => {
  for (let i = 0; i < seconds; i++) h.record(id, fast({ at: from + i * S, cpu: cpu(i) }))
}

describe('FleetHistory', () => {
  it('keeps 1 s points for the last 10 minutes and serves the 5 minute range from them', () => {
    feed('a', 0, 900, (i) => i) // 15 minutes of 1 s points
    const now = 899 * S
    const pts = h.series('a', 'cpu', '5m', now)
    expect(pts).toHaveLength(301) // t in [now-300s, now]
    expect(pts[0]).toEqual({ t: 599 * S, avg: 599, max: 599 })
    expect(pts[pts.length - 1].avg).toBe(899)
  })

  it('drops raw points older than 10 minutes', () => {
    feed('a', 0, 900, () => 1)
    expect(h.rawCount('a')).toBe(601)
  })

  it('folds into 10 s buckets with avg and max, and serves 1 h from them', () => {
    feed('a', 0, 20, (i) => (i === 3 ? 90 : 10)) // two buckets: [0,10s) and [10s,20s)
    const pts = h.series('a', 'cpu', '1h', 19 * S)
    expect(pts).toHaveLength(2)
    expect(pts[0].t).toBe(0)
    expect(pts[0].avg).toBeCloseTo(18, 5)
    expect(pts[0].max).toBe(90)
    expect(pts[1]).toEqual({ t: 10 * S, avg: 10, max: 10 })
  })

  it('keeps buckets for 24 hours and no longer', () => {
    const day = 24 * 3600
    feed('a', 0, day + 3600, () => 5)
    const now = (day + 3599) * S
    const pts = h.series('a', 'cpu', '24h', now)
    expect(pts[0].t).toBeGreaterThanOrEqual(now - day * S)
    expect(h.bucketCount('a')).toBeLessThanOrEqual(day / 10 + 1)
  })

  it('ranges 6h and 24h slice the buckets by time', () => {
    feed('a', 0, 8 * 3600, () => 1)
    const now = (8 * 3600 - 1) * S
    expect(h.series('a', 'cpu', '6h', now)[0].t).toBeGreaterThanOrEqual(now - 6 * 3600 * S)
    expect(h.series('a', 'cpu', '24h', now)[0].t).toBe(0)
  })

  it('ignores a duplicate or older point of the same host', () => {
    h.record('a', fast({ at: 5 * S, cpu: 10 }))
    h.record('a', fast({ at: 5 * S, cpu: 99 }))
    h.record('a', fast({ at: 4 * S, cpu: 99 }))
    expect(h.series('a', 'cpu', '5m', 5 * S)).toEqual([{ t: 5 * S, avg: 10, max: 10 }])
  })

  it('keeps every item and every host apart; forget drops one host', () => {
    h.record('a', fast({ at: S, cpu: 1, mem: 2, load: 3 }))
    h.record('b', fast({ at: S, cpu: 9 }))
    expect(h.series('a', 'mem', '5m', S)[0].avg).toBe(2)
    expect(h.series('a', 'load', '5m', S)[0].avg).toBe(3)
    expect(h.series('b', 'cpu', '5m', S)[0].avg).toBe(9)
    h.forget('a')
    expect(h.series('a', 'cpu', '5m', S)).toEqual([])
    expect(h.series('b', 'cpu', '5m', S)).toHaveLength(1)
  })

  it('lives in memory only: nothing is written to localStorage or sessionStorage', () => {
    const set = vi.spyOn(Storage.prototype, 'setItem')
    feed('a', 0, 30, () => 1)
    h.series('a', 'cpu', '1h', 29 * S)
    expect(set).not.toHaveBeenCalled()
    set.mockRestore()
  })
})
