// spa/src/lib/pool/events-model.ts — the events page's rows (monitoring tabs spec M23, M35; plan MON-10b): the pool hosts' alerts (`pool.alerts.v1`) and observations (`pool.observations.v1`) as one list. Pure: the data comes in, rows
// come out; the page (MON-10c) draws them and owns the words. Nothing here changes an event: the App has no button that closes one (M35).
//   alerts   one row per alert. A silence is about a peer: its key `silence:<alias>` puts it on that host (a peer the pool does not list: on the host whose list holds it), and the same key from several hosts' lists whose
//            times overlap is one row (every wwd opens its own silence for the same peer, 88 10-20); two stops of the same peer that do not overlap are two rows. Anything else belongs to the host whose list holds it.
//   observations   each host's newest `OBSERVATIONS_SHOWN`; more than that: one "more" row, which the page links to that host's processes tab.
import type { SilenceKind } from './alerts-api'
import type { HostAlertsEntry } from './alerts-store'
import type { PoolObservation } from './observations-api'

export const OBSERVATIONS_SHOWN = 3

export interface EventHost { hostId: string; alias: string }

interface RowBase {
  /** Stable across reads and unique across hosts: an alert's `host|key|start`, an observation's `host|its own id` (wwd's id is only unique on its own host). */
  id: string
  hostId: string
  alias: string
  /** When it began (ms). */
  at: number
  /** In progress now. */
  live: boolean
  /** When it stopped (ms); null while it is in progress. */
  endAt: number | null
  /** The line a person reads: an alert's detail (its kind when there is none), an observation's command signature. */
  title: string
}

export interface AlertRow extends RowBase {
  source: 'alert'
  /** `push` or `digest`; anything else stays as the word. */
  level: string
  kind: string
  /** Only on a silence. */
  silence?: SilenceKind
  /** The latest time the rule still held (ms). */
  last: number
  more?: undefined
}

export interface ObservationRow extends RowBase {
  source: 'obs'
  level: 'obs'
  kind: 'undeclared'
  obs: { session: string; via: string; cpuSeconds: number; /** CPU peak in cores (the wire says percent of one core). */ cpuCores: number; memPeakMb: number; procsPeak: number }
  more?: undefined
}

/** "n more observations on this host": the page links it to the host's processes tab. */
export interface MoreRow { more: true; id: string; hostId: string; alias: string; n: number; at: number }

export type EventRow = AlertRow | ObservationRow | MoreRow
const isMore = (r: EventRow): r is MoreRow => r.more === true

const SILENCE_KEY = /^silence:(\S+)$/

interface Owned { host: EventHost; key: string; since: number; end: number; live: boolean; clearedAt: number | null; last: number; level: string; kind: string; silence?: SilenceKind; detail: string }

export function alertRowsOf(byHost: Record<string, HostAlertsEntry>, hosts: readonly EventHost[]): AlertRow[] {
  const byAlias = new Map(hosts.map((h) => [h.alias, h]))
  const groups = new Map<string, Owned[]>()
  for (const [listAlias, entry] of Object.entries(byHost)) {
    const listHost = byAlias.get(listAlias)
    if (!listHost || entry.kind !== 'ok') continue
    for (const a of entry.alerts) {
      const about = a.kind === 'silence' ? SILENCE_KEY.exec(a.key)?.[1] : undefined
      const host = (about !== undefined ? byAlias.get(about) : undefined) ?? listHost
      const o: Owned = { host, key: a.key, since: a.since, clearedAt: a.clearedAt, live: a.clearedAt === null, end: a.clearedAt ?? Infinity, last: a.last, level: a.level, kind: a.kind, silence: a.silence, detail: a.detail }
      const gk = `${host.hostId}\u0000${a.key}`
      const list = groups.get(gk)
      if (list) list.push(o)
      else groups.set(gk, [o])
    }
  }
  const rows: AlertRow[] = []
  const rowOf = (c: Owned): AlertRow => ({
    id: `${c.host.hostId}|${c.key}|${c.since}`, hostId: c.host.hostId, alias: c.host.alias, at: c.since, source: 'alert', level: c.level, kind: c.kind,
    ...(c.silence !== undefined ? { silence: c.silence } : {}), live: c.live, endAt: c.live ? null : c.end, title: c.detail || c.kind, last: c.last,
  })
  for (const list of groups.values()) {
    list.sort((a, b) => a.since - b.since)
    let cur = list[0]
    for (const o of list.slice(1)) {
      if (o.since <= cur.end) {
        // overlapping: one event. Still in progress if any is; otherwise over when the last one was.
        cur = { ...cur, end: Math.max(cur.end, o.end), live: cur.live || o.live, last: Math.max(cur.last, o.last), level: cur.level === 'push' || o.level === 'push' ? 'push' : cur.level }
      } else {
        rows.push(rowOf(cur))
        cur = o
      }
    }
    rows.push(rowOf(cur))
  }
  return rows
}

