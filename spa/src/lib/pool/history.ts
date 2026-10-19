// spa/src/lib/pool/history.ts — the host tab's 5 minute range on the resource pool (plan WWD-7, 7a-4b). When the tab opens, wwd's own last 5 minutes
// (its 5 s facts) are read once and put into the App's raw track, so the charts are full at once instead of filling from the left; the
// zone of each sample is kept for the band. 1 h and longer keep what this App collected. Module memory, never persisted (like the fleet history).
import { fleetHistory } from '../fleet/history'
import { fetchFactsHistory, PoolReadError } from './api'
import { factsToFast } from './cell-model'
import type { PoolZone } from './types'

/** The window the band and the seeded raw track cover. */
export const FACTS_HISTORY_MS = 5 * 60_000

/** `host_not_local`: this daemon cannot read that host's history (it does not forward); the gauges stay, that chart does not. */
export type HistoryLoad = 'idle' | 'ok' | 'host_not_local' | 'error'

export interface ZonePoint { t: number; zone: PoolZone }

const zones = new Map<string, ZonePoint[]>()
const loads = new Map<string, HistoryLoad>()

export const readZones = (fleetHostId: string): ZonePoint[] => zones.get(fleetHostId) ?? []
export const readHistoryLoad = (fleetHostId: string): HistoryLoad => loads.get(fleetHostId) ?? 'idle'
export const clearZoneMemory = (): void => { zones.clear(); loads.clear() }

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

/**
 * Reads the last 5 minutes of `alias` through `sourceHostId`'s daemon and seeds them under `fleetHostId`. The outcome is remembered per host.
 * Never throws: a refusal that is `host_not_local` is told apart, every other failure is `error`.
 */
export async function loadFactsHistory(sourceHostId: string, alias: string, fleetHostId: string, now: number): Promise<HistoryLoad> {
  let out: HistoryLoad
  try {
    const h = await fetchFactsHistory(sourceHostId, alias, new Date(now - FACTS_HISTORY_MS).toISOString(), '5s')
    fleetHistory.seedRaw(fleetHostId, h.records.map((f) => factsToFast(f, 0)), now)
    for (const f of h.records) recordZone(fleetHostId, f.at, f.derived.zone, now)
    out = 'ok'
  } catch (e) {
    out = e instanceof PoolReadError && e.poolError.code === 'host_not_local' ? 'host_not_local' : 'error'
  }
  loads.set(fleetHostId, out)
  return out
}
