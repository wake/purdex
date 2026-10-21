// spa/src/components/watch/host/MiniChart.tsx — one card of the host page's 監控 tab (monitoring tabs spec M42, M22; plan MON-6d-2): a title, the big number, a plot about 90 px tall, the legend. It draws the `ChartDef` that
// `lib/pool/host-charts.ts` made from the samples; it holds no state. The plot is an SVG with a viewBox that stretches (`preserveAspectRatio="none"`): a point is placed by its TIME in [now − range, now] and its value between the
// chart's min and max. A null value breaks a line, and so do two points more than 3 steps apart (a hole in the record). The hovered point is the parent's (`hoverIndex`, `onHover`): the cards of one tab share it (MON-6e).
import { useId, useRef } from 'react'
import { Info } from '@phosphor-icons/react'
import type { ChartColor, ChartDef, ChartSeries } from '../../../lib/pool/host-charts'
import { bigValue } from '../../../lib/pool/host-charts'
import { formatValue } from '../../../lib/pool/host-charts-full'
import { RANGE_MS, readsFiveSecond, type HostRange, type HostSample } from '../../../lib/pool/host-history'
import { useI18nStore } from '../../../stores/useI18nStore'

const W = 600
const H = 90
/** A line breaks when two neighbours are more than this many steps apart. */
const BREAK_STEPS = 3
const COLOR: Record<ChartColor, string> = { accent: 'text-accent', muted: 'text-text-muted', heavy: 'text-queue-heavy', light: 'text-queue-light' }
const DEAD_COLOR = { warning: 'text-status-warning-text', error: 'text-status-error-text', muted: 'text-text-muted' } as const

export interface MiniChartProps {
  def: ChartDef
  samples: readonly HostSample[]
  range: HostRange
  now: number
  /** The hovered sample (the same index for every card of the tab), or null. */
  hoverIndex: number | null
  onHover: (i: number | null) => void
  /** The host has gone: the slanted lines run from `fromAt` to the right edge (M22). */
  dead: { fromAt: number; tone: 'warning' | 'error' | 'muted' } | null
  /** `full` (the 狀態 tab's charts): the unit and a note by the title, no big number, a value in each legend entry, five times on the axis. Default `mini`. */
  variant?: 'mini' | 'full'
}

interface Pt { x: number; y: number; base: number }

const two = (n: number): string => String(n).padStart(2, '0')
const hm = (ms: number): string => { const d = new Date(ms); return `${two(d.getHours())}:${two(d.getMinutes())}` }
const hms = (ms: number): string => { const d = new Date(ms); return `${two(d.getHours())}:${two(d.getMinutes())}:${two(d.getSeconds())}` }
const tick = (v: number): string => (Math.abs(v) >= 100 || Number.isInteger(v) ? String(Math.round(v)) : v.toFixed(1))
const f = (n: number): string => n.toFixed(1)

/** The value of a series at each sample, with the layers under a stacked one added; null when it or a layer under it has no value. */
function totalsOf(defSeries: readonly ChartSeries[], index: number): (number | null)[] {
  const s = defSeries[index]
  if (!s.stack || index === 0) return s.values
  const under = totalsOf(defSeries, index - 1)
  return s.values.map((v, i) => (v === null || under[i] === null ? null : v + (under[i] as number)))
}

