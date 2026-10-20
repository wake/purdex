// The 狀態 tab's full charts as data (monitoring tabs spec M43, M44; plan MON-7c-1): what `fullChartsOf` makes of the samples of a range. Samples are built from literals with 7a's `sampleOfFacts` / `sampleOfPoint`.
import { describe, it, expect } from 'vitest'
import { fullChartsOf, formatValue, type ChartCtx, type ChartGroup } from './host-charts-full'
import { sampleOfFacts, sampleOfPoint, type HostSample } from './host-history'
import { parseFacts, parseSeries } from './parse'
import type { PoolFacts } from './types'
import darwinJson from './__fixtures__/facts_darwin.json'
import linuxJson from './__fixtures__/facts_linux.json'

type Rec = Record<string, unknown>
const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v)) as T
const core = (kind: string | null, idle = 90) => ({ user_pct: 6, system_pct: 4, idle_pct: idle, kind })
/** A record with the host-metrics fields, on top of a stored fixture. */
function rich(base: unknown, over: (raw: Rec) => void = () => {}): PoolFacts {
  const raw = clone(base) as Rec
  Object.assign(raw.cpu as Rec, { load15: 1.5, user_pct: 12, system_pct: 6, cores: [core('p'), core('p'), core('e')] })
  Object.assign(raw.mem as Rec, { pressure: 2, compressor_mb: 2048, decompress_rate: 5000, swap_used_mb: 512, swap_total_mb: 4096, psi: { some10: 3, full10: 1, some60: 2 } })
  Object.assign(raw, { queue: { queued: 2 }, procs_total: 500, boot_at: '2026-10-10T08:00:00Z', self: { cpu_pct: 0.5, rss_mb: 30 }, disk_io: [{ device: 'disk0', read_bps: 2_000_000, write_bps: 1_000_000, read_iops: 100, write_iops: 50 }] })
  over(raw)
  return parseFacts(raw)!
}
const T0 = Date.parse('2026-10-21T01:00:00Z')
const five = (f: PoolFacts, n = 4): HostSample[] => Array.from({ length: n }, (_, i) => sampleOfFacts({ ...f, at: T0 + i * 5000 }))
const seriesSamples = (extra: Rec = {}, n = 4): HostSample[] => parseSeries({
  step: '1m', truncated: false,
  points: Array.from({ length: n }, (_, i) => ({
    at: new Date(T0 + i * 60_000).toISOString(), zone: 'normal', cpu_idle_pct: 80, mem_available_mb: 8000, pressure: 1, page_out_rate: 0, disk_max_used_pct: 50, headroom: { heavy: 1, light: 2 }, ...extra,
  })),
})!.points.map((p) => sampleOfPoint(p, 16000, 10))
const ctx = (over: Partial<ChartCtx> = {}): ChartCtx => ({ platform: 'darwin', rttMs: 12, local: false, sourceAlias: 'mlab', ...over })
const flat = (gs: ChartGroup[]) => gs.flatMap((g) => g.charts)
const get = (gs: ChartGroup[], id: string) => flat(gs).find((c) => c.id === id)!
const MINI = ['cpu', 'load', 'mem', 'paging', 'dfree', 'run']

describe('fullChartsOf: the groups', () => {
  it('six groups in order, with none of the monitoring tab\'s six cards', () => {
    const gs = fullChartsOf(five(rich(darwinJson)), rich(darwinJson), '5m', ctx())
    expect(gs.map((g) => g.id)).toEqual(['cpu', 'mem', 'disk', 'pool', 'proc', 'host'])
    for (const id of MINI) expect(flat(gs).map((c) => c.id), id).not.toContain(id)
  })

  it('macOS draws the compressor and the decompress rate, not PSI; Linux the other way round, and its decompress rate is "not on this platform"', () => {
    const mac = fullChartsOf(five(rich(darwinJson)), rich(darwinJson), '5m', ctx({ platform: 'darwin' }))
    expect(mac.find((g) => g.id === 'mem')!.charts.map((c) => c.id)).toEqual(['pressure', 'comp', 'decomp', 'swap'])
    const lf = rich(linuxJson)
    const lin = fullChartsOf(five(lf), lf, '5m', ctx({ platform: 'linux' }))
    expect(lin.find((g) => g.id === 'mem')!.charts.map((c) => c.id)).toEqual(['pressure', 'psi', 'decomp', 'swap'])
    expect(get(lin, 'decomp')).toMatchObject({ noneKey: 'watch.host.full.decomp_linux_none', pending: true })
    expect(get(mac, 'decomp').noneKey).toBeUndefined()
  })
})

