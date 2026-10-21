// spa/src/lib/pool/host-charts-full.ts — the 狀態 tab's full charts as data (monitoring tabs spec M43, M44; plan MON-7c-1): the host's samples of a range made into six groups of charts and two number tiles, after the design's `chartDefs`
// and `GROUPS`, without the six cards of the 監控 tab. Pure, from the pool only: the samples (7a's `HostSample.detail`) and the latest facts come in, plain objects come out, and every word is an i18n key or a value the caller writes.
import type { ChartColor, ChartDef, ChartLine, ChartSeries, ValueFormat } from './host-charts'
import { chart, niceMax, peakOf, series } from './host-charts'
import { readsFiveSecond, type HostDetail, type HostRange, type HostSample } from './host-history'
import type { PoolFacts } from './types'

export type ChartGroupId = 'cpu' | 'mem' | 'disk' | 'pool' | 'proc' | 'host'
/** A number card: `text` is a word with its parameters for the component to write; null: no figure (a dash). */
export interface TileDef { id: 'uptime' | 'latency'; titleKey: string; text: { key: string; params?: Record<string, string | number> } | null }
export interface ChartGroup { id: ChartGroupId; charts: ChartDef[]; tiles: TileDef[] }

/** What the charts need to know about the host beyond its samples. */
export interface ChartCtx {
  platform: string
  /** The round trip from the pool's source daemon to this host (ms), null when unknown. */
  rttMs: number | null
  /** This host is the one the answering daemon reports as its own: there is no network between them. */
  local: boolean
  sourceAlias: string | null
}

const K = 'watch.host.full'
/** The lines past the first four go round the same four colours: no new token (88 10-20). */
const PALETTE: ChartColor[] = ['accent', 'muted', 'heavy', 'light']

const dcol = (samples: readonly HostSample[], pick: (d: HostDetail) => number | null): (number | null)[] => samples.map((s) => (s.detail ? pick(s.detail) : null))
const lastNonNull = <T,>(samples: readonly HostSample[], pick: (d: HostDetail) => T | null): T | null => {
  for (let i = samples.length - 1; i >= 0; i--) { const d = samples[i].detail; const v = d ? pick(d) : null; if (v !== null) return v }
  return null
}

export function formatValue(format: ValueFormat, v: number): string {
  switch (format) {
    case 'pct': return `${Math.round(v)}%`
    case 'gb': return `${v.toFixed(1)} GB`
    case 'mb': return `${Math.round(v)} MB`
    case 'int': return String(Math.round(v))
    case 'f1': return v.toFixed(1)
    case 'rate': return Math.round(v).toLocaleString('en-US')
    case 'mbps': return `${(v / 1e6).toFixed(1)} MB/s`
    case 'iops': return String(Math.round(v))
  }
}

/**
 * The charts of `range`. A series with no point at all in the range is pending, and the word says why: a figure the 1 minute series does not carry (the per-core shares, PSI, the sessions room, the listed processes, the residents)
 * waits for the series API from 1 hour up; every other empty chart is "this host has no data". The per-core chart has a word of its own from 1 hour up: the cores are never in a series.
 */
