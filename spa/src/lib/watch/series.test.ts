import { describe, it, expect } from 'vitest'
import { stretches, toPoints, collectedMinutes, summarize, RANGE_MS } from './series'
import { usageTone } from '../usage-display'

const P = (v: number, x = 0, p = v) => ({ x, v, p })

describe('stretches', () => {
  it('splits a run where a segment crosses a line; a segment takes the tone of its higher end; joints are shared', () => {
    const pts = [P(50, 0), P(60, 1), P(80, 2), P(95, 3), P(40, 4)]
    const st = stretches(pts, usageTone)
    expect(st.map((s) => [s.tone, s.pts.map((p) => p.v)])).toEqual([
      ['ok', [50, 60]], ['warn', [60, 80]], ['danger', [80, 95, 40]],
    ])
  })
  it('a gap (NaN) breaks the run', () => {
    const st = stretches([P(10, 0), P(Number.NaN, 1), P(20, 2), P(30, 3)], usageTone)
    expect(st.map((s) => s.pts.map((p) => p.v))).toEqual([[10], [20, 30]])
  })
  it('an empty series has no stretches', () => {
    expect(stretches([], usageTone)).toEqual([])
  })
})

describe('toPoints', () => {
  it('maps t onto [now - range, now]', () => {
    const now = 1_000_000_000
    const pts = toPoints([{ t: now - RANGE_MS['5m'], avg: 1, max: 2 }, { t: now - RANGE_MS['5m'] / 2, avg: 3, max: 4 }, { t: now, avg: 5, max: 6 }], now, '5m')
    expect(pts.map((p) => p.x)).toEqual([0, 0.5, 1])
    expect(pts[1]).toMatchObject({ v: 3, p: 4 })
  })
})

describe('collectedMinutes / summarize', () => {
  it('collected minutes come from the first point', () => {
    expect(collectedMinutes([{ t: 1_000_000 - 150_000, avg: 1, max: 1 }], 1_000_000)).toBe(2.5)
    expect(collectedMinutes([], 1_000_000)).toBe(0)
    expect(collectedMinutes([{ t: 1_000_000 - 240_000, avg: 1, max: 1 }, { t: 1_000_000 - 30_000, avg: 1, max: 1 }], 1_000_000)).toBe(4) // from the FIRST point, not the last
  })
  it('the average is of the averages and the peak is the largest max', () => {
    expect(summarize([{ t: 1, avg: 10, max: 30 }, { t: 2, avg: 20, max: 90 }])).toEqual({ avg: 15, peak: 90 })
  })
  it('an empty series is NaN', () => {
    const s = summarize([])
    expect(Number.isNaN(s.avg) && Number.isNaN(s.peak)).toBe(true)
  })
})
