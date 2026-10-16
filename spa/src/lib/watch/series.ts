// spa/src/lib/watch/series.ts — the pure side of the host tab's charts: history points → unit-square points, tone stretches, the
// collected span and the range's average / peak. `stretches` is moved from the approved prototype (prototypes/load-control/ui.tsx).
import type { HistoryPoint, HistoryRange } from '../fleet/history'
import type { Tone } from './metrics'

export const RANGES: HistoryRange[] = ['5m', '1h', '6h', '24h']
export const RANGE_MS: Record<HistoryRange, number> = { '5m': 5 * 60_000, '1h': 3600_000, '6h': 6 * 3600_000, '24h': 24 * 3600_000 }

/** x in 0..1 across the range (1 = now), v = the average, p = the peak. */
export interface SPt { x: number; v: number; p: number }
export interface Stretch { tone: Tone; pts: SPt[] }

export function toPoints(points: HistoryPoint[], now: number, range: HistoryRange): SPt[] {
  const span = RANGE_MS[range]
  return points.map((q) => ({ x: (q.t - (now - span)) / span, v: q.avg, p: q.max }))
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

export function summarize(points: HistoryPoint[]): { avg: number; peak: number } {
  if (points.length === 0) return { avg: Number.NaN, peak: Number.NaN }
  let sum = 0
  let peak = -Infinity
  for (const q of points) { sum += q.avg; if (q.max > peak) peak = q.max }
  return { avg: sum / points.length, peak }
}
