import { describe, it, expect } from 'vitest'
import { gaugesOf, miniChartsOf, bigValue, GAUGES, type ChartDef } from './host-charts'
import type { HostSample } from './host-history'
import type { PoolFacts } from './types'
import { parseFacts } from './parse'
import darwinJson from './__fixtures__/facts_darwin.json'

const t = (k: string, p?: Record<string, string | number>) => (p ? `${k}:${JSON.stringify(p)}` : k)
const base = parseFacts(darwinJson)!
const facts = (over: { cpuIdle?: number; memAvail?: number; level?: number; pageOut?: number | null; disks?: PoolFacts['disk']; worker?: Partial<PoolFacts['worker']> } = {}): PoolFacts => ({
  ...base,
  cpu: { ...base.cpu, idle_pct: over.cpuIdle ?? 90, ncpu: 8 },
  mem: { ...base.mem, total_mb: 16000, available_mb: over.memAvail ?? 12000, pressure: over.level ?? 1, page_out_rate: over.pageOut === undefined ? 10 : over.pageOut },
  disk: over.disks ?? [{ mount: '/', used_pct: 50, free_gb: 120 } as never],
  worker: { ...base.worker, enabled: true, slots: { heavy: 2, light: 4 }, running: { heavy: 1, light: 0 }, ...over.worker },
})
const g = (f: PoolFacts | null) => gaugesOf(f, t)

describe('gaugesOf (M41-1, M22)', () => {
  it('six gauges in order: CPU, memory, pressure, paging, disk, slots', () => {
    expect(GAUGES.map((x) => x.id)).toEqual(['cpu', 'mem', 'level', 'page_rate', 'disk', 'slots'])
    expect(g(facts())).toHaveLength(6)
  })
  it('CPU is 100 − idle, memory the used share; both turn at 70 and 90', () => {
    const [cpu, mem] = g(facts({ cpuIdle: 25, memAvail: 1600 }))!
    expect(cpu).toMatchObject({ value: '75', unit: '%', pct: 75, tone: 'warn' })
    expect(mem).toMatchObject({ value: '90', unit: '%', pct: 90, tone: 'danger' })
    expect(g(facts({ cpuIdle: 31 }))[0]!.tone).toBe('ok') // 69
    expect(g(facts({ cpuIdle: 30 }))[0]!.tone).toBe('warn') // 70
    expect(g(facts({ cpuIdle: 10 }))[0]!.tone).toBe('danger') // 90
  })
  it.each([[1, 33, 'ok', 'watch.host.pressure.normal'], [2, 66, 'warn', 'watch.host.pressure.warn'], [4, 100, 'danger', 'watch.host.pressure.critical']] as const)('pressure level %s: %s of the arc, %s', (level, pct, tone, word) => {
    expect(g(facts({ level }))[2]).toMatchObject({ value: word, pct, tone })
  })
  it('pressure level 0 has no arc: a dash', () => {
    expect(g(facts({ level: 0 }))[2]).toEqual({ value: '—', pct: 0, tone: 'ok' })
  })
  it('paging: full at 800 pages/s, yellow above 50, red above 800', () => {
    expect(g(facts({ pageOut: 50 }))[3]).toMatchObject({ pct: 6.25, tone: 'ok' })
    expect(g(facts({ pageOut: 51 }))[3]!.tone).toBe('warn')
    expect(g(facts({ pageOut: 800 }))[3]).toMatchObject({ pct: 100, tone: 'warn' })
    expect(g(facts({ pageOut: 801 }))[3]).toMatchObject({ pct: 100, tone: 'danger' })
    expect(g(facts({ pageOut: 1500 }))[3]!.pct).toBe(100)
  })
  it('paging that the platform does not report is no reading (a dash), not a healthy zero', () => {
    expect(g(facts({ pageOut: null }))[3]).toBeNull()
    expect(g(facts({ pageOut: 0 }))[3]).toMatchObject({ value: '0', pct: 0, tone: 'ok' }) // a measured zero is still a zero
  })
  it('disk is the fullest one: yellow from 85, red from 95', () => {
    const disks = [{ mount: '/', used_pct: 40, free_gb: 300 }, { mount: '/data', used_pct: 85, free_gb: 20 }] as never
    expect(g(facts({ disks }))[4]).toMatchObject({ value: '85', pct: 85, tone: 'warn' })
    expect(g(facts({ disks: [{ mount: '/', used_pct: 95, free_gb: 2 }] as never }))[4]!.tone).toBe('danger')
    expect(g(facts({ disks: [{ mount: '/', used_pct: 84, free_gb: 30 }] as never }))[4]!.tone).toBe('ok')
  })
  it('no disk at all is a dash', () => {
    expect(g(facts({ disks: [] }))[4]).toBeNull()
  })
  it('slots in use: u/t and the share; full is yellow', () => {
    expect(g(facts({ worker: { running: { heavy: 1, light: 2 } } }))[5]).toEqual({ value: '3/6', unit: 'watch.host.unit.jobs', pct: 50, tone: 'ok' })
    expect(g(facts({ worker: { running: { heavy: 2, light: 4 } } }))[5]).toMatchObject({ value: '6/6', pct: 100, tone: 'warn' })
  })
  it('worker off: 關 with the unit worker, no arc, not yellow', () => {
    expect(g(facts({ worker: { enabled: false } }))[5]).toEqual({ value: 'watch.host.gauge.off', unit: 'worker', pct: 0, tone: 'ok' })
  })
  it('no facts (a state that is not ok): six dashes', () => {
    expect(g(null)).toEqual([null, null, null, null, null, null])
  })
})