export function fullChartsOf(samples: readonly HostSample[], facts: PoolFacts | null, range: HostRange, ctx: ChartCtx): ChartGroup[] {
  const long = !readsFiveSecond(range)
  const linux = ctx.platform === 'linux'
  const ncpu = samples.reduce<number | null>((n, s) => s.ncpu ?? n, null) ?? facts?.cpu.ncpu ?? null

  /** A chart of the set: `carried` is whether the 1 minute series has the figure. */
  const mk = (id: string, unit: string, ss: ChartSeries[], lines: ChartLine[], yMax: number, format: ValueFormat, carried: boolean, extra: Partial<ChartDef> = {}): ChartDef => ({
    ...chart(id, unit, ss, lines, yMax, `${K}.${id}.title`), format, pendingKey: long && !carried ? `${K}.pending_series` : `${K}.no_data`, ...extra,
  })

  // ---- CPU ----
  const coreCount = samples.reduce((n, s) => Math.max(n, s.detail?.cores?.length ?? 0), 0)
  const perCore = Array.from({ length: coreCount }, (_, i): ChartSeries => {
    const kind = lastNonNull(samples, (d) => d.cores?.[i]?.kind ?? null)
    const values = samples.map((s) => { const idle = s.detail?.cores?.[i]?.idle; return idle === undefined || idle === null ? null : 100 - idle })
    return series(`core${i}`, kind === 'e' ? `${K}.s.core_e` : kind === 'p' ? `${K}.s.core_p` : `${K}.s.core`, values, kind === 'e' ? 'muted' : 'accent', { labelParams: { n: i + 1 } })
  })
  const user = dcol(samples, (d) => d.userPct)
  const system = dcol(samples, (d) => d.systemPct)
  const load15 = dcol(samples, (d) => d.load15)
  const cpu: ChartDef[] = [
    mk('percore', '%', perCore, [], 100, 'pct', false, { pendingKey: long ? `${K}.pending_percore` : `${K}.no_data` }),
    mk('cpusplit', '%', [series('user', `${K}.s.user`, user, 'accent', { area: true }), series('system', `${K}.s.system`, system, 'muted', { area: true, stack: true })], [], 100, 'pct', true),
    mk('load15', '', [series('load15', `${K}.s.load15`, load15, 'accent')], ncpu === null ? [] : [{ value: ncpu, labelKey: 'watch.host.chart.cores', labelParams: { n: ncpu } }], Math.max(ncpu === null ? 1 : ncpu * 1.25, peakOf([load15]) * 1.15, 1), 'f1', true),
  ]

  // ---- memory ----
  const level = dcol(samples, (d) => d.level)
  const compGb = dcol(samples, (d) => (d.compressorMb === null ? null : d.compressorMb / 1024))
  const decomp = dcol(samples, (d) => d.decompressRate)
  const swapGb = samples.map((s) => (s.swapUsedMb === null ? null : s.swapUsedMb / 1024)) // the swap in use is a field of the sample, not of its detail
  // The cap is the newest sample's total (an older one may be from before a change); with none there, the chart is capped by its own peak.
  const swapTotal = samples.length > 0 ? (samples[samples.length - 1].detail?.swapTotalMb ?? null) : null
  const psi = (pick: (p: NonNullable<HostDetail['psi']>) => number) => dcol(samples, (d) => (d.psi ? pick(d.psi) : null))
  const mem: ChartDef[] = [
    mk('pressure', '', [series('level', `${K}.s.level`, level, 'accent', { step: true })], [{ value: 2, tone: 'warn' }, { value: 4, tone: 'danger' }], 4.4, 'int', true),
    linux
      ? mk('psi', '%', [series('some10', `${K}.s.some10`, psi((p) => p.some10), 'accent'), series('full10', `${K}.s.full10`, psi((p) => p.full10), 'heavy'), series('some60', `${K}.s.some60`, psi((p) => p.some60), 'muted')],
        [{ value: 10, tone: 'warn' }, { value: 25, tone: 'danger' }], 30, 'f1', false)
      : mk('comp', 'GB', [series('comp', `${K}.s.comp`, compGb, 'accent', { area: true })], [{ value: 4, tone: 'warn' }], niceMax(Math.max(5, peakOf([compGb]) * 1.1)), 'gb', true),
    linux
      ? mk('decomp', '', [], [], 1, 'rate', true, { noneKey: `${K}.decomp_linux_none` })
      : mk('decomp', '', [series('decomp', `${K}.s.decomp`, decomp, 'accent')], [{ value: 80000, tone: 'warn' }], niceMax(Math.max(100000, peakOf([decomp]) * 1.1)), 'rate', true),
    mk('swap', 'GB', [series('swap', `${K}.s.swap`, swapGb, 'accent', { area: true })], linux ? [{ value: 0.5, tone: 'warn' }] : [], swapTotal !== null && swapTotal > 0 ? swapTotal / 1024 : niceMax(Math.max(1, peakOf([swapGb]) * 1.15)), 'gb', true),
  ]

  // ---- disk: one line per mount (the union, by first appearance), per device for the throughput ----
  const mounts: string[] = []
  for (const s of samples) for (const d of s.detail?.disks ?? []) if (!mounts.includes(d.mount)) mounts.push(d.mount)
  const devices: string[] = []
  for (const s of samples) for (const d of s.detail?.diskIO ?? []) if (!devices.includes(d.device)) devices.push(d.device)
  const perMount = mounts.map((m, i) => series(`mount${i}`, `${K}.s.mount`, samples.map((s) => s.detail?.disks?.find((d) => d.mount === m)?.usedPct ?? null), PALETTE[i % PALETTE.length], { label: m }))
  /** The read and write of each device: two lines each. */
  const ioSeries = (kind: 'bps' | 'iops'): ChartSeries[] => devices.flatMap((dev, i) => {
    const of = (s: HostSample) => s.detail?.diskIO?.find((x) => x.device === dev) ?? null
    const read = samples.map((s) => { const d = of(s); return d ? (kind === 'bps' ? d.readBps : d.readIops) : null })
    const write = samples.map((s) => { const d = of(s); return d ? (kind === 'bps' ? d.writeBps : d.writeIops) : null })
    return [series(`read${i}`, `${K}.s.read`, read, 'accent', { labelParams: { device: dev } }), series(`write${i}`, `${K}.s.write`, write, 'muted', { labelParams: { device: dev } })]
  })
  const ioBps = ioSeries('bps')
  const ioOps = ioSeries('iops')
  const disk: ChartDef[] = [
    mk('disk', '%', perMount, [{ value: 85, tone: 'warn' }, { value: 95, tone: 'danger' }], 100, 'pct', true),
    mk('diskio', '', ioBps, [], niceMax(peakOf(ioBps.map((s) => s.values))), 'mbps', true),
    mk('iops', '', ioOps, [], niceMax(peakOf(ioOps.map((s) => s.values))), 'iops', true),
  ]

  // ---- the pool ----
  const hh = dcol(samples, (d) => d.headroomHeavy)
  const hl = dcol(samples, (d) => d.headroomLight)
  const queued = dcol(samples, (d) => d.queued)
  const room = dcol(samples, (d) => d.sessionsRoom)
  const pool: ChartDef[] = [
    mk('hr', '', [series('hh', `${K}.s.heavy`, hh, 'heavy', { step: true }), series('hl', `${K}.s.light`, hl, 'light', { step: true })], [], 3.3, 'int', true),
    mk('q', '', [series('queued', `${K}.s.queued`, queued, 'accent', { step: true })], [], niceMax(peakOf([queued]) * 1.2, 4), 'int', true),
    mk('sr', '', [series('room', `${K}.s.room`, room, 'accent', { step: true })], [], 8.8, 'int', false),
  ]

  // ---- processes ----
  const listed = dcol(samples, (d) => d.procsListed)
  const residents = dcol(samples, (d) => d.residents)
  const resMem = dcol(samples, (d) => d.residentsMemMb)
  const total = dcol(samples, (d) => d.procsTotal)
  const proc: ChartDef[] = [
    mk('np', '', [series('listed', `${K}.s.listed`, listed, 'accent'), series('residents', `${K}.s.residents`, residents, 'muted')], [], niceMax(peakOf([listed, residents]) * 1.2), 'int', false),
    mk('nproc', '', [series('total', `${K}.s.total`, total, 'accent')], [], niceMax(peakOf([total]) * 1.2), 'int', true),
    mk('resmem', 'MB', [series('resmem', `${K}.s.resmem`, resMem, 'accent', { area: true })], [], niceMax(Math.max(100, peakOf([resMem]) * 1.2)), 'mb', false),
  ]

  // ---- the host and wwd ----
  const selfCpu = dcol(samples, (d) => d.selfCpuPct)
  const selfRss = dcol(samples, (d) => d.selfRssMb)
  const host: ChartDef[] = [
    mk('wwdcpu', '%', [series('selfcpu', `${K}.s.selfcpu`, selfCpu, 'accent')], [{ value: 1, tone: 'warn' }], niceMax(Math.max(2, peakOf([selfCpu]) * 1.2)), 'f1', true),
    mk('wwdrss', 'MB', [series('selfrss', `${K}.s.selfrss`, selfRss, 'accent')], [{ value: 50, tone: 'warn' }], niceMax(Math.max(60, peakOf([selfRss]) * 1.2)), 'mb', true),
  ]
  const tiles: TileDef[] = [uptimeTile(facts), latencyTile(ctx)]

  return [
    { id: 'cpu', charts: cpu, tiles: [] }, { id: 'mem', charts: mem, tiles: [] }, { id: 'disk', charts: disk, tiles: [] },
    { id: 'pool', charts: pool, tiles: [] }, { id: 'proc', charts: proc, tiles: [] }, { id: 'host', charts: host, tiles },
  ]
}

