// spa/src/lib/pool/host-charts.ts — what the host page's 監控 tab draws, as data (monitoring tabs spec M41, M42, M43, M22; plan MON-6d-1): six gauges from the latest facts and six mini charts from the host's samples. Pure: facts and
// samples come in, plain objects come out; the gauge and `MiniChart` draw them (6d-2) and the words are the caller's (`t` is passed in). The thresholds are written here, next to the gauges that use them.
import { usageTone, type UsageTone } from '../usage-display'
import type { HostRange, HostSample } from './host-history'
import type { PoolFacts } from './types'

type T = (key: string, params?: Record<string, string | number>) => string

// ---- the gauges (M41-1) -----------------------------------------------------------------------------------------------------------------------------------------

export interface GaugeView { value: string; unit?: string; pct: number; tone: UsageTone }

/** The six gauges, in order, with the key of their label. */
export const GAUGES = [
  { id: 'cpu', labelKey: 'watch.host.s.cpu' },
  { id: 'mem', labelKey: 'watch.host.s.mem' },
  { id: 'level', labelKey: 'watch.host.s.level' },
  { id: 'page_rate', labelKey: 'watch.host.s.page_rate' },
  { id: 'disk', labelKey: 'watch.host.s.disk' },
  { id: 'slots', labelKey: 'watch.host.s.slots' },
] as const

/** The paging gauge is full at the error line (800 pages/s). */
const PAGING_FULL = 800
const pagingTone = (v: number): UsageTone => (v > 800 ? 'danger' : v > 50 ? 'warn' : 'ok')
const diskTone = (v: number): UsageTone => (v >= 95 ? 'danger' : v >= 85 ? 'warn' : 'ok')
/** Levels are 1 / 2 / 4. */
const levelTone = (v: number): UsageTone => (v >= 3 ? 'danger' : v >= 1.75 ? 'warn' : 'ok')
const pressureWord = (v: number, t: T): string => t(`watch.host.pressure.${v >= 3 ? 'critical' : v >= 1.75 ? 'warn' : 'normal'}`)

/** `facts` is null when the host is not ok (M22): every gauge is a dash. A reading the host cannot give (no disk) is a dash too. */
export function gaugesOf(facts: PoolFacts | null, t: T): (GaugeView | null)[] {
  if (!facts) return GAUGES.map(() => null)
  const cpu = 100 - facts.cpu.idle_pct
  const mem = facts.mem.total_mb > 0 ? ((facts.mem.total_mb - facts.mem.available_mb) / facts.mem.total_mb) * 100 : 0
  const level = facts.mem.pressure
  // A platform or a moment that gives no paging figure is "no reading", not a healthy zero: that gauge is a dash.
  const paging = facts.mem.page_out_rate
  const fullest = facts.disk.reduce<number | null>((best, d) => (best === null || d.used_pct > best ? d.used_pct : best), null)
  const w = facts.worker
  const total = w.slots.heavy + w.slots.light
  const used = w.running.heavy + w.running.light
  return [
    { value: cpu.toFixed(0), unit: '%', pct: cpu, tone: usageTone(cpu) },
    { value: mem.toFixed(0), unit: '%', pct: mem, tone: usageTone(mem) },
    level <= 0 ? { value: '—', pct: 0, tone: 'ok' } : { value: pressureWord(level, t), pct: level === 1 ? 33 : level === 2 ? 66 : 100, tone: levelTone(level) },
    paging === null ? null : { value: Math.round(paging).toLocaleString(), unit: t('watch.host.unit.pages'), pct: Math.min(100, (paging / PAGING_FULL) * 100), tone: pagingTone(paging) },
    fullest === null ? null : { value: fullest.toFixed(0), unit: '%', pct: fullest, tone: diskTone(fullest) },
    w.enabled
      ? { value: `${used}/${total}`, unit: t('watch.host.unit.jobs'), pct: total > 0 ? Math.min(100, (used / total) * 100) : 0, tone: total > 0 && used >= total ? 'warn' : 'ok' }
      : { value: t('watch.host.gauge.off'), unit: 'worker', pct: 0, tone: 'ok' },
  ]
}

// ---- the mini charts (M41-2, M42, M43) --------------------------------------------------------------------------------------------------------------------------

