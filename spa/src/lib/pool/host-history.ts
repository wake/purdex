// spa/src/lib/pool/host-history.ts — what the host page remembers of a host's readings, from the pool only (monitoring tabs spec M43, plan MON-6c). One reading
// is a `HostSample`; two sources make it: wwd's full 5 s record and the daemon's 1 minute series point. A field the source does not say is null, never 0 (a 0 reads as
// "measured, and nothing"). The memory is per `host_id`, module-level, never persisted.
import { useNexHostStore } from '../../stores/useNexHostStore'
import { fetchFactsHistory, fetchPoolSeries, PoolReadError } from './api'
import type { PoolFacts, PoolZone, SeriesPoint } from './types'

export interface HostSample {
  at: number
  cpuPct: number | null
  load1: number | null
  load5: number | null
  ncpu: number | null
  memUsedMb: number | null
  memAvailMb: number | null
  memTotalMb: number | null
  pageOut: number | null
  swapOut: number | null
  swapUsedMb: number | null
  diskUsedPct: number | null
  diskFreeGb: number | null
  runHeavy: number | null
  runLight: number | null
  slotHeavy: number | null
  slotLight: number | null
  workerOn: boolean | null
  zone: PoolZone | null
  /** What the status tab and the full charts read beyond the mini charts' fields. Optional: a sample built without it (a test literal, an older reader) has none; absent means this sample was not read for detail. */
  detail?: HostDetail
}

/**
 * The detail of one sample. Every figure is null where the source does not say (never 0, which would read as "measured, and nothing"): a record from before the host-metrics fields, a platform without the figure, or a series point,
 * which does not carry `cores`, `psi`, `sessionsRoom`, `procsListed`, `residents`, `residentsMemMb` and `bootAt` (host-metrics spec §2.5).
 */
export interface HostDetail {
  load15: number | null
  userPct: number | null
  systemPct: number | null
  cores: { user: number | null; system: number | null; idle: number | null; kind: 'p' | 'e' | null }[] | null
  /** The memory pressure level; 0 (unknown) is null. */
  level: number | null
  psi: { some10: number; full10: number; some60: number } | null
  compressorMb: number | null
  decompressRate: number | null
  swapTotalMb: number | null
  disks: { mount: string; usedPct: number; freeGb: number }[] | null
  diskIO: { device: string; readBps: number | null; writeBps: number | null; readIops: number | null; writeIops: number | null }[] | null
  headroomHeavy: number | null
  headroomLight: number | null
  queued: number | null
  sessionsRoom: number | null
  /** How many processes the record lists (not the host's total). */
  procsListed: number | null
  /** The residents' count in all and their memory in all. */
  residents: number | null
  residentsMemMb: number | null
  procsTotal: number | null
  selfCpuPct: number | null
  selfRssMb: number | null
  bootAt: number | null
}

const levelOf = (pressure: number): number | null => (pressure > 0 ? pressure : null)

