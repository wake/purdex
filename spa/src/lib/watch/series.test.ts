import { describe, it, expect } from 'vitest'
import { stretches, toPoints, collectedMinutes, summarize, decimate, RANGE_MS } from './series'
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
    expect(pts.filter((p) => !Number.isNaN(p.v)).map((p) => p.x)).toEqual([0, 0.5, 1]) // the holes between them are NaN breaks
    expect(pts[2]).toMatchObject({ v: 3, p: 4 })
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

describe('gaps', () => {
  const NOW = 10_000_000_000
  it('a 20-minute hole in a 1 h series breaks the line: two runs, no segment across the hole', () => {
    const pts = []
    for (let m = 60; m >= 0; m--) if (m < 25 || m > 45) for (let k = 0; k < 6; k++) pts.push({ t: NOW - m * 60_000 + k * 10_000, avg: 10, max: 10 })
    const st = stretches(toPoints(pts.filter((q) => q.t <= NOW), NOW, '1h'), usageTone)
    expect(st.length).toBe(2)
    for (const s of st) for (let i = 1; i < s.pts.length; i++) expect(s.pts[i].x - s.pts[i - 1].x).toBeLessThan(0.01)
  })
  it('raw 1 s points: a 3 s step breaks, a 2 s step does not', () => {
    const mk = (gap: number) => toPoints([{ t: NOW - 10_000, avg: 1, max: 1 }, { t: NOW - 10_000 + gap, avg: 1, max: 1 }], NOW, '5m')
    expect(stretches(mk(3000), usageTone).length).toBe(2)
    expect(stretches(mk(2000), usageTone).length).toBe(1)
  })
})

describe('unknown pressure', () => {
  const NOW = 10_000_000_000
  const lv = [{ t: NOW - 3000, avg: 0, max: 0 }, { t: NOW - 2000, avg: 0, max: 0 }, { t: NOW - 1000, avg: 2, max: 2 }]
  it('level 0 is a gap, not a real 0', () => {
    const pts = toPoints(lv, NOW, '5m', { unknownZero: true })
    expect(pts.slice(0, 2).every((p) => Number.isNaN(p.v))).toBe(true)
    expect(stretches(pts, usageTone).flatMap((s) => s.pts).length).toBe(1)
  })
  it('summarize leaves it out; all-unknown is NaN', () => {
    expect(summarize(lv, true)).toEqual({ avg: 2, peak: 2 })
    const s = summarize(lv.slice(0, 2), true)
    expect(Number.isNaN(s.avg) && Number.isNaN(s.peak)).toBe(true)
  })
})

describe('decimate', () => {
  it('24 h of 10 s buckets (8640 points) comes down to ~400 and the band keeps the true peak', () => {
    const NOW = 10_000_000_000
    const hist = Array.from({ length: 8640 }, (_, i) => ({ t: NOW - (8639 - i) * 10_000, avg: 20, max: i === 4321 ? 97 : 25 }))
    const out = decimate(toPoints(hist, NOW, '24h'))
    expect(out.length).toBeLessThanOrEqual(402)
    expect(Math.max(...out.map((p) => p.p))).toBe(97)
  })
  it('a small series is untouched', () => {
    const pts = [P(1, 0.1), P(2, 0.2)]
    expect(decimate(pts)).toEqual(pts)
  })
})