export function MiniChart({ def, samples, range, now, hoverIndex, onHover, dead, variant = 'mini' }: MiniChartProps) {
  const t = useI18nStore((s) => s.t)
  const full = variant === 'full'
  const plotRef = useRef<HTMLDivElement>(null)
  const hatchId = useId()
  const R = RANGE_MS[range]
  const stepMs = readsFiveSecond(range) ? 5000 : 60_000
  const span = def.yMax - def.yMin > 0 ? def.yMax - def.yMin : 1
  const X = (at: number): number => ((at - (now - R)) / R) * W
  const Y = (v: number): number => H - ((Math.min(def.yMax, Math.max(def.yMin, v)) - def.yMin) / span) * H

  // A reading taken after the host went (M22) is not drawn, not hovered and not the big number.
  const live = (s: HostSample): boolean => !dead || s.at <= dead.fromAt
  // Each series as runs of points: broken by a null, and by a hole of more than 3 steps.
  const runsOf = (index: number): Pt[][] => {
    const totals = totalsOf(def.series, index)
    const under = def.series[index].stack && index > 0 ? totalsOf(def.series, index - 1) : null
    const runs: Pt[][] = []
    let cur: Pt[] = []
    let prevAt = 0
    samples.forEach((smp, i) => {
      const v = totals[i]
      if (!live(smp)) return
      if (v === null || v === undefined) { if (cur.length) runs.push(cur); cur = []; return }
      if (cur.length && smp.at - prevAt > BREAK_STEPS * stepMs) { runs.push(cur); cur = [] }
      cur.push({ x: X(smp.at), y: Y(v), base: under && under[i] !== null ? Y(under[i] as number) : Y(0) })
      prevAt = smp.at
    })
    if (cur.length) runs.push(cur)
    return runs
  }
  const lineD = (runs: Pt[][], step: boolean): string => runs.map((r) => {
    if (r.length === 1) return `M${f(r[0].x)} ${f(r[0].y)}h0.1`
    return step
      ? `M${f(r[0].x)} ${f(r[0].y)}${r.slice(1).map((p) => `H${f(p.x)}V${f(p.y)}`).join('')}`
      : r.map((p, i) => `${i ? 'L' : 'M'}${f(p.x)} ${f(p.y)}`).join('')
  }).join('')
  const areaD = (runs: Pt[][]): string => runs.filter((r) => r.length > 1).map((r) => `${r.map((p, i) => `${i ? 'L' : 'M'}${f(p.x)} ${f(p.y)}`).join('')}${[...r].reverse().map((p) => `L${f(p.x)} ${f(p.base)}`).join('')}Z`).join('')

  let lastIdx = -1
  samples.forEach((s, i) => { if (live(s)) lastIdx = i })
  const hoverable = hoverIndex !== null && hoverIndex >= 0 && hoverIndex < samples.length && live(samples[hoverIndex])
  const hovered = hoverable ? samples[hoverIndex] : undefined
  const at = hoverable ? hoverIndex : lastIdx
  const big = full ? null : bigValue(def, samples, at, t)
  // The full legend writes each series' own value at that point (a stacked layer's own, not the sum).
  const legendValue = (s: ChartSeries): string | null => {
    const v = at >= 0 ? s.values[at] : null
    return v === null || v === undefined ? null : formatValue(def.format ?? 'f1', v)
  }

  const move = (clientX: number): void => {
    const el = plotRef.current
    if (!el || samples.length === 0) { onHover(null); return }
    const r = el.getBoundingClientRect()
    if (r.width <= 0) return
    const at = now - R + ((clientX - r.left) / r.width) * R
    let best = -1
    samples.forEach((s, i) => { if (live(s) && (best < 0 || Math.abs(s.at - at) < Math.abs(samples[best].at - at))) best = i })
    onHover(best < 0 ? null : best)
  }

  const deadFrom = dead ? Math.min(100, Math.max(0, ((dead.fromAt - (now - R)) / R) * 100)) : null
  const mid = (def.yMax + def.yMin) / 2

  return (
    <div data-testid="mini-chart" data-chart={def.id} className="rounded-lg border border-border-default bg-surface-secondary px-3 py-2">
      <div className="flex items-baseline gap-2">
        <span className="text-xs text-text-secondary">{t(def.titleKey)}</span>
        {full && def.unit !== '' && <span data-testid="mini-unit" className="text-[10px] text-text-muted">{def.unit}</span>}
        {full && def.noteKey && <span data-testid="mini-note" title={t(def.noteKey)} className="inline-flex text-text-muted"><Info size={11} /></span>}
        <span className="flex-1" />
        {big && (
          <b data-testid="mini-big" className={`text-sm font-semibold tabular-nums ${big.muted ? 'font-normal text-text-muted' : 'text-text-primary'}`}>
            {big.main}{big.unit && <i className="ml-0.5 text-[10px] font-normal not-italic text-text-muted">{big.unit}</i>}
          </b>
        )}
      </div>
      <div className="relative mt-1 flex gap-1">
        <div className="flex w-7 shrink-0 flex-col justify-between text-right text-[9px] tabular-nums text-text-muted" style={{ height: H }}>
          {[def.yMax, mid, def.yMin].map((v, i) => <span key={i} data-testid={`mini-ytick-${i}`}>{tick(v)}</span>)}
        </div>
        <div ref={plotRef} data-testid="mini-plot" className="relative min-w-0 flex-1" style={{ height: H }} onMouseMove={(e) => move(e.clientX)} onMouseLeave={() => onHover(null)}>
          <svg width="100%" height={H} viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="block" aria-hidden="true">
            <defs>
              <pattern id={hatchId} width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(45)" className={dead ? DEAD_COLOR[dead.tone] : undefined}><line x1="0" y1="0" x2="0" y2="6" stroke="currentColor" strokeWidth="1.5" /></pattern>
            </defs>
            {[0, 0.5, 1].map((p) => <line key={p} x1="0" x2={W} y1={p * H} y2={p * H} stroke="var(--border-default)" strokeWidth="1" vectorEffect="non-scaling-stroke" opacity="0.5" />)}
            {!def.pending && def.lines.map((l, i) => (
              <line key={i} data-testid={`mini-threshold-${i}`} data-y={Y(l.value)} x1="0" x2={W} y1={Y(l.value)} y2={Y(l.value)} stroke="currentColor" strokeWidth="1" strokeDasharray="4 3" vectorEffect="non-scaling-stroke"
                className={l.tone === 'danger' ? 'text-status-error-text' : l.tone === 'warn' ? 'text-status-warning-text' : 'text-text-muted'} opacity="0.7" />
            ))}
            {!def.pending && def.series.map((s, idx) => {
              if (s.pending) return null
              const runs = runsOf(idx)
              return (
                <g key={s.id} className={COLOR[s.color]} opacity={s.faint ? 0.45 : 1}>
                  {s.area && <path data-testid={`mini-area-${s.id}`} d={areaD(runs)} fill="currentColor" fillOpacity={0.15} stroke="none" />}
                  <path data-testid={`mini-path-${s.id}`} d={lineD(runs, s.step === true)} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" strokeDasharray={s.dash ? '4 3' : undefined} />
                </g>
              )
            })}
            {dead && deadFrom !== null && deadFrom < 100 && (
              <rect data-testid="mini-dead" data-tone={dead.tone} data-from-pct={deadFrom} x={(deadFrom / 100) * W} y="0" width={W - (deadFrom / 100) * W} height={H} fill={`url(#${hatchId})`} opacity="0.35" className={DEAD_COLOR[dead.tone]} />
            )}
            {hovered && <line data-testid="mini-hover-line" x1={X(hovered.at)} x2={X(hovered.at)} y1="0" y2={H} stroke="var(--text-secondary)" strokeWidth="1" vectorEffect="non-scaling-stroke" />}
          </svg>
          {!def.pending && def.lines.map((l, i) => l.labelKey && (
            <span key={i} data-testid={`mini-threshold-label-${i}`} className="pointer-events-none absolute right-0 text-[9px] text-text-muted" style={{ top: Math.max(0, Y(l.value) - 11) }}>{t(l.labelKey, l.labelParams)}</span>
          ))}
          {hovered && <span data-testid="mini-hover-time" className="pointer-events-none absolute top-0 rounded bg-surface-elevated px-1 text-[9px] tabular-nums text-text-primary" style={{ left: `${Math.min(88, Math.max(0, (X(hovered.at) / W) * 100 - 6))}%` }}>{hms(hovered.at)}</span>}
          {def.pending && <div data-testid="mini-pending" className="absolute inset-0 flex items-center justify-center px-3 text-center text-[11px] leading-snug text-text-muted">{t(def.noneKey ?? def.pendingKey ?? 'watch.host.chart.pending_all')}</div>}
        </div>
      </div>
      <div className="mt-1 flex justify-between pl-8 text-[9px] tabular-nums text-text-muted">
        {(full ? [0, 1, 2, 3, 4].map((i) => now - R + (R * i) / 4) : [now - R, now - R / 2, now]).map((tickAt, i) => <span key={i} data-testid={`mini-xtick-${i}`}>{hm(tickAt)}</span>)}
      </div>
      <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 pl-8 text-[10px] text-text-secondary">
        {def.series.map((s) => (
          <span key={s.id} data-testid={`mini-legend-${s.id}`} data-pending={String(s.pending)} className="inline-flex items-center gap-1">
            <span className={`inline-block h-1.5 w-1.5 rounded-full bg-current ${COLOR[s.color]} ${s.pending ? 'opacity-30' : ''}`} />
            {s.label ?? t(s.labelKey, s.labelParams)}
            {full && legendValue(s) !== null && <b className="font-medium tabular-nums text-text-primary">{legendValue(s)}</b>}
            {s.pending && <span className="text-text-muted">・{t('watch.host.chart.pending_series')}</span>}
          </span>
        ))}
      </div>
    </div>
  )
}
