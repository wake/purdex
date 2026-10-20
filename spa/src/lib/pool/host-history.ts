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
}

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

interface HostMemory { tracks: Record<HostTrack, HostSample[]>; loads: Record<HostTrack, HostLoad>; gen: Record<HostTrack, number> }
const memory = new Map<string, HostMemory>()
const memoryOf = (hostId: string): HostMemory => {
  let m = memory.get(hostId)
  if (!m) { m = { tracks: { short: [], long: [] }, loads: { short: 'idle', long: 'idle' }, gen: { short: 0, long: 0 } }; memory.set(hostId, m) }
  return m
}
export const clearHostHistory = (): void => { memory.clear() }

/** Time order, once per time (what is there stays: a late answer does not overwrite a newer reading), nothing from the future, only `keepMs` back from the newest. */
function merge(have: HostSample[], add: HostSample[], keepMs: number, now: number): HostSample[] {
  const byAt = new Map<number, HostSample>()
  for (const s of add) if (s.at <= now + FUTURE_TOLERANCE_MS) byAt.set(s.at, s)
  for (const s of have) byAt.set(s.at, s)
  const all = [...byAt.values()].sort((a, b) => a.at - b.at)
  if (all.length === 0) return all
  const newest = all[all.length - 1].at
  return all.filter((s) => s.at >= newest - keepMs)
}

/** The samples of `range` ending at `now`, from the track that range reads. */
export function readSamples(hostId: string, range: HostRange, now: number): HostSample[] {
  return memoryOf(hostId).tracks[readsFiveSecond(range) ? 'short' : 'long'].filter((s) => s.at >= now - RANGE_MS[range] && s.at <= now + FUTURE_TOLERANCE_MS)
}
export const readHostLoad = (hostId: string, track: HostTrack): HostLoad => memoryOf(hostId).loads[track]

/** Whatever load of this track is out is called off: when it comes back it writes nothing (the page left, the range changed, nothing took its place). */
export function invalidateHostHistory(hostId: string, track?: HostTrack): void {
  const m = memoryOf(hostId)
  for (const t of track ? [track] : (['short', 'long'] as const)) m.gen[t] += 1
}

const failure = (e: unknown): HostLoad => (e instanceof PoolReadError && e.poolError.code === 'host_not_local' ? 'host_not_local' : 'error')

/** Reads wwd's last 30 minutes of 5 s records of `alias` through `source`'s daemon (the 5, 15 and 30 minute ranges are cut from it). Never throws. */
export async function loadShort(source: string, alias: string, hostId: string, now: number): Promise<HostLoad> {
  const m = memoryOf(hostId)
  const mine = ++m.gen.short
  let records: PoolFacts[] = []
  let out: HostLoad
  try {
    records = (await fetchFactsHistory(source, alias, new Date(now - RANGE_MS['30m'] - SKEW_MS).toISOString(), '5s')).records
    out = 'ok'
  } catch (e) { out = failure(e) }
  // A later load for this host started while this one was out: it owns the memory now, whatever this one found.
  if (m.gen.short !== mine) return m.loads.short
  if (out === 'ok') m.tracks.short = merge(m.tracks.short, records.map(sampleOfFacts), KEEP_MS.short, now)
  m.loads.short = out
  return out
}

/** A new facts record of this host (the pool store got a new `facts.at`) joins the short track. */
export function appendLive(hostId: string, facts: PoolFacts, now: number = Date.now()): void {
  const m = memoryOf(hostId)
  m.tracks.short = merge(m.tracks.short, [sampleOfFacts(facts)], KEEP_MS.short, now)
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
  const mine = ++m.gen.long
  const last = m.tracks.long.length > 0 ? m.tracks.long[m.tracks.long.length - 1].at : null
  const since = a.refresh && last !== null ? last : a.now - RANGE_MS[a.range] - SKEW_MS
  let points: SeriesPoint[] = []
  let out: HostLoad
  try {
    points = (await fetchPoolSeries(a.source, a.alias, new Date(since).toISOString(), '1m')).points
    out = 'ok'
  } catch (e) { out = failure(e) }
  if (m.gen.long !== mine) return m.loads.long
  if (out === 'ok') m.tracks.long = merge(m.tracks.long, points.map((p) => sampleOfPoint(p, a.memTotalMb, a.ncpu)), KEEP_MS.long, a.now)
  m.loads.long = out
  return out
}