/** The sample of a full 5 s record; the disk is the one with the highest `used_pct`. */
export function sampleOfFacts(f: PoolFacts): HostSample {
  const fullest = f.disk.reduce<PoolFacts['disk'][number] | null>((best, d) => (best === null || d.used_pct > best.used_pct ? d : best), null)
  return {
    at: f.at, cpuPct: 100 - f.cpu.idle_pct, load1: f.cpu.load1, load5: f.cpu.load5, ncpu: f.cpu.ncpu,
    memUsedMb: f.mem.total_mb - f.mem.available_mb, memAvailMb: f.mem.available_mb, memTotalMb: f.mem.total_mb,
    pageOut: f.mem.page_out_rate, swapOut: f.mem.swap_out_rate, swapUsedMb: f.mem.swap_used_mb,
    diskUsedPct: fullest?.used_pct ?? null, diskFreeGb: fullest?.free_gb ?? null,
    runHeavy: f.worker.running.heavy, runLight: f.worker.running.light, slotHeavy: f.worker.slots.heavy, slotLight: f.worker.slots.light,
    workerOn: f.worker.enabled, zone: f.derived.zone,
    detail: {
      load15: f.cpu.load15 ?? null, userPct: f.cpu.user_pct ?? null, systemPct: f.cpu.system_pct ?? null,
      cores: f.cpu.cores ? f.cpu.cores.map((c) => ({ user: c.user_pct, system: c.system_pct, idle: c.idle_pct, kind: c.kind })) : null,
      level: levelOf(f.mem.pressure), psi: f.mem.psi, compressorMb: f.mem.compressor_mb, decompressRate: f.mem.decompress_rate ?? null, swapTotalMb: f.mem.swap_total_mb ?? null,
      disks: f.disk.map((d) => ({ mount: d.mount, usedPct: d.used_pct, freeGb: d.free_gb })),
      diskIO: f.disk_io ? f.disk_io.map((d) => ({ device: d.device, readBps: d.read_bps, writeBps: d.write_bps, readIops: d.read_iops, writeIops: d.write_iops })) : null,
      headroomHeavy: f.derived.headroom.heavy, headroomLight: f.derived.headroom.light, queued: f.queue?.queued ?? null, sessionsRoom: f.derived.sessions_room,
      procsListed: f.procs.length, residents: f.residents.reduce((n, r) => n + r.count, 0), residentsMemMb: f.residents.reduce((n, r) => n + r.mem_mb, 0),
      procsTotal: f.procs_total ?? null, selfCpuPct: f.self?.cpu_pct ?? null, selfRssMb: f.self?.rss_mb ?? null, bootAt: f.boot_at ?? null,
    },
  }
}

/**
 * The sample of a series point. The series carries only what is available of memory, so the used figure needs the host's total; `ncpu` is the host's constant.
 * The series has no slots and no worker switch: those are null.
 */
export function sampleOfPoint(p: SeriesPoint, memTotalMb: number | null, ncpu: number | null): HostSample {
  const total = memTotalMb !== null && memTotalMb > 0 ? memTotalMb : null
  const fullest = (p.disks ?? []).reduce<NonNullable<SeriesPoint['disks']>[number] | null>((best, d) => (best === null || d.used_pct > best.used_pct ? d : best), null)
  return {
    at: p.at, cpuPct: 100 - p.cpu_idle_pct, load1: p.load1 ?? null, load5: p.load5 ?? null, ncpu,
    memUsedMb: total === null ? null : total - p.mem_available_mb, memAvailMb: p.mem_available_mb, memTotalMb: total,
    pageOut: p.page_out_rate, swapOut: p.swap_out_rate ?? null, swapUsedMb: p.swap_used_mb ?? null,
    diskUsedPct: fullest?.used_pct ?? p.disk_max_used_pct, diskFreeGb: fullest?.free_gb ?? null,
    runHeavy: p.worker_running?.heavy ?? null, runLight: p.worker_running?.light ?? null, slotHeavy: null, slotLight: null, workerOn: null, zone: p.zone,
    detail: {
      load15: p.load15 ?? null, userPct: p.cpu_user_pct ?? null, systemPct: p.cpu_system_pct ?? null, cores: null,
      level: levelOf(p.pressure), psi: null, compressorMb: p.compressor_mb ?? null, decompressRate: p.decompress_rate ?? null, swapTotalMb: p.swap_total_mb ?? null,
      disks: p.disks ? p.disks.map((d) => ({ mount: d.mount, usedPct: d.used_pct, freeGb: d.free_gb })) : null,
      diskIO: p.disk_io ? p.disk_io.map((d) => ({ device: d.device, readBps: d.read_bps, writeBps: d.write_bps, readIops: d.read_iops, writeIops: d.write_iops })) : null,
      headroomHeavy: p.headroom.heavy, headroomLight: p.headroom.light, queued: p.queue_queued ?? null, sessionsRoom: null,
      procsListed: null, residents: null, residentsMemMb: null,
      procsTotal: p.procs_total ?? null, selfCpuPct: p.self_cpu_pct ?? null, selfRssMb: p.self_rss_mb ?? null, bootAt: null,
    },
  }
}

