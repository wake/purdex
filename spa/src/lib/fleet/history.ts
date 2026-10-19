// spa/src/lib/fleet/history.ts — the fleet's history, IN MEMORY ONLY (fleet spec §2.5: results are used and dropped; any
// history the UI shows lives in the App's memory and a newly started App starts empty). Nothing here touches localStorage,
// sessionStorage or a persist middleware.
//
// Per host and item: 1 s points for the last 10 minutes, and 10 s buckets `{avg, max}` for 24 hours (the bucket being filled
// is served too). `series` picks the 1 s points for the 5 minute range and the buckets for 1 h / 6 h / 24 h.
import type { FleetFast } from './types'

export const HISTORY_ITEMS = ['cpu', 'mem', 'level', 'page_rate', 'disk', 'load'] as const
export type HistoryItem = (typeof HISTORY_ITEMS)[number]
export type HistoryRange = '5m' | '1h' | '6h' | '24h'
export interface HistoryPoint { t: number; avg: number; max: number }

const RAW_MS = 10 * 60_000
const BUCKET_MS = 10_000
const BUCKETS_MS = 24 * 3600_000
const FUTURE_TOLERANCE_MS = 5_000
const RANGE_MS: Record<HistoryRange, number> = { '5m': 5 * 60_000, '1h': 3600_000, '6h': 6 * 3600_000, '24h': 24 * 3600_000 }

interface Bucket { t: number; sum: number; n: number; max: number }
interface Track { raw: Array<{ t: number; v: number }>; buckets: Bucket[] }
interface HostHistory { lastAt: number; lastRawAt: number; items: Record<HistoryItem, Track> }

const emptyTrack = (): Track => ({ raw: [], buckets: [] })

export class FleetHistory {
  private hosts = new Map<string, HostHistory>()

  /**
   * One 1 s reading of a host. A point not newer than the host's last one is ignored (a re-sent or out-of-order reading).
   * Dedupe is on the reading's own `at` (a stale reading re-sent each second is one point, even a future-dated one that is stored at the receive time). A reading dated more than a few
   * seconds ahead of this App's clock (the daemon's clock is ahead) is stored at the receive time, kept after the last stored
   * point, so it cannot push `lastAt` far ahead of the readings that follow, nor trim the history.
   */
  record(hostId: string, reading: FleetFast): void {
    const now = Date.now()
    let hh = this.hosts.get(hostId)
    if (!hh) {
      hh = { lastAt: -Infinity, lastRawAt: -Infinity, items: Object.fromEntries(HISTORY_ITEMS.map((i) => [i, emptyTrack()])) as Record<HistoryItem, Track> }
      this.hosts.set(hostId, hh)
    }
    if (reading.at === hh.lastRawAt) return
    hh.lastRawAt = reading.at
    const ahead = reading.at > now + FUTURE_TOLERANCE_MS
    const t = ahead ? Math.max(now, hh.lastAt + 1) : reading.at
    if (t <= hh.lastAt) return
    const fast = { ...reading, at: t }
    hh.lastAt = t
    for (const item of HISTORY_ITEMS) {
      const v = fast[item]
      const tr = hh.items[item]
      tr.raw.push({ t: fast.at, v })
      let drop = 0
      while (drop < tr.raw.length && tr.raw[drop].t < fast.at - RAW_MS) drop++
      if (drop) tr.raw.splice(0, drop)
      const bt = Math.floor(fast.at / BUCKET_MS) * BUCKET_MS
      // The bucket of this time: usually the last, but seeded buckets can be newer than a live reading, so it is found by its time.
      let k = tr.buckets.length - 1
      while (k >= 0 && tr.buckets[k].t > bt) k--
      const last = tr.buckets[k]
      if (last && last.t === bt) { last.sum += v; last.n++; if (v > last.max) last.max = v }
      else tr.buckets.splice(k + 1, 0, { t: bt, sum: v, n: 1, max: v })
      let dropB = 0
      while (dropB < tr.buckets.length && tr.buckets[dropB].t < fast.at - BUCKETS_MS) dropB++
      if (dropB) tr.buckets.splice(0, dropB)
    }
  }

