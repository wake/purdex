// spa/src/components/watch/MetricChart.tsx — one chart row of the host tab's 資源 and the shared time axis under the last row (ported
// from the approved prototype's useChart / MetricRow / Axis, and `SeriesPaths` from prototypes/load-control/ui.tsx). The line is the
// average, a faint band the peak, a dashed warning line; only the stretches above it take the warning or error colour.
import type { HistoryPoint, HistoryRange } from '../../lib/fleet/history'
import type { MetricDef, Tone } from '../../lib/watch/metrics'
import { RANGE_MS, collectedMinutes, stretches, summarize, toPoints, type SPt } from '../../lib/watch/series'
import { useI18nStore } from '../../stores/useI18nStore'

export const ROW = 'grid grid-cols-[8rem_minmax(0,1fr)_9.5rem] items-center gap-x-4'
const COVER_SLACK_MS = 15_000
const CH = 36
const W = 400
const LINE_CLASS: Record<Tone, string> = { ok: 'text-text-secondary', warn: 'text-status-warning', danger: 'text-status-error' }

/** Average line + peak band, one path per tone stretch. X/Y map a point to the viewBox. */
function SeriesPaths({ pts, toneOf, X, Y }: { pts: SPt[]; toneOf: (v: number) => Tone; X: (x: number) => number; Y: (v: number) => number }) {
  const st = stretches(pts, toneOf)
  return (
    <>
      {st.filter((s) => s.pts.length > 1).map((s, i) => {
        const top = s.pts.map((p, j) => `${j ? 'L' : 'M'}${X(p.x).toFixed(1)} ${Y(p.p).toFixed(1)}`).join('')
        const bot = [...s.pts].reverse().map((p) => `L${X(p.x).toFixed(1)} ${Y(p.v).toFixed(1)}`).join('')
        return <path key={`b${i}`} data-testid="chart-band" d={`${top}${bot}Z`} className={LINE_CLASS[s.tone]} fill="currentColor" fillOpacity={0.1} stroke="none" />
      })}
      {st.map((s, i) => (
        <path
          key={`l${i}`} data-testid="chart-line" data-tone={s.tone} className={LINE_CLASS[s.tone]} fill="none" stroke="currentColor" strokeWidth={1.5}
          strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke"
          d={s.pts.length > 1 ? s.pts.map((p, j) => `${j ? 'L' : 'M'}${X(p.x).toFixed(1)} ${Y(p.v).toFixed(1)}`).join('') : `M${X(s.pts[0].x).toFixed(1)} ${Y(s.pts[0].v).toFixed(1)}h0.1`}
        />
      ))}
    </>
  )
}

export function MetricRow({ def, points, now, range, offline }: { def: MetricDef; points: HistoryPoint[]; now: number; range: HistoryRange; offline: boolean }) {
  const t = useI18nStore((s) => s.t)
  const { avg, peak } = summarize(points)
  const yMax = def.yMax(Number.isNaN(peak) ? 0 : peak)
  const Y = (v: number) => CH - 1 - Math.min(1, Math.max(0, v / yMax)) * (CH - 4)
  const f = (v: number) => (Number.isNaN(v) ? '—' : def.fmt(v, t))
  return (
    <div className={`${ROW} py-2`} data-testid={`metric-${def.k}`}>
      <span className="text-xs text-text-secondary">{t(def.labelKey)}</span>
      <div data-testid="chart-area" className={offline ? 'opacity-50' : ''}>
        <svg viewBox={`0 0 ${W} ${CH}`} preserveAspectRatio="none" className="w-full block" style={{ height: CH }} aria-hidden="true">
          {[0.5, CH - 0.5].map((y) => <line key={y} x1="0" x2={W} y1={y} y2={y} stroke="var(--border-subtle)" vectorEffect="non-scaling-stroke" />)}
          {def.warnAt < yMax && <line x1="0" x2={W} y1={Y(def.warnAt)} y2={Y(def.warnAt)} stroke="var(--border-default)" strokeDasharray="3 3" vectorEffect="non-scaling-stroke" />}
          <SeriesPaths pts={toPoints(points, now, range)} toneOf={def.toneOf} X={(x) => x * W} Y={Y} />
        </svg>
      </div>
      <span className="text-[10px] text-text-muted tabular-nums text-right whitespace-nowrap">{t('watch.host.avg_peak', { avg: f(avg), peak: f(peak) })}</span>
    </div>
  )
}

/** The shared axis under the last row: 現在 … −n 分／小時, and how much the App has collected while that is short of the range. */
export function TimeAxis({ points, now, range }: { points: HistoryPoint[]; now: number; range: HistoryRange }) {
  const t = useI18nStore((s) => s.t)
  const win = RANGE_MS[range] / 60_000
  const collected = collectedMinutes(points, now)
  // `series` cuts the history at the range start, so a full history's first point is never earlier than that: only a first point more
  // than a bucket (10 s) and a bit later than the start means the App has not collected the whole range yet.
  const partial = collected * 60_000 < RANGE_MS[range] - COVER_SLACK_MS
  const label = (min: number) => (min === 0 ? t('watch.host.axis_now') : min < 60 ? t('watch.host.axis_min', { n: min }) : t('watch.host.axis_hour', { n: +(min / 60).toFixed(1) }))
  return (
    <div className={`${ROW} pt-0.5`}>
      <span />
      <div data-testid="axis" className="relative h-4">
        {[1, 0.75, 0.5, 0.25, 0].map((fr, i) => (
          <span key={fr} className={`absolute top-0 text-[10px] text-text-muted tabular-nums whitespace-nowrap ${i === 0 ? '' : i === 4 ? '-translate-x-full' : '-translate-x-1/2'}`} style={{ left: `${(1 - fr) * 100}%` }}>
            {label(Math.round(win * fr))}
          </span>
        ))}
      </div>
      <span data-testid="axis-collecting" className="text-[10px] text-text-muted text-right">{partial ? t('watch.host.collecting', { n: Math.floor(collected) }) : ''}</span>
    </div>
  )
}