/** Records from several pages, one by its id (the cursor is inclusive, so a record can be on two), in the order first met. */
export function mergeObservations(...pages: readonly (readonly PoolObservation[])[]): PoolObservation[] {
  const seen = new Set<string>()
  const out: PoolObservation[] = []
  for (const page of pages) for (const o of page) if (!seen.has(o.id)) { seen.add(o.id); out.push(o) }
  return out
}

export function observationRowsOf(byHost: Record<string, readonly PoolObservation[]>, hosts: readonly EventHost[]): EventRow[] {
  const rows: EventRow[] = []
  for (const host of hosts) {
    const list = byHost[host.alias]
    if (!list || list.length === 0) continue
    const newest = [...list].sort((a, b) => b.startedAt - a.startedAt)
    const shown = newest.slice(0, OBSERVATIONS_SHOWN)
    for (const o of shown) {
      rows.push({
        id: `${host.hostId}|${o.id}`, hostId: host.hostId, alias: host.alias, at: o.startedAt, source: 'obs', level: 'obs', kind: 'undeclared', live: o.endedAt === null, endAt: o.endedAt, title: o.signature,
        obs: { session: o.session, via: o.via, cpuSeconds: o.cpuSeconds, cpuCores: o.cpuPeak / 100, memPeakMb: o.memPeakMb, procsPeak: o.procsPeak },
      })
    }
    if (newest.length > OBSERVATIONS_SHOWN) {
      // just under the oldest one shown, so it sorts right below it
      rows.push({ more: true, id: `more|${host.hostId}`, hostId: host.hostId, alias: host.alias, n: newest.length - OBSERVATIONS_SHOWN, at: shown[shown.length - 1].startedAt - 1 })
    }
  }
  return rows
}

export interface EventFilters { host: string; source: 'all' | 'alert' | 'obs'; status: 'all' | 'live' | 'done' }

export function filterEvents(rows: readonly EventRow[], f: EventFilters): EventRow[] {
  return rows.filter((r) => {
    if (f.host !== 'all' && r.hostId !== f.host) return false
    if (isMore(r)) return f.source !== 'alert' && f.status !== 'live'
    if (f.source !== 'all' && r.source !== f.source) return false
    if (f.status === 'live') return r.live
    if (f.status === 'done') return !r.live
    return true
  })
}

export interface DayGroup { label: 'today' | 'yesterday' | 'date'; /** `MM/DD` in the device's time. */ date: string; rows: EventRow[] }

const two = (n: number): string => String(n).padStart(2, '0')
const dayKey = (ms: number): string => { const d = new Date(ms); return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}` }
const mmdd = (ms: number): string => { const d = new Date(ms); return `${two(d.getMonth() + 1)}/${two(d.getDate())}` }

/** Newest first, in the device's days: today, yesterday, then each older day as `MM/DD`. */
export function groupByDay(rows: readonly EventRow[], now: number): DayGroup[] {
  const today = dayKey(now)
  const y = new Date(now)
  y.setDate(y.getDate() - 1)
  const yesterday = dayKey(y.getTime())
  const out: Array<DayGroup & { key: string }> = []
  for (const r of [...rows].sort((a, b) => b.at - a.at)) {
    const k = dayKey(r.at)
    let g = out.find((x) => x.key === k)
    if (!g) {
      g = { key: k, label: k === today ? 'today' : k === yesterday ? 'yesterday' : 'date', date: mmdd(r.at), rows: [] }
      out.push(g)
    }
    g.rows.push(r)
  }
  return out.map(({ label, date, rows: rs }) => ({ label, date, rows: rs }))
}

/** The line at the top of the page: push-level in progress, and the other kinds in progress. A "more" row is not an event. */
export function liveCounts(rows: readonly EventRow[]): { push: number; other: number } {
  let push = 0
  let other = 0
  for (const r of rows) {
    if (isMore(r) || !r.live) continue
    if (r.level === 'push') push++
    else other++
  }
  return { push, other }
}