/** The six cards of the 監控 tab; a chart of the full set (`host-charts-full.ts`) has an id of its own, so `ChartDef.id` is any string. */
export type ChartId = 'cpu' | 'load' | 'mem' | 'paging' | 'dfree' | 'run'
/** How a chart's legend and ticks write a value (`formatValue` in `host-charts-full.ts`). */
export type ValueFormat = 'pct' | 'gb' | 'mb' | 'int' | 'f1' | 'rate' | 'mbps' | 'iops'
/** The colour of a line: `accent` the main one, `muted` the second, `heavy` and `light` the queue's two kinds (MON-3a's tokens). */
export type ChartColor = 'accent' | 'muted' | 'heavy' | 'light'

export interface ChartSeries {
  id: string
  labelKey: string
  /** A name that is data (a mount point, a device), written as it is, not through `t`. */
  label?: string
  labelParams?: Record<string, string | number>
  /** Aligned with the samples; null: not measured at that point (the line breaks there). */
  values: (number | null)[]
  color: ChartColor
  area?: boolean
  /** Drawn on top of the series before it (memory: available on used). */
  stack?: boolean
  step?: boolean
  dash?: boolean
  faint?: boolean
  /** No point at all in this range: the legend says it is waiting for the series API's fields. */
  pending: boolean
}

export interface ChartLine { value: number; labelKey?: string; labelParams?: Record<string, number>; tone?: 'warn' | 'danger' }

export interface ChartDef {
  id: string
  titleKey: string
  /** How the legend and the ticks write a value. */
  format?: ValueFormat
  /** Replaces the default word for "no point in this range". */
  pendingKey?: string
  /** This platform does not have the figure at all (not "waiting for the series"). */
  noneKey?: string
  noteKey?: string
  /** The unit of the axis. */
  unit: string
  series: ChartSeries[]
  /** Dashed threshold lines. */
  lines: ChartLine[]
  yMin: number
  yMax: number
  /** Every series is pending: the plot says so. */
  pending: boolean
}

export const niceMax = (peak: number, floor = 1): number => {
  const want = Math.max(floor, peak * 1.15)
  const mag = 10 ** Math.floor(Math.log10(want))
  return Math.ceil(want / mag) * mag
}
export const peakOf = (series: readonly (readonly (number | null)[])[]): number => {
  let m = 0
  for (const s of series) for (const v of s) if (v !== null && v > m) m = v
  return m
}
const col = (samples: readonly HostSample[], pick: (s: HostSample) => number | null): (number | null)[] => samples.map(pick)
const mbToGb = (v: number | null): number | null => (v === null ? null : v / 1024)
const lastOf = (samples: readonly HostSample[], pick: (s: HostSample) => number | null): number | null => {
  for (let i = samples.length - 1; i >= 0; i--) { const v = pick(samples[i]); if (v !== null) return v }
  return null
}

/** The `disk_min_gb` line: below it wwd raises its disk alert. */
const DISK_MIN_GB = 10

export function series(id: string, labelKey: string, values: (number | null)[], color: ChartColor, extra: Partial<ChartSeries> = {}): ChartSeries {
  return { id, labelKey, values, color, ...extra, pending: values.every((v) => v === null) }
}
export function chart(id: string, unit: string, ss: ChartSeries[], lines: ChartLine[], yMax: number, titleKey = `watch.host.chart.${id}.title`): ChartDef {
  return { id, titleKey, unit, series: ss, lines, yMin: 0, yMax, pending: ss.every((s) => s.pending) }
}

/**
 * The six cards of the 監控 tab for the samples of `range`. A series with no point at all in this range is `pending`: older daemons' series lack the G-1 fields, and from 1 hour up the series has no slots (spec §3).
 * `facts` is the latest reading, used for the constants a sample may not carry (cores, memory total).
 */