// ---- The memory: two tracks per host (`host_id`) -------------------------------------------------------------------------------------------------------------

/** The ranges of the host page's charts. 30 minutes and shorter are read from wwd's 5 s records, longer ones from the daemon's 1 minute series. */
export type HostRange = '5m' | '15m' | '30m' | '1h' | '6h' | '24h'
export const RANGE_MS: Record<HostRange, number> = { '5m': 5 * 60_000, '15m': 15 * 60_000, '30m': 30 * 60_000, '1h': 3600_000, '6h': 6 * 3600_000, '24h': 24 * 3600_000 }
export const readsFiveSecond = (r: HostRange): boolean => RANGE_MS[r] <= RANGE_MS['30m']

/** `host_not_local`: this daemon cannot read that host's history (it does not forward); the gauges stay, that chart does not. */
export type HostLoad = 'idle' | 'ok' | 'host_not_local' | 'error'
export type HostTrack = 'short' | 'long'

/** What each track keeps, back from its newest sample: 31 minutes of 5 s samples (a 30 minute range and a minute of skew), 24 hours of 1 minute samples. */
export const KEEP_MS: Record<HostTrack, number> = { short: 31 * 60_000, long: 24 * 3600_000 }
/** The request reaches this much further back than the window: the daemon cuts by its own sample times, so one whose clock is behind this App's would otherwise leave the newest minutes out. */
export const SKEW_MS = 60_000
/** A sample dated more than this ahead of now (the daemon's clock is ahead) cannot be placed and is dropped. */
const FUTURE_TOLERANCE_MS = 5_000

interface HostMemory {
  tracks: Record<HostTrack, HostSample[]>; loads: Record<HostTrack, HostLoad>; gen: Record<HostTrack, number>
  /** How many pages hold each track open: only the last one to leave calls a read off. */
  holds: Record<HostTrack, number>
  listeners: Set<() => void>
  /** The long read in flight, by what it asks for: the same ask is not made twice (a second page shares it), a different one takes over. */
  longInFlight: string | null
  /** The short read in flight, with what it asks for: the same ask shares it (a second caller gets its answer, the memory's generation is not moved), a different one takes over. */
  shortInFlight: { ask: string; p: Promise<HostLoad> } | null
}
const memory = new Map<string, HostMemory>()
const newMemory = (): HostMemory => ({ tracks: { short: [], long: [] }, loads: { short: 'idle', long: 'idle' }, gen: { short: 0, long: 0 }, holds: { short: 0, long: 0 }, listeners: new Set(), longInFlight: null, shortInFlight: null })
/** What the memory keeps of hosts nobody is looking at: leaving a page keeps its history (coming back is full at once), but not for any number of hosts. */
const MAX_HOSTS = 32
const inUse = (m: HostMemory): boolean => m.listeners.size > 0 || m.holds.short > 0 || m.holds.long > 0 || m.longInFlight !== null || m.shortInFlight !== null
/** Oldest first (Map order): let go of the ones nobody listens to or holds, until the cap is met. Run when a host is added and when one stops being used. */
function prune(except?: string): void {
  for (const [id, old] of memory) {
    if (memory.size <= MAX_HOSTS) break
    if (id !== except && !inUse(old)) memory.delete(id)
  }
}
const memoryOf = (hostId: string): HostMemory => {
  let m = memory.get(hostId)
  if (!m) {
    m = newMemory()
    memory.set(hostId, m)
    prune(hostId)
  }
  return m
}
export const clearHostHistory = (): void => { memory.clear() }

/** Everything that draws a host's readings is told when they change, whoever made the change (two pages on one host see the same memory). */
export function subscribeHost(hostId: string, fn: () => void): () => void {
  const m = memoryOf(hostId)
  m.listeners.add(fn)
  return () => { m.listeners.delete(fn); prune() }
}
const notify = (m: HostMemory): void => { for (const fn of [...m.listeners]) fn() }

