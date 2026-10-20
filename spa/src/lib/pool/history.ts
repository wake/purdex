// spa/src/lib/pool/history.ts — the host tab's 5 minute range on the resource pool (plan WWD-7, 7a-4b). When the tab opens, wwd's own last 5 minutes
// (its 5 s facts) are read once and put into the App's raw track, so the charts are full at once instead of filling from the left; the
// zone of each sample is kept for the band. 1 h and longer keep what this App collected. Module memory, never persisted (like the fleet history).
import { fleetHistory, type HistoryItem, type HistoryRange } from '../fleet/history'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { fetchFactsHistory, fetchPoolSeries, PoolReadError } from './api'
import { factsToFast } from './fleet-bridge'
import type { PoolZone, SeriesPoint } from './types'

/** The window the band and the seeded raw track cover. */
export const FACTS_HISTORY_MS = 5 * 60_000
/**
 * The request reaches this much further back than the window: the daemon cuts by its own sample times, so a daemon whose clock is behind this
 * App's would otherwise leave the newest minutes out. What is older than the window is dropped again on this side (the charts cut at 5 minutes).
 */
export const FACTS_HISTORY_SKEW_MS = 60_000

/** `host_not_local`: this daemon cannot read that host's history (it does not forward); the gauges stay, that chart does not. */
export type HistoryLoad = 'idle' | 'ok' | 'host_not_local' | 'error'

export interface ZonePoint { t: number; zone: PoolZone }

const zones = new Map<string, ZonePoint[]>()
const loads = new Map<string, HistoryLoad>()
/** The latest load started for a host: an older one that comes back after it may not write (it could undo what the later one found). */
const generation = new Map<string, number>()

export const readZones = (fleetHostId: string): ZonePoint[] => zones.get(fleetHostId) ?? []
export const readHistoryLoad = (fleetHostId: string): HistoryLoad => loads.get(fleetHostId) ?? 'idle'
export const clearZoneMemory = (): void => { zones.clear(); loads.clear(); generation.clear() }

/** A sample dated more than this ahead of now (the daemon's clock is ahead) cannot be placed and is dropped (the fleet history does the same). */
const FUTURE_TOLERANCE_MS = 5_000

/** One sample's zone: time order, once per time (the first stays), only the last 5 minutes, and not from the future. */
export function recordZone(fleetHostId: string, t: number, zone: PoolZone, now: number = Date.now()): void {
  if (t > now + FUTURE_TOLERANCE_MS) return
  const have = zones.get(fleetHostId) ?? []
  if (have.some((p) => p.t === t)) return
  const all = [...have, { t, zone }].sort((a, b) => a.t - b.t)
  const newest = all[all.length - 1].t
  zones.set(fleetHostId, all.filter((p) => p.t >= newest - FACTS_HISTORY_MS))
}

/** Whatever load of this host is out is called off: when it comes back it writes nothing (the tab left, the range changed, nothing took its place). */
export function invalidateFactsHistory(fleetHostId: string): void {
  generation.set(fleetHostId, (generation.get(fleetHostId) ?? 0) + 1)
}

/**
 * Reads the last 5 minutes of `alias` through `sourceHostId`'s daemon and seeds them under `fleetHostId`. The outcome is remembered per host.
 * Never throws: a refusal that is `host_not_local` is told apart, every other failure is `error`.
 */
export async function loadFactsHistory(sourceHostId: string, alias: string, fleetHostId: string, now: number): Promise<HistoryLoad> {
  const mine = (generation.get(fleetHostId) ?? 0) + 1
  generation.set(fleetHostId, mine)
  let history: Awaited<ReturnType<typeof fetchFactsHistory>> | null = null
  let out: HistoryLoad
  try {
    history = await fetchFactsHistory(sourceHostId, alias, new Date(now - FACTS_HISTORY_MS - FACTS_HISTORY_SKEW_MS).toISOString(), '5s')
    out = 'ok'
  } catch (e) {
    out = e instanceof PoolReadError && e.poolError.code === 'host_not_local' ? 'host_not_local' : 'error'
  }
  // A later load for this host started while this one was out: it owns the memory now, whatever this one found.
  if (generation.get(fleetHostId) !== mine) return readHistoryLoad(fleetHostId)
  if (history) {
    fleetHistory.seedRaw(fleetHostId, history.records.map((f) => factsToFast(f, 0)), now)
    for (const f of history.records) recordZone(fleetHostId, f.at, f.derived.zone, now)
  }
  loads.set(fleetHostId, out)
  return out
}

/** Is the daemon the pool is read through able to answer `/api/pool/facts/series`? An older one is simply not asked: the long ranges stay what the App collected. */
export function seriesAnnounced(sourceHostId: string | null): boolean {
  if (sourceHostId === null) return false
  return useNexHostStore.getState().byHost[sourceHostId]?.daemonCapabilities?.includes('pool.series.v1') === true
}

const SERIES_RANGE_MS: Record<Exclude<HistoryRange, '5m'>, number> = { '1h': 3600_000, '6h': 6 * 3600_000, '24h': 24 * 3600_000 }

/**
 * A series point as the five charted items. An item the daemon has no value for is left out (a gap in that chart), never a 0: a 0 reads as
 * "measured, and nothing". Memory is used %, from the host's own total (the series carries only what is available); with no total it is left out.
 */
function seriesReading(p: SeriesPoint, memTotalMb: number | null): { at: number } & Partial<Record<HistoryItem, number>> {
  const out: { at: number } & Partial<Record<HistoryItem, number>> = { at: p.at, cpu: 100 - p.cpu_idle_pct, level: p.pressure }
  if (memTotalMb !== null && memTotalMb > 0) out.mem = ((memTotalMb - p.mem_available_mb) / memTotalMb) * 100
  if (p.page_out_rate !== null) out.page_rate = p.page_out_rate
  if (p.disk_max_used_pct !== null) out.disk = p.disk_max_used_pct
  return out
}

/**
 * Reads the series for a 1 h+ range through `sourceHostId`'s daemon and seeds the bucket track under `fleetHostId`. It shares the host's load generation
 * with the 5 minute read, so a later load, or an invalidation, calls it off the same way. The outcome is returned, not remembered: a long range that
 * cannot be read is just the App's own history, with no note.
 */
export async function loadSeries(a: {
  sourceHostId: string; alias: string; fleetHostId: string; range: Exclude<HistoryRange, '5m'>; memTotalMb: number | null; now: number
}): Promise<HistoryLoad> {
  const mine = (generation.get(a.fleetHostId) ?? 0) + 1
  generation.set(a.fleetHostId, mine)
  let points: SeriesPoint[] = []
  let out: HistoryLoad
  try {
    points = (await fetchPoolSeries(a.sourceHostId, a.alias, new Date(a.now - SERIES_RANGE_MS[a.range] - FACTS_HISTORY_SKEW_MS).toISOString(), '1m')).points
    out = 'ok'
  } catch (e) {
    out = e instanceof PoolReadError && e.poolError.code === 'host_not_local' ? 'host_not_local' : 'error'
  }
  if (generation.get(a.fleetHostId) !== mine) return out
  if (out === 'ok') fleetHistory.seedBuckets(a.fleetHostId, points.map((p) => seriesReading(p, a.memTotalMb)), a.now)
  return out
}
