// spa/src/lib/watch/events-view.ts — what the 事件 tab shows of the events (watcher area spec §5): the rows of every host merged
// newest first, the three chip filters, and the day separators. Pure; the pane only draws it.
import type { EventsEntry } from './events-store'
import type { WatchEvent } from './events-types'

/** An event with the App host it was read from (the filter's 主機 is the App host, not the daemon's own name). */
export interface EventRow { hostId: string; event: WatchEvent }

export type KindFilter = 'all' | 'unmanaged_heavy' | 'pressure_high' | 'slot_orphaned' | 'slot_long'
export type StatusFilter = 'all' | 'pending' | 'handled'
export interface EventFilters { host: string; kind: KindFilter; status: StatusFilter }

export const DEFAULT_FILTERS: EventFilters = { host: 'all', kind: 'all', status: 'all' }
export const KIND_FILTERS: readonly KindFilter[] = ['all', 'unmanaged_heavy', 'pressure_high', 'slot_orphaned', 'slot_long']
export const STATUS_FILTERS: readonly StatusFilter[] = ['all', 'pending', 'handled']

/** The kinds the tab knows by name; any other kind a newer daemon sends is drawn with its wire name. */
export const KNOWN_KINDS: readonly string[] = ['unmanaged_heavy', 'pressure_high', 'slot_orphaned', 'slot_long']
export const KNOWN_LABELS: readonly string[] = ['gate_gap', 'unknown', 'allowed']

/** Theme-token classes of a kind's tag (one hue per kind) and of an E1 label. */
export const KIND_TONE: Record<string, string> = {
  unmanaged_heavy: 'text-status-warning-text',
  pressure_high: 'text-status-error-text',
  slot_orphaned: 'text-accent',
  slot_long: 'text-text-secondary',
}
export const LABEL_TONE: Record<string, string> = {
  gate_gap: 'text-status-error-text',
  unknown: 'text-status-warning-text',
  allowed: 'text-text-secondary',
}

/** Every host's events as rows, newest first (ties keep the host order the caller gave). */
export function mergeRows(byHost: Record<string, EventsEntry>, hostOrder: readonly string[]): EventRow[] {
  const rows: EventRow[] = []
  const ids = [...hostOrder, ...Object.keys(byHost).filter((id) => !hostOrder.includes(id))]
  for (const hostId of ids) {
    const e = byHost[hostId]
    if (e?.kind === 'ok') for (const event of e.events) rows.push({ hostId, event })
  }
  return rows.sort((a, b) => Date.parse(b.event.at) - Date.parse(a.event.at))
}

export function applyFilters(rows: EventRow[], f: EventFilters): EventRow[] {
  return rows.filter((r) =>
    (f.host === 'all' || r.hostId === f.host) &&
    (f.kind === 'all' || r.event.kind === f.kind) &&
    (f.status === 'all' || r.event.status === f.status))
}

export type DayKey = 'today' | 'yesterday' | string

const pad = (n: number): string => String(n).padStart(2, '0')
const dayStart = (d: Date): number => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()

/** `today`, `yesterday`, or the local date as `YYYY-MM-DD`. */
export function dayKeyOf(atMs: number, nowMs: number): DayKey {
  const d = new Date(atMs)
  const days = Math.round((dayStart(new Date(nowMs)) - dayStart(d)) / 86_400_000)
  if (days === 0) return 'today'
  if (days === 1) return 'yesterday'
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

export interface DayGroup { day: DayKey; rows: EventRow[] }

/** Consecutive rows of one day make a group (the rows are already newest first). */
export function groupByDay(rows: EventRow[], nowMs: number): DayGroup[] {
  const groups: DayGroup[] = []
  for (const r of rows) {
    const day = dayKeyOf(Date.parse(r.event.at), nowMs)
    const last = groups[groups.length - 1]
    if (last && last.day === day) last.rows.push(r)
    else groups.push({ day, rows: [r] })
  }
  return groups
}

/** `HH:mm` in local time. */
export function clock(at: string): string {
  const d = new Date(at)
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** 「42 秒」「3 分 5 秒」-style duration as short numbers: `42s`, `3m05s`, `1h02m`. */
export function shortDuration(s: number): string {
  const total = Math.round(s)
  if (total < 60) return `${total}s`
  if (total < 3600) return `${Math.floor(total / 60)}m${pad(total % 60)}s`
  return `${Math.floor(total / 3600)}h${pad(Math.floor((total % 3600) / 60))}m`
}
