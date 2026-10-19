// spa/src/lib/watch/series.ts — the pure side of the host tab's charts: history points → unit-square points, tone stretches, the
// collected span and the range's average / peak. `stretches` is moved from the approved prototype (prototypes/load-control/ui.tsx).
import type { HistoryPoint, HistoryRange } from '../fleet/history'
import type { Tone } from './metrics'

export const RANGES: HistoryRange[] = ['5m', '1h', '6h', '24h']
export const RANGE_MS: Record<HistoryRange, number> = { '5m': 5 * 60_000, '1h': 3600_000, '6h': 6 * 3600_000, '24h': 24 * 3600_000 }

/** x in 0..1 across the range (1 = now), v = the average, p = the peak. */
export interface SPt { x: number; v: number; p: number }
export interface Stretch { tone: Tone; pts: SPt[] }

/** The expected step between two points of a range: 1 s raw points for 5 分, 10 s buckets for the rest. */
export const stepMs = (range: HistoryRange): number => (range === '5m' ? 1000 : 10_000)
/**
 * How far apart two points of a range are expected to be at most, for telling a hole from a slow stretch. The 5 minute range is the 1 s readings; 1 h and
 * longer hold the App's own 10 s buckets and, before them, the daemon's 1 minute series, so there a neighbouring step can be up to a minute.
 */
export const holeStepMs = (range: HistoryRange): number => (range === '5m' ? 1000 : 60_000)
/** A step wider than this many expected steps is a hole in the history (offline host, stalled fleet), not a slow stretch. */
const GAP_STEPS = 2.5
/** The charts are 400 viewBox units wide; more points than that cannot be told apart. */
export const MAX_POINTS = 400

export interface ToPointsOpts {
  /** Pressure: a level of 0 means unknown, which is a gap, not a real 0. */
  unknownZero?: boolean
}

/** History points onto the unit square; a hole (or an unknown reading) becomes a NaN point, which breaks the line. */
export function toPoints(points: HistoryPoint[], now: number, range: HistoryRange, opts: ToPointsOpts = {}): SPt[] {
  const span = RANGE_MS[range]
  const from = now - span
  const cap = holeStepMs(range)
  // In a long range the expected step is the one around the pair, not one for the whole range: a 60 s outage in a run of 10 s buckets is a hole,
  // the same 60 s between two minute points (or where the two kinds meet) is not. A neighbouring step is capped, so a hole next to a pair does not widen its limit.
  const near = (i: number): number => Math.min(cap, Math.max(stepMs(range), points[i].t - points[i - 1].t))
  const out: SPt[] = []
  for (let i = 0; i < points.length; i++) {
    const q = points[i]
    if (i > 0) {
      const prev = points[i - 1].t
      const sides = [i > 1 ? near(i - 1) : null, i + 1 < points.length ? near(i + 1) : null].filter((v): v is number => v !== null)
      const expected = range === '5m' ? cap : sides.length > 0 ? Math.max(...sides) : cap
      if (q.t - prev > GAP_STEPS * expected) out.push({ x: (prev + (q.t - prev) / 2 - from) / span, v: Number.NaN, p: Number.NaN })
    }
    const unknown = opts.unknownZero === true && q.max <= 0
    out.push({ x: (q.t - from) / span, v: unknown ? Number.NaN : q.avg, p: unknown ? Number.NaN : q.max })
  }
  return out
}

/** At most `n` points (one per x unit): the line keeps the mean of the merged points, the band the largest peak. NaN points stay as breaks. */
export function decimate(pts: SPt[], n: number = MAX_POINTS): SPt[] {
  if (pts.length <= n) return pts
  const out: SPt[] = []
  let bin = -1
  let g: { x: number; v: number; p: number; c: number } | null = null
  const flush = () => { if (g) out.push({ x: g.x / g.c, v: g.v / g.c, p: g.p }); g = null; bin = -1 }
  for (const q of pts) {
    if (Number.isNaN(q.v)) { flush(); out.push(q); continue }
    const b = Math.min(n - 1, Math.max(0, Math.floor(q.x * n)))
    if (b !== bin) { flush(); bin = b }
    g = g ? { x: g.x + q.x, v: g.v + q.v, p: Math.max(g.p, q.p), c: g.c + 1 } : { x: q.x, v: q.v, p: q.p, c: 1 }
  }
  flush()
  return out
}

/** Splits each unbroken run into stretches of one tone (a segment takes the tone of its higher end); stretches share their joint. */
export function stretches(pts: SPt[], toneOf: (v: number) => Tone): Stretch[] {
  const out: Stretch[] = []
  let run: SPt[] = []
  const flush = () => {
    if (run.length === 1) out.push({ tone: toneOf(run[0].v), pts: run })
    for (let i = 0; i + 1 < run.length; i++) {
      const t = toneOf(Math.max(run[i].v, run[i + 1].v))
      const last = out[out.length - 1]
      if (last && last.tone === t && last.pts[last.pts.length - 1] === run[i]) last.pts.push(run[i + 1])
      else out.push({ tone: t, pts: [run[i], run[i + 1]] })
    }
    run = []
  }
  for (const p of pts) {
    if (Number.isNaN(p.v)) flush()
    else run.push(p)
  }
  flush()
  return out
}

/** Minutes of history the App holds (the history has no helper): from the first point to now. */
export function collectedMinutes(points: HistoryPoint[], now: number): number {
  return points.length === 0 ? 0 : Math.max(0, (now - points[0].t) / 60_000)
}

export function summarize(points: HistoryPoint[], unknownZero = false): { avg: number; peak: number } {
  let sum = 0
  let n = 0
  let peak = -Infinity
  for (const q of points) {
    if (unknownZero && q.max <= 0) continue
    sum += q.avg
    n++
    if (q.max > peak) peak = q.max
  }
  return n === 0 ? { avg: Number.NaN, peak: Number.NaN } : { avg: sum / n, peak }
}