export function miniChartsOf(samples: readonly HostSample[], facts: PoolFacts | null, range: HostRange): ChartDef[] {
  void range // the ranges differ in what the samples carry, not in how they are drawn: a series with no point is pending
  const ncpu = lastOf(samples, (s) => s.ncpu) ?? facts?.cpu.ncpu ?? null
  const totalMb = lastOf(samples, (s) => s.memTotalMb) ?? facts?.mem.total_mb ?? null
  const totalGb = totalMb === null ? 0 : totalMb / 1024

  const cpu = chart('cpu', '%', [series('cpu', 'watch.host.chart.s.cpu', col(samples, (s) => s.cpuPct), 'accent', { area: true })], [{ value: 70, tone: 'warn' }, { value: 90, tone: 'danger' }], 100)

  const load1 = col(samples, (s) => s.load1)
  const load5 = col(samples, (s) => s.load5)
  const load = chart('load', '', [series('load1', 'watch.host.chart.s.load1', load1, 'accent'), series('load5', 'watch.host.chart.s.load5', load5, 'muted', { faint: true })],
    ncpu === null ? [] : [{ value: ncpu, labelKey: 'watch.host.chart.cores', labelParams: { n: ncpu } }], Math.max(ncpu === null ? 1 : ncpu * 1.25, peakOf([load1, load5]) * 1.15, 1))

  const used = col(samples, (s) => mbToGb(s.memUsedMb))
  const avail = col(samples, (s) => mbToGb(s.memAvailMb))
  const mem = chart('mem', 'GB', [series('used', 'watch.host.chart.s.used', used, 'accent', { area: true }), series('avail', 'watch.host.chart.s.avail', avail, 'muted', { area: true, stack: true, faint: true })],
    totalGb > 0 ? [{ value: totalGb * 0.9, tone: 'warn' }] : [], totalGb > 0 ? totalGb : niceMax(peakOf([used, avail])))

  const page = col(samples, (s) => s.pageOut)
  const swap = col(samples, (s) => s.swapOut)
  const paging = chart('paging', '', [series('page', 'watch.host.chart.s.page', page, 'accent'), series('swap', 'watch.host.chart.s.swap', swap, 'muted')], [], niceMax(peakOf([page, swap]), 10))

  const free = col(samples, (s) => s.diskFreeGb)
  const dfree = chart('dfree', 'GB', [series('dfree', 'watch.host.chart.s.dfree', free, 'accent', { area: true })], [{ value: DISK_MIN_GB, labelKey: 'watch.host.chart.disk_min', tone: 'warn' }], niceMax(peakOf([free]), DISK_MIN_GB * 2))

  const runH = col(samples, (s) => s.runHeavy)
  const runL = col(samples, (s) => s.runLight)
  const slotH = col(samples, (s) => s.slotHeavy)
  const slotL = col(samples, (s) => s.slotLight)
  const run = chart('run', '', [
    series('runHeavy', 'watch.host.chart.s.run_heavy', runH, 'heavy', { step: true }),
    series('runLight', 'watch.host.chart.s.run_light', runL, 'light', { step: true }),
    series('slotHeavy', 'watch.host.chart.s.slot_heavy', slotH, 'heavy', { step: true, dash: true }),
    series('slotLight', 'watch.host.chart.s.slot_light', slotL, 'light', { step: true, dash: true }),
  ], [], Math.max(Math.ceil(peakOf([runH, runL, slotH, slotL])) + 1, 2))

  return [cpu, load, mem, paging, dfree, run]
}

/** The big number at the top right of a card: `main` and its small `unit`; `muted` for words (worker off). A point with no value is a dash. */
export interface BigValue { main: string; unit?: string; muted?: boolean }

const f1 = (v: number): string => v.toFixed(1)
const r0 = (v: number): string => v.toFixed(0)

export function bigValue(def: ChartDef, samples: readonly HostSample[], i: number, t: T): BigValue {
  const s = samples[i]
  const dash: BigValue = { main: '—' }
  if (!s) return dash
  switch (def.id) {
    case 'cpu': return s.cpuPct === null ? dash : { main: r0(s.cpuPct), unit: '%' }
    case 'load': return s.load1 === null ? dash : { main: f1(s.load1), unit: s.ncpu === null ? undefined : t('watch.host.chart.big.cores', { n: s.ncpu }) }
    case 'mem': {
      if (s.memUsedMb === null || s.memTotalMb === null || s.memTotalMb <= 0) return dash
      return { main: r0((s.memUsedMb / s.memTotalMb) * 100), unit: `%・${f1(s.memUsedMb / 1024)}／${f1(s.memTotalMb / 1024)} GB` }
    }
    case 'paging': {
      if (s.pageOut === null) return dash
      const swap = s.swapUsedMb === null ? '' : `・${t('watch.host.chart.big.swap', { v: f1(s.swapUsedMb / 1024) })}`
      return { main: Math.round(s.pageOut).toLocaleString(), unit: `${t('watch.host.chart.big.paging')}${swap}` }
    }
    case 'dfree': return s.diskFreeGb === null ? dash : { main: f1(s.diskFreeGb), unit: ' GB' }
    case 'run': {
      if (s.workerOn === false) return { main: t('watch.host.chart.worker_off'), muted: true }
      if (s.runHeavy === null || s.runLight === null) return dash
      const room = (v: number | null): string => (v === null ? '' : `/${v}`)
      return { main: `${t('watch.host.chart.heavy')} ${s.runHeavy}${room(s.slotHeavy)} ${t('watch.host.chart.light')} ${s.runLight}${room(s.slotLight)}` }
    }
    // The charts of the full set show no big number.
    default: return dash
  }
}