/** A page holds this track open; the returned release calls off what is still out only when the last page has left. */
export function holdTrack(hostId: string, track: HostTrack): () => void {
  const m = memoryOf(hostId)
  m.holds[track] += 1
  return () => { m.holds[track] -= 1; if (m.holds[track] <= 0) { m.gen[track] += 1; if (track === 'long') m.longInFlight = null; else m.shortInFlight = null } prune() }
}

/** The long track is empty again (the daemon no longer announces the series, so what it left is not a history this App can vouch for). */
export function dropLongTrack(hostId: string): void {
  const m = memory.get(hostId)
  if (!m || (m.tracks.long.length === 0 && m.loads.long === 'idle')) return
  m.tracks.long = []; m.loads.long = 'idle'; m.gen.long += 1; m.longInFlight = null
  notify(m)
}

/**
 * Time order, once per time (what is there stays: a late answer does not overwrite a newer reading), nothing from the future, only `keepMs` back from the newest.
 * The one exception: a series sample read before the host's memory total was known (null) is replaced by the same time read after it.
 */
function merge(have: HostSample[], add: HostSample[], keepMs: number, now: number): HostSample[] {
  const byAt = new Map<number, HostSample>()
  for (const s of add) if (s.at <= now + FUTURE_TOLERANCE_MS) byAt.set(s.at, s)
  for (const s of have) {
    const incoming = byAt.get(s.at)
    byAt.set(s.at, incoming && s.memTotalMb === null && incoming.memTotalMb !== null ? incoming : s)
  }
  const all = [...byAt.values()].sort((a, b) => a.at - b.at)
  if (all.length === 0) return all
  const newest = all[all.length - 1].at
  return all.filter((s) => s.at >= newest - keepMs)
}

/** The samples of `range` ending at `now`, from the track that range reads. */
export function readSamples(hostId: string, range: HostRange, now: number): HostSample[] {
  return (memory.get(hostId)?.tracks[readsFiveSecond(range) ? 'short' : 'long'] ?? []).filter((s) => s.at >= now - RANGE_MS[range] && s.at <= now + FUTURE_TOLERANCE_MS)
}
/** The time of the newest sample of this track, or null when it has none. */
export const newestAt = (hostId: string, track: HostTrack): number | null => {
  const t = memory.get(hostId)?.tracks[track] ?? []
  return t.length > 0 ? t[t.length - 1].at : null
}
export const readHostLoad = (hostId: string, track: HostTrack): HostLoad => memory.get(hostId)?.loads[track] ?? 'idle'

/** Whatever load of this track is out is called off: when it comes back it writes nothing (the page left, the range changed, nothing took its place). */
export function invalidateHostHistory(hostId: string, track?: HostTrack): void {
  const m = memoryOf(hostId)
  for (const t of track ? [track] : (['short', 'long'] as const)) m.gen[t] += 1
  if (!track || track === 'long') m.longInFlight = null
  if (!track || track === 'short') m.shortInFlight = null
}

/** How far into the window the oldest 5 s record may start and still count as covering it. */
const BACKFILL_TOLERANCE_MS = 15_000
const failure = (e: unknown): HostLoad => (e instanceof PoolReadError && e.poolError.code === 'host_not_local' ? 'host_not_local' : 'error')

/** Reads wwd's last 30 minutes of 5 s records of `alias` through `source`'s daemon (the 5, 15 and 30 minute ranges are cut from it). Never throws. */
export function loadShort(source: string, alias: string, hostId: string, now: number): Promise<HostLoad> {
  const m = memoryOf(hostId)
  const ask = `${source}|${alias}`
  // The same ask already out is that read: a second one would call the first off (they share the generation), and if it failed it would throw away what the first found.
  if (m.shortInFlight !== null && m.shortInFlight.ask === ask) return m.shortInFlight.p
  const mine = ++m.gen.short
  const flight: NonNullable<HostMemory['shortInFlight']> = { ask, p: undefined as unknown as Promise<HostLoad> }
  flight.p = readShort(m, source, alias, hostId, now, mine, flight)
  m.shortInFlight = flight
  return flight.p
}