const pad2 = (n: number): string => String(n).padStart(2, '0')

/** Since when the host is up, read off the latest record: how long (to that record's own time) and the date. No record, or no boot time: a dash. No reboot line is drawn (88 10-20). */
function uptimeTile(facts: PoolFacts | null): TileDef {
  const titleKey = `${K}.uptime.title`
  if (!facts || facts.boot_at === undefined) return { id: 'uptime', titleKey, text: null }
  const up = Math.max(0, facts.at - facts.boot_at)
  const b = new Date(facts.boot_at)
  return { id: 'uptime', titleKey, text: { key: `${K}.uptime`, params: { d: Math.floor(up / 86_400_000), h: Math.floor((up % 86_400_000) / 3_600_000), date: `${pad2(b.getMonth() + 1)}/${pad2(b.getDate())} ${pad2(b.getHours())}:${pad2(b.getMinutes())}` } } }
}

/** How long the source daemon's read of this host takes. The local host is not read over a network; an unknown figure is a dash. */
function latencyTile(ctx: ChartCtx): TileDef {
  const titleKey = `${K}.latency.title`
  if (ctx.local) return { id: 'latency', titleKey, text: { key: `${K}.latency_local` } }
  if (ctx.rttMs === null) return { id: 'latency', titleKey, text: null }
  return { id: 'latency', titleKey, text: { key: `${K}.latency`, params: { alias: ctx.sourceAlias ?? '', ms: Math.round(ctx.rttMs) } } }
}
