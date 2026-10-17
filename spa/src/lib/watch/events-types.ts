// spa/src/lib/watch/events-types.ts — the shape of `GET /api/watch/events` (internal/watch/eventview.go `EventView`), read
// leniently: a row without an id, a time or a kind is dropped; a field this App does not know is ignored.
export type EventStatus = 'pending' | 'handled'

export interface WatchEvent {
  id: string
  /** The host that recorded it (its daemon's host name). */
  host: string
  /** `unmanaged_heavy | pressure_high | slot_orphaned | slot_long`; a kind this App does not know is kept as it came. */
  kind: string
  /** RFC 3339. */
  at: string
  /** E1 only: `gate_gap | unknown | allowed`. */
  label?: string
  command?: string
  cwd?: string
  session?: string
  peakCores?: number
  peakMb?: number
  durationS?: number
  pressure?: number
  pageRate?: number
  detail?: string
  slot?: string
  status: EventStatus
  note?: string
  handledAt?: string
}

/** A count the daemon can have sent: a whole number, zero or more. */
export const isCount = (v: unknown): v is number => typeof v === 'number' && Number.isInteger(v) && v >= 0

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const str = (v: unknown): string | undefined => (typeof v === 'string' && v !== '' ? v : undefined)
const num = (v: unknown): number | undefined => (typeof v === 'number' && Number.isFinite(v) ? v : undefined)

function parseEvent(v: unknown): WatchEvent | null {
  if (!isObj(v)) return null
  const id = str(v.id)
  const kind = str(v.kind)
  const at = str(v.at)
  if (!id || !kind || !at || Number.isNaN(Date.parse(at))) return null
  return {
    id, kind, at, host: str(v.host) ?? '',
    label: str(v.label), command: str(v.command), cwd: str(v.cwd), session: str(v.session),
    peakCores: num(v.peak_cores), peakMb: num(v.peak_mb), durationS: num(v.duration_s),
    pressure: num(v.pressure), pageRate: num(v.page_rate), detail: str(v.detail), slot: str(v.slot),
    // anything but `handled` is pending: an event waits until the daemon says it was handled
    status: v.status === 'handled' ? 'handled' : 'pending',
    note: str(v.note), handledAt: str(v.handled_at),
  }
}

/** null = unreadable (no `events` array). */
export function parseWatchEvents(raw: unknown): WatchEvent[] | null {
  if (!isObj(raw) || !Array.isArray(raw.events)) return null
  return raw.events.map(parseEvent).filter((e): e is WatchEvent => e !== null)
}