async function readShort(m: HostMemory, source: string, alias: string, hostId: string, now: number, mine: number, flight: NonNullable<HostMemory['shortInFlight']>): Promise<HostLoad> {
  let records: PoolFacts[] = []
  let out: HostLoad
  const since = new Date(now - RANGE_MS['30m'] - SKEW_MS).toISOString()
  try {
    records = (await fetchFactsHistory(source, alias, since, '5s')).records
    out = 'ok'
  } catch (e) { out = failure(e) }
  // wwd keeps its 5 s records in memory only, so after it restarts the front of the window is empty. The 1 minute records live in wwd.db and span the restart: fill the front from them.
  if (out === 'ok' && m.gen.short === mine) {
    const oldest = records.reduce((min, r) => Math.min(min, r.at), Infinity)
    if (!(oldest <= now - RANGE_MS['30m'] + BACKFILL_TOLERANCE_MS)) {
      try {
        const minutes = (await fetchFactsHistory(source, alias, since, '1m')).records.filter((r) => r.at < oldest)
        records = [...minutes, ...records]
      } catch { /* the 5 s answer stands */ }
    }
  }
  if (m.shortInFlight === flight) m.shortInFlight = null
  // A later load for this host started while this one was out: it owns the memory now, whatever this one found.
  if (m.gen.short !== mine) return m.loads.short
  if (out === 'ok') m.tracks.short = merge(m.tracks.short, records.map(sampleOfFacts), KEEP_MS.short, now)
  m.loads.short = out
  notify(m)
  return out
}

/** A new facts record of this host (the pool store got a new `facts.at`) joins the short track. */
export function appendLive(hostId: string, facts: PoolFacts, now: number = Date.now()): void {
  const m = memoryOf(hostId)
  m.tracks.short = merge(m.tracks.short, [sampleOfFacts(facts)], KEEP_MS.short, now)
  notify(m)
}

/** Is the daemon the pool is read through able to answer `/api/pool/facts/series`? An older one is simply not asked: the long ranges stay empty. */
export function seriesAnnounced(source: string | null, byHost: ReturnType<typeof useNexHostStore.getState>['byHost'] = useNexHostStore.getState().byHost): boolean {
  if (source === null) return false
  return byHost[source]?.daemonCapabilities?.includes('pool.series.v1') === true
}

/**
 * Reads the 1 minute series of a long range. With `refresh`, only what is newer than the last sample of the track is read (one more minute at a time), not the whole range again.
 * Not asked at all when the daemon has not announced `pool.series.v1`. Never throws.
 */
export async function loadLong(a: { source: string; alias: string; hostId: string; range: HostRange; memTotalMb: number | null; ncpu: number | null; now: number; refresh?: boolean }): Promise<HostLoad> {
  const m = memoryOf(a.hostId)
  if (!seriesAnnounced(a.source)) return m.loads.long
  // A top-up never takes over a read in flight, and the same ask made by a second page is that read (a new read would call the first off).
  const ask = `${a.source}|${a.alias}|${a.range}|${a.memTotalMb !== null && a.memTotalMb > 0}`
  if (m.longInFlight !== null && (a.refresh || m.longInFlight === ask)) return m.loads.long
  const mine = ++m.gen.long
  m.longInFlight = ask
  const last = m.tracks.long.length > 0 ? m.tracks.long[m.tracks.long.length - 1].at : null
  const since = a.refresh && last !== null ? last : a.now - RANGE_MS[a.range] - SKEW_MS
  let points: SeriesPoint[] = []
  let out: HostLoad
  try {
    points = (await fetchPoolSeries(a.source, a.alias, new Date(since).toISOString(), '1m')).points
    out = 'ok'
  } catch (e) { out = failure(e) }
  if (m.gen.long === mine) m.longInFlight = null
  if (m.gen.long !== mine) return m.loads.long
  if (out === 'ok') m.tracks.long = merge(m.tracks.long, points.map((p) => sampleOfPoint(p, a.memTotalMb, a.ncpu)), KEEP_MS.long, a.now)
  m.loads.long = out
  notify(m)
  return out
}