describe('fullChartsOf: pending, and which word', () => {
  it('five minutes, every figure there: nothing is pending', () => {
    const f = rich(darwinJson)
    const gs = fullChartsOf(five(f), f, '5m', ctx())
    expect(flat(gs).filter((c) => c.pending && !c.noneKey).map((c) => c.id)).toEqual([])
  })

  it('six hours: what a series does not carry waits for the series API; every core has its own word (the cores never go in a series)', () => {
    const gs = fullChartsOf(seriesSamples(), null, '6h', ctx({ platform: 'linux' }))
    for (const id of ['psi', 'sr', 'np', 'resmem']) expect(get(gs, id), id).toMatchObject({ pending: true, pendingKey: 'watch.host.full.pending_series' })
    expect(get(gs, 'percore')).toMatchObject({ pending: true, pendingKey: 'watch.host.full.pending_percore' })
  })

  it('six hours: a figure the series does carry but this host did not send is "no data", not "waiting for the series API"', () => {
    const gs = fullChartsOf(seriesSamples(), null, '6h', ctx())
    expect(get(gs, 'load15')).toMatchObject({ pending: true, pendingKey: 'watch.host.full.no_data' })
    expect(get(gs, 'comp')).toMatchObject({ pending: true, pendingKey: 'watch.host.full.no_data' })
  })

  it('six hours with the figures sent: those charts have lines', () => {
    const gs = fullChartsOf(seriesSamples({ load15: 1.2, compressor_mb: 1024, swap_total_mb: 4096, procs_total: 300, self_cpu_pct: 0.4, self_rss_mb: 25 }), null, '6h', ctx())
    for (const id of ['load15', 'comp', 'nproc', 'wwdcpu', 'wwdrss']) expect(get(gs, id).pending, id).toBe(false)
  })

  it('five minutes with a figure missing from every sample: "no data" (the host has none), not the series word', () => {
    const f = rich(darwinJson, (raw) => { delete (raw.cpu as Rec).load15 })
    expect(get(fullChartsOf(five(f), f, '5m', ctx()), 'load15')).toMatchObject({ pending: true, pendingKey: 'watch.host.full.no_data' })
  })
})

