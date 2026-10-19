import { describe, it, expect, beforeEach, vi } from 'vitest'
import { FleetHistory } from './history'
import { fast } from './__fixtures__/builders'

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

  it('a reading dated far in the future does not shut out the normal ones that follow', () => {
    const now = Date.now()
    h.record('a', fast({ at: now - 2 * S, cpu: 1 }))
    h.record('a', fast({ at: now + 3600_000, cpu: 2 })) // the daemon's clock is an hour ahead
    h.record('a', fast({ at: now + S, cpu: 4 })) // clamped to the receive time, so the next normal one is newer
    expect(h.series('a', 'cpu', '5m', now + 2 * S).map((p) => p.avg)).toEqual([1, 2, 4])
  })

  it('the same future reading re-sent each second is recorded once', () => {
    vi.useFakeTimers()
    vi.setSystemTime(10_000_000)
    const at = Date.now() + 3600_000
    h.record('a', fast({ at, cpu: 7 }))
    vi.setSystemTime(10_001_000)
    h.record('a', fast({ at, cpu: 7 }))
    vi.setSystemTime(10_002_000)
    h.record('a', fast({ at, cpu: 7 }))
    vi.useRealTimers()
    expect(h.rawCount('a')).toBe(1)
  })

  it('skew straddling the tolerance leaves no gap: an unclamped future reading, then a clamped one', () => {
    const now = Date.now()
    h.record('a', fast({ at: now + 4_900, cpu: 1 }))
    h.record('a', fast({ at: now + 5_900, cpu: 2 })) // now past the tolerance: clamped, but still after the last
    expect(h.rawCount('a')).toBe(2)
  })
})

describe('FleetHistory.seedBuckets (1 h and longer filled from the daemon\'s 1 minute series)', () => {
  const T0 = 2_000_000 // a multiple of the 10 s bucket
  const sec = (n: number) => T0 + n * 1000
  const at = (n: number, items: Record<string, number> = { cpu: 10 }) => ({ at: sec(n), ...items })

  it('series points become buckets before and among what the App collected, in time order', () => {
    const h = new FleetHistory()
    h.record('h', fast({ at: sec(1000), cpu: 50 }))
    h.seedBuckets('h', [at(0, { cpu: 10 }), at(60, { cpu: 20 })], sec(1001))
    expect(h.series('h', 'cpu', '1h', sec(1001)).map((p) => [p.t, p.avg, p.max])).toEqual([[sec(0), 10, 10], [sec(60), 20, 20], [sec(1000), 50, 50]])
  })

  it('a bucket the App already has keeps its own value', () => {
    const h = new FleetHistory()
    h.record('h', fast({ at: sec(1000), cpu: 50 }))
    h.seedBuckets('h', [at(1003, { cpu: 99 })], sec(1010))
    expect(h.series('h', 'cpu', '1h', sec(1010)).map((p) => p.avg)).toEqual([50])
  })

  it('a point dated ahead of this App\'s clock is not placed', () => {
    const h = new FleetHistory()
    h.seedBuckets('h', [at(0), at(100_000)], sec(10))
    expect(h.series('h', 'cpu', '24h', sec(10)).map((p) => p.t)).toEqual([sec(0)])
    expect(h.bucketCount('h')).toBe(1)
  })

  it('an item a point does not carry is a gap for that item only', () => {
    const h = new FleetHistory()
    h.seedBuckets('h', [{ at: sec(0), cpu: 10 }, { at: sec(60), cpu: 20, mem: 40 }], sec(70))
    expect(h.series('h', 'cpu', '1h', sec(70)).map((p) => p.avg)).toEqual([10, 20])
    expect(h.series('h', 'mem', '1h', sec(70)).map((p) => [p.t, p.avg])).toEqual([[sec(60), 40]])
  })

  it('the 5 minute track is not touched', () => {
    const h = new FleetHistory()
    h.seedBuckets('h', [at(0), at(60)], sec(70))
    expect(h.rawCount('h')).toBe(0)
  })

  it('what is older than the 24 h of buckets is dropped', () => {
    const h = new FleetHistory()
    h.seedBuckets('h', [at(0), at(25 * 3600)], sec(25 * 3600 + 10))
    expect(h.series('h', 'cpu', '24h', sec(25 * 3600 + 10)).map((p) => p.t)).toEqual([sec(25 * 3600)])
  })

  it('a live reading older than a seeded bucket still lands in time order', () => {
    const h = new FleetHistory()
    h.seedBuckets('h', [at(100, { cpu: 20 })], sec(130))
    h.record('h', fast({ at: sec(50), cpu: 10 }))
    h.record('h', fast({ at: sec(120), cpu: 30 }))
    expect(h.series('h', 'cpu', '1h', sec(130)).map((p) => [p.t, p.avg])).toEqual([[sec(50), 10], [sec(100), 20], [sec(120), 30]])
  })
})