// ---- the charts ------------------------------------------------------------------------------------------------------------------------------------------------

const NOW = Date.parse('2026-10-20T12:00:00Z')
const sample = (i: number, over: Partial<HostSample> = {}): HostSample => ({
  at: NOW - (4 - i) * 60_000, cpuPct: 20 + i, load1: 1 + i / 10, load5: 1, ncpu: 8, memUsedMb: 8192 + i * 100, memAvailMb: 8192 - i * 100, memTotalMb: 16384,
  pageOut: i, swapOut: i * 2, swapUsedMb: 512, diskUsedPct: 50, diskFreeGb: 100 - i, runHeavy: 1, runLight: i % 2, slotHeavy: 2, slotLight: 4, workerOn: true, zone: 'normal', ...over,
})
const full = [0, 1, 2, 3, 4].map((i) => sample(i))
const get = (defs: ChartDef[], id: string) => defs.find((d) => d.id === id)!
const charts = (samples: HostSample[], range: '5m' | '6h' = '5m') => miniChartsOf(samples, facts(), range)

describe('miniChartsOf (M41-2, M42, M43)', () => {
  it('six charts in order: CPU, load, memory, paging and swap, disk free, slots', () => {
    expect(charts(full).map((d) => d.id)).toEqual(['cpu', 'load', 'mem', 'paging', 'dfree', 'run'])
  })
  it('5 minutes, everything present: nothing is pending', () => {
    for (const d of charts(full)) {
      expect(d.pending, d.id).toBe(false)
      expect(d.series.every((s) => !s.pending), d.id).toBe(true)
    }
  })
  it('CPU: an area, dashed lines at 70 and 90, 0 to 100', () => {
    const d = get(charts(full), 'cpu')
    expect(d.series).toHaveLength(1)
    expect(d.series[0]).toMatchObject({ area: true, color: 'accent', values: [20, 21, 22, 23, 24] })
    expect(d.lines.map((l) => l.value)).toEqual([70, 90])
    expect(d.yMax).toBe(100)
  })
  it('load: load1 and a faint load5, and a line at the core count labelled with it', () => {
    const d = get(charts(full), 'load')
    expect(d.series.map((s) => [s.id, s.faint === true])).toEqual([['load1', false], ['load5', true]])
    expect(d.lines).toHaveLength(1)
    expect(d.lines[0]).toMatchObject({ value: 8, labelKey: 'watch.host.chart.cores', labelParams: { n: 8 } })
    expect(d.yMax).toBeGreaterThanOrEqual(8)
  })
  it('memory: used and available stacked up to the total in GB, with a line at 90 %', () => {
    const d = get(charts(full), 'mem')
    expect(d.series.map((s) => [s.id, s.stack === true])).toEqual([['used', false], ['avail', true]])
    expect(d.series[0].values[0]).toBeCloseTo(8, 5)
    expect(d.series[1].values[0]).toBeCloseTo(8, 5)
    expect(d.yMax).toBeCloseTo(16, 5)
    expect(d.lines[0].value).toBeCloseTo(14.4, 5)
  })
  it('paging and swap: two lines', () => {
    const d = get(charts(full), 'paging')
    expect(d.series.map((s) => s.id)).toEqual(['page', 'swap'])
    expect(d.series[0].values).toEqual([0, 1, 2, 3, 4])
  })
  it('disk free: GB, and the disk_min_gb line at 10', () => {
    const d = get(charts(full), 'dfree')
    expect(d.series[0].values).toEqual([100, 99, 98, 97, 96])
    expect(d.lines).toEqual([{ value: 10, labelKey: 'watch.host.chart.disk_min', tone: 'warn' }])
  })
  it('slots: the running ones as steps, the room as dashed steps', () => {
    const d = get(charts(full), 'run')
    expect(d.series.map((s) => [s.id, s.step === true, s.dash === true, s.color])).toEqual([
      ['runHeavy', true, false, 'heavy'], ['runLight', true, false, 'light'], ['slotHeavy', true, true, 'heavy'], ['slotLight', true, true, 'light'],
    ])
    expect(d.yMax).toBeGreaterThanOrEqual(4)
  })
  it('6 hours (series points of G-1): the slot room is pending, the rest are there', () => {
    const points = full.map((s) => ({ ...s, slotHeavy: null, slotLight: null, workerOn: null }))
    const defs = charts(points, '6h')
    const run = get(defs, 'run')
    expect(run.series.find((s) => s.id === 'slotHeavy')!.pending).toBe(true)
    expect(run.series.find((s) => s.id === 'slotLight')!.pending).toBe(true)
    expect(run.series.find((s) => s.id === 'runHeavy')!.pending).toBe(false)
    expect(run.pending).toBe(false)
    expect(defs.filter((d) => d.pending)).toEqual([])
  })
  it('an older daemon\'s series (the 8 original fields): load, swap and running are pending', () => {
    const old = full.map((s) => ({ ...s, load1: null, load5: null, swapOut: null, swapUsedMb: null, runHeavy: null, runLight: null, slotHeavy: null, slotLight: null, workerOn: null }))
    const defs = charts(old, '6h')
    expect(get(defs, 'load').pending).toBe(true)
    expect(get(defs, 'run').pending).toBe(true)
    expect(get(defs, 'paging').series.find((s) => s.id === 'swap')!.pending).toBe(true)
    expect(get(defs, 'paging').pending).toBe(false) // the paging line is there
    expect(get(defs, 'cpu').pending).toBe(false)
  })
  it('one point missing is not pending: only a series with no point at all is', () => {
    const some = full.map((s, i) => (i === 2 ? { ...s, load1: null } : s))
    expect(get(charts(some), 'load').series[0].pending).toBe(false)
    expect(get(charts(full.map((s) => ({ ...s, load1: null }))), 'load').series[0].pending).toBe(true)
  })
  it('no samples at all: every chart is pending', () => {
    expect(charts([]).every((d) => d.pending)).toBe(true)
  })
})