describe('fullChartsOf: the lines', () => {
  it('one line per core, coloured by kind: performance accent, efficiency muted, unknown accent', () => {
    const f = rich(darwinJson, (raw) => { (raw.cpu as Rec).cores = [core('p'), core('e'), core(null), core('p')] })
    const pc = get(fullChartsOf(five(f), f, '5m', ctx()), 'percore')
    expect(pc.series.map((s) => s.color)).toEqual(['accent', 'muted', 'accent', 'accent'])
    expect(pc.series).toHaveLength(4)
    expect(pc.series[0].values.every((v) => v === 10)).toBe(true) // 100 − idle
  })

  it('ten cores are ten lines', () => {
    const f = rich(darwinJson, (raw) => { (raw.cpu as Rec).cores = Array.from({ length: 10 }, (_, i) => core(i < 6 ? 'p' : 'e')) })
    expect(get(fullChartsOf(five(f), f, '5m', ctx()), 'percore').series).toHaveLength(10)
  })

  it('the disks are the union of the mounts by first appearance, so a mount that shows up later is a third line and the order holds', () => {
    const f = rich(darwinJson)
    const a = sampleOfFacts({ ...f, at: T0, disk: [{ ...f.disk[0], mount: '/', used_pct: 40 }, { ...f.disk[0], mount: '/v', used_pct: 60 }] })
    const b = sampleOfFacts({ ...f, at: T0 + 5000, disk: [{ ...f.disk[0], mount: '/v', used_pct: 61 }, { ...f.disk[0], mount: '/', used_pct: 41 }, { ...f.disk[0], mount: '/new', used_pct: 5 }] })
    const d = get(fullChartsOf([a, b], f, '5m', ctx()), 'disk')
    expect(d.series.map((s) => s.label)).toEqual(['/', '/v', '/new'])
    expect(d.series[2].values).toEqual([null, 5])
    expect(d.series[0].values).toEqual([40, 41])
  })

  it('swap is capped at the swap the host can hold; with no total it is capped by its own peak', () => {
    const f = rich(darwinJson)
    expect(get(fullChartsOf(five(f), f, '5m', ctx()), 'swap').yMax).toBe(4) // 4096 MB
    const g = rich(darwinJson, (raw) => { delete (raw.mem as Rec).swap_total_mb })
    expect(get(fullChartsOf(five(g), g, '5m', ctx()), 'swap').yMax).not.toBe(4)
  })

  it('the read and write of each device are two lines each; IOPS the same', () => {
    const f = rich(darwinJson)
    const io = get(fullChartsOf(five(f), f, '5m', ctx()), 'diskio')
    expect(io.series.map((s) => s.labelParams?.device)).toEqual(['disk0', 'disk0'])
    expect(io.format).toBe('mbps')
    expect(get(fullChartsOf(five(f), f, '5m', ctx()), 'iops').series).toHaveLength(2)
  })
})

describe('fullChartsOf: the number tiles', () => {
  const tiles = (gs: ChartGroup[]) => gs.find((g) => g.id === 'host')!.tiles
  const find = (gs: ChartGroup[], id: string) => tiles(gs).find((t) => t.id === id)!

  it('uptime is read from the latest record\'s boot time: days, hours and the date', () => {
    const f = rich(darwinJson, (raw) => { raw.at = '2026-10-12T10:30:00Z' })
    const gs = fullChartsOf(five(f), f, '5m', ctx())
    expect(find(gs, 'uptime').text).toMatchObject({ key: 'watch.host.full.uptime', params: { d: 2, h: 2 } })
  })

  it('no record, or no boot time: a dash', () => {
    expect(find(fullChartsOf(five(rich(darwinJson)), null, '5m', ctx()), 'uptime').text).toBeNull()
    const f = rich(darwinJson, (raw) => { delete raw.boot_at })
    expect(find(fullChartsOf(five(f), f, '5m', ctx()), 'uptime').text).toBeNull()
  })

  it('latency: the source reads this host in N ms; the local host is not over a network; none known is a dash', () => {
    const f = rich(darwinJson)
    expect(find(fullChartsOf(five(f), f, '5m', ctx({ rttMs: 12.4, sourceAlias: 'mlab' })), 'latency').text).toEqual({ key: 'watch.host.full.latency', params: { alias: 'mlab', ms: 12 } })
    expect(find(fullChartsOf(five(f), f, '5m', ctx({ local: true, rttMs: 3 })), 'latency').text).toEqual({ key: 'watch.host.full.latency_local' })
    expect(find(fullChartsOf(five(f), f, '5m', ctx({ rttMs: null })), 'latency').text).toBeNull()
  })
})

describe('formatValue', () => {
  it.each([
    ['pct', 42.6, '43%'], ['gb', 3.14159, '3.1 GB'], ['mb', 1234.5, '1235 MB'], ['int', 7.4, '7'], ['f1', 2.25, '2.3'], ['rate', 123456, '123,456'], ['mbps', 2_500_000, '2.5 MB/s'], ['iops', 99.6, '100'],
  ] as const)('%s of %s is %s', (format, v, want) => {
    expect(formatValue(format, v)).toBe(want)
  })
})
