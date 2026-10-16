// spa/src/lib/watch/metrics.ts — the five readings of a host tab's 資源 section (watcher area spec §6.1): their tone rules, warning
// lines, chart scales and the gauge each draws from the latest fast frame. Ported from the approved prototype's METRICS.
import { usageTone, type UsageTone } from '../usage-display'
import type { FleetFast } from '../fleet/types'
import type { HistoryItem } from '../fleet/history'

export type Tone = UsageTone
type T = (key: string) => string

export interface GaugeReading { value: string; unit?: string; pct: number; tone: Tone }

export interface MetricDef {
  k: HistoryItem
  labelKey: string
  shortKey: string
  toneOf: (v: number) => Tone
  /** The dashed line on the chart: only the stretches above it take a colour. */
  warnAt: number
  yMax: (peak: number) => number
  fmt: (v: number, t: T) => string
}

/** The paging gauge is full at the error line (800 pages/s). */
const PAGING_FULL = 800
const pagingTone = (v: number): Tone => (v > 800 ? 'danger' : v > 50 ? 'warn' : 'ok')
const diskTone = (v: number): Tone => (v >= 95 ? 'danger' : v >= 85 ? 'warn' : 'ok')
/** Levels are 1 / 2 / 4; a history average between them is read the same way. */
const levelTone = (v: number): Tone => (v >= 3 ? 'danger' : v >= 1.75 ? 'warn' : 'ok')
const pressureWord = (v: number, t: T): string => t(`watch.host.pressure.${v >= 3 ? 'critical' : v >= 1.75 ? 'warn' : 'normal'}`)
const pct0 = (v: number): string => `${v.toFixed(0)}%`

export const niceMax = (peak: number): number => {
  const want = Math.max(100, peak * 1.15)
  const mag = 10 ** Math.floor(Math.log10(want))
  return Math.ceil(want / mag) * mag
}

export const METRICS: MetricDef[] = [
  { k: 'cpu', labelKey: 'watch.host.m.cpu', shortKey: 'watch.host.s.cpu', toneOf: usageTone, warnAt: 70, yMax: () => 100, fmt: pct0 },
  { k: 'mem', labelKey: 'watch.host.m.mem', shortKey: 'watch.host.s.mem', toneOf: usageTone, warnAt: 70, yMax: () => 100, fmt: pct0 },
  { k: 'level', labelKey: 'watch.host.m.level', shortKey: 'watch.host.s.level', toneOf: levelTone, warnAt: 1.75, yMax: () => 4.5, fmt: pressureWord },
  { k: 'page_rate', labelKey: 'watch.host.m.page_rate', shortKey: 'watch.host.s.page_rate', toneOf: pagingTone, warnAt: 50, yMax: niceMax, fmt: (v) => v.toFixed(0) },
  { k: 'disk', labelKey: 'watch.host.m.disk', shortKey: 'watch.host.s.disk', toneOf: diskTone, warnAt: 85, yMax: () => 100, fmt: pct0 },
]

export function metricDef(k: HistoryItem): MetricDef {
  const d = METRICS.find((m) => m.k === k)
  if (!d) throw new Error(`no metric ${k}`)
  return d
}

/** What the gauge of one reading shows. Pressure level 0 (unknown) has no arc. */
export function gaugeOf(k: HistoryItem, f: FleetFast, t: T): GaugeReading {
  switch (k) {
    case 'level': {
      if (f.level <= 0) return { value: '—', pct: 0, tone: 'ok' }
      return { value: pressureWord(f.level, t), pct: f.level === 1 ? 33 : f.level === 2 ? 66 : 100, tone: levelTone(f.level) }
    }
    case 'page_rate':
      return { value: f.page_rate.toLocaleString(), unit: t('watch.host.unit.pages'), pct: Math.min(100, (f.page_rate / PAGING_FULL) * 100), tone: pagingTone(f.page_rate) }
    case 'mem': return { value: f.mem.toFixed(0), unit: '%', pct: f.mem, tone: usageTone(f.mem) }
    case 'disk': return { value: f.disk.toFixed(0), unit: '%', pct: f.disk, tone: diskTone(f.disk) }
    default: return { value: f.cpu.toFixed(0), unit: '%', pct: f.cpu, tone: usageTone(f.cpu) }
  }
}