describe('bigValue (the number at the top right of a card)', () => {
  const defs = charts(full)
  const v = (id: string, i: number, s = full) => bigValue(get(defs, id), s, i, t)
  it('cpu: the percent', () => {
    expect(v('cpu', 4)).toEqual({ main: '24', unit: '%' })
  })
  it('load: load1 over the cores', () => {
    expect(v('load', 4)).toEqual({ main: '1.4', unit: 'watch.host.chart.big.cores:{"n":8}' })
  })
  it('memory: the share and the GB over the total', () => {
    expect(v('mem', 0)).toEqual({ main: '50', unit: '%・8.0／16.0 GB' })
  })
  it('paging: pages per second, and the swap in use', () => {
    expect(v('paging', 4)).toEqual({ main: '4', unit: 'watch.host.chart.big.paging・watch.host.chart.big.swap:{"v":"0.5"}' })
  })
  it('paging without a swap figure: pages per second alone', () => {
    const s = full.map((x) => ({ ...x, swapUsedMb: null }))
    expect(bigValue(get(charts(s), 'paging'), s, 4, t)).toEqual({ main: '4', unit: 'watch.host.chart.big.paging' })
  })
  it('disk free: GB', () => {
    expect(v('dfree', 4)).toEqual({ main: '96.0', unit: ' GB' })
  })
  it('slots: running over the room, heavy and light', () => {
    const r = v('run', 1)
    expect(r.main).toBe('watch.host.chart.heavy 1/2 watch.host.chart.light 1/4')
  })
  it('slots while the worker is off: the words, muted', () => {
    const off = full.map((x) => ({ ...x, workerOn: false }))
    expect(bigValue(get(charts(off), 'run'), off, 4, t)).toEqual({ main: 'watch.host.chart.worker_off', muted: true })
  })
  it('a point with no value is a dash', () => {
    const s = full.map((x, i) => (i === 3 ? { ...x, cpuPct: null } : x))
    expect(bigValue(get(charts(s), 'cpu'), s, 3, t)).toEqual({ main: '—' })
  })
})