  /**
   * Readings the daemon already has (its 5 s history, read when the host tab opens) put into the raw 5 minute track, before and among the
   * ones this App collected. A time already there keeps its own value; what is older than the raw window is dropped; the 10 s buckets (1 h
   * and longer) are not touched, they stay what this App saw. Readings carry the five charted items, `load` is not charted. A reading dated
   * more than a few seconds ahead of `now` (the daemon's clock is ahead) cannot be placed on this App's time line and is dropped: kept, it
   * would be taken for the newest and wipe the history this App has, and the chart would cut it off as being in the future.
   */
  seedRaw(hostId: string, all: Array<Pick<FleetFast, 'at' | 'cpu' | 'mem' | 'level' | 'page_rate' | 'disk'>>, now: number = Date.now()): void {
    const readings = all.filter((r) => r.at <= now + FUTURE_TOLERANCE_MS)
    let hh = this.hosts.get(hostId)
    if (!hh) {
      hh = { lastAt: -Infinity, lastRawAt: -Infinity, items: Object.fromEntries(HISTORY_ITEMS.map((i) => [i, emptyTrack()])) as Record<HistoryItem, Track> }
      this.hosts.set(hostId, hh)
    }
    const newest = Math.max(hh.lastAt, ...readings.map((r) => r.at))
    for (const item of HISTORY_ITEMS) {
      if (item === 'load') continue
      const tr = hh.items[item]
      const have = new Set(tr.raw.map((p) => p.t))
      const add = readings.filter((r) => !have.has(r.at)).map((r) => ({ t: r.at, v: r[item] }))
      tr.raw = [...tr.raw, ...add].filter((p) => p.t >= newest - RAW_MS).sort((a, b) => a.t - b.t)
    }
    // `record()` takes a reading only if it is newer than `lastAt`: with the cursor left behind, a live reading at (or before) the newest seeded
    // time would be added after it, doubled or out of order.
    if (readings.length > 0) hh.lastAt = Math.max(hh.lastAt, ...readings.map((r) => r.at))
  }

  /**
   * Points the daemon already has (its 1 minute series, read when a 1 h+ range opens) put into the 10 s bucket track, before and among the buckets
   * this App collected. A bucket already there keeps its own value; a point dated more than a few seconds ahead of `now` is not placed (the same
   * reason as `seedRaw`); what is older than the 24 h of buckets is dropped. An item a point does not carry is a gap for that item only. The 5
   * minute track and the cursors `record()` judges by are not touched: a live reading is still welcome whatever was seeded.
   */
  seedBuckets(hostId: string, points: Array<{ at: number } & Partial<Record<HistoryItem, number>>>, now: number = Date.now()): void {
    let hh = this.hosts.get(hostId)
    if (!hh) {
      hh = { lastAt: -Infinity, lastRawAt: -Infinity, items: Object.fromEntries(HISTORY_ITEMS.map((i) => [i, emptyTrack()])) as Record<HistoryItem, Track> }
      this.hosts.set(hostId, hh)
    }
    const placed = points.filter((p) => p.at <= now + FUTURE_TOLERANCE_MS)
    if (placed.length === 0) return
    for (const item of HISTORY_ITEMS) {
      const tr = hh.items[item]
      const have = new Set(tr.buckets.map((b) => b.t))
      const add: Bucket[] = []
      for (const p of placed) {
        const v = p[item]
        if (v === undefined) continue
        const t = Math.floor(p.at / BUCKET_MS) * BUCKET_MS
        if (have.has(t)) continue
        have.add(t)
        add.push({ t, sum: v, n: 1, max: v })
      }
      if (add.length === 0) continue
      const all = [...tr.buckets, ...add].sort((a, b) => a.t - b.t)
      const newest = all[all.length - 1].t
      tr.buckets = all.filter((b) => b.t >= newest - BUCKETS_MS)
    }
  }

  series(hostId: string, item: HistoryItem, range: HistoryRange, now: number = Date.now()): HistoryPoint[] {
    const tr = this.hosts.get(hostId)?.items[item]
    if (!tr) return []
    const from = now - RANGE_MS[range]
    if (range === '5m') return tr.raw.filter((p) => p.t >= from && p.t <= now).map((p) => ({ t: p.t, avg: p.v, max: p.v }))
    return tr.buckets.filter((b) => b.t >= from && b.t <= now).map((b) => ({ t: b.t, avg: b.sum / b.n, max: b.max }))
  }

  forget(hostId: string): void { this.hosts.delete(hostId) }
  clear(): void { this.hosts.clear() }

  /** Test hooks. */
  rawCount(hostId: string): number { return this.hosts.get(hostId)?.items.cpu.raw.length ?? 0 }
  bucketCount(hostId: string): number { return this.hosts.get(hostId)?.items.cpu.buckets.length ?? 0 }
}

/** The app-wide history the store feeds. */
export const fleetHistory = new FleetHistory()