describe('FleetHistory.seedRaw (the 5 min range filled from the daemon\'s history)', () => {
  const at = (n: number) => 1_000_000 + n * 1000
  const seed = (n: number, cpu: number) => ({ at: at(n), cpu, mem: 1, level: 1, page_rate: 0, disk: 1 })

  it('older readings are put before the ones already there, in time order', () => {
    const h = new FleetHistory()
    h.record('h', fast({ at: at(10), cpu: 50 }))
    h.seedRaw('h', [seed(0, 10), seed(5, 20)])
    expect(h.series('h', 'cpu', '5m', at(11)).map((p) => [p.t, p.avg])).toEqual([[at(0), 10], [at(5), 20], [at(10), 50]])
  })

  it('a reading at the same time as one already there is kept once, the one already there wins', () => {
    const h = new FleetHistory()
    h.record('h', fast({ at: at(10), cpu: 50 }))
    h.seedRaw('h', [seed(10, 99)])
    expect(h.series('h', 'cpu', '5m', at(11)).map((p) => p.avg)).toEqual([50])
  })

  it('readings older than the raw window are dropped, and the 1 h+ buckets are not touched', () => {
    const h = new FleetHistory()
    h.record('h', fast({ at: at(900), cpu: 50 }))
    h.seedRaw('h', [seed(0, 10)])
    expect(h.rawCount('h')).toBe(1)
    expect(h.bucketCount('h')).toBe(1)
  })

  it('after a seed, a live reading at the same time as the newest seeded one is not added twice', () => {
    const h = new FleetHistory()
    h.seedRaw('h', [seed(0, 10), seed(5, 20)], at(6))
    h.record('h', fast({ at: at(5), cpu: 77 }))
    expect(h.series('h', 'cpu', '5m', at(6)).map((p) => [p.t, p.avg])).toEqual([[at(0), 10], [at(5), 20]])
  })

  it('after a seed, a live reading older than the newest seeded one is ignored, a newer one follows in order', () => {
    const h = new FleetHistory()
    h.seedRaw('h', [seed(0, 10), seed(5, 20)], at(6))
    h.record('h', fast({ at: at(3), cpu: 77 }))
    h.record('h', fast({ at: at(7), cpu: 30 }))
    expect(h.series('h', 'cpu', '5m', at(8)).map((p) => [p.t, p.avg])).toEqual([[at(0), 10], [at(5), 20], [at(7), 30]])
  })

  it('a reading dated ahead of this App\'s clock is not placed: it must not wipe the history nor empty the chart', () => {
    const h = new FleetHistory()
    h.record('h', fast({ at: at(10), cpu: 50 }))
    h.seedRaw('h', [seed(0, 10), seed(5, 20), seed(700, 99)], at(11))
    expect(h.series('h', 'cpu', '5m', at(11)).map((p) => [p.t, p.avg])).toEqual([[at(0), 10], [at(5), 20], [at(10), 50]])
  })

  it('a reading a few seconds ahead is within the tolerance and is kept', () => {
    const h = new FleetHistory()
    h.seedRaw('h', [seed(0, 10), seed(13, 20)], at(10))
    expect(h.rawCount('h')).toBe(2)
  })

  it('seeding a host with no history yet starts one', () => {
    const h = new FleetHistory()
    h.seedRaw('h', [seed(0, 10), seed(5, 20)])
    expect(h.series('h', 'cpu', '5m', at(6)).map((p) => p.avg)).toEqual([10, 20])
  })
})
