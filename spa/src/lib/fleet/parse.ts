// spa/src/lib/fleet/parse.ts — the trust boundary of the fleet wire. A malformed host row is dropped whole; a malformed
// `fast` / `slow` part makes that part null (nothing is invented); a malformed envelope is `null`.
import {
  ERROR_CODES, HOST_KINDS, HOST_STATUSES, ZONES,
  type FleetAnswer, type FleetErrorCode, type FleetFast, type FleetHost, type FleetHostStatus, type FleetProc, type FleetPush,
  type FleetPushHost, type FleetQueue, type FleetSlow, type FleetWaiter,
} from './types'

type Rec = Record<string, unknown>
const isRec = (v: unknown): v is Rec => typeof v === 'object' && v !== null && !Array.isArray(v)
const num = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)
const str = (v: unknown): v is string => typeof v === 'string'
const oneOf = <T extends string>(list: readonly T[], v: unknown): v is T => str(v) && (list as readonly string[]).includes(v)

export function parseFast(v: unknown): FleetFast | null {
  if (!isRec(v)) return null
  const { at, cpu, mem, level, page_rate, disk, zone, load } = v
  if (!num(at) || !num(cpu) || !num(mem) || !num(level) || !num(page_rate) || !num(disk) || !num(load) || !oneOf(ZONES, zone)) return null
  const opt = (x: unknown): number => (num(x) ? x : 0)
  return { at, cpu, mem, level, page_rate, disk, disk_free_gb: opt(v.disk_free_gb), load1: opt(v.load1), ncpu: opt(v.ncpu), zone, load }
}

function parseProcs(v: unknown): FleetProc[] {
  if (!Array.isArray(v)) return []
  const out: FleetProc[] = []
  for (const p of v) {
    if (isRec(p) && num(p.pid) && str(p.name) && num(p.cpu) && num(p.rss_mb)) out.push({ pid: p.pid, name: p.name, cpu: p.cpu, rss_mb: p.rss_mb })
  }
  return out
}

function parseSlots(v: unknown): { used: number; total: number } | null {
  return isRec(v) && num(v.used) && num(v.total) ? { used: v.used, total: v.total } : null
}

export function parseQueue(v: unknown): FleetQueue | undefined {
  if (!isRec(v)) return undefined
  const heavy = parseSlots(v.heavy)
  const light = parseSlots(v.light)
  if (!heavy || !light || !Array.isArray(v.waiters)) return undefined
  const waiters: FleetWaiter[] = []
  for (const w of v.waiters) {
    if (isRec(w) && num(w.place) && str(w.command) && str(w.project) && num(w.since)) waiters.push({ place: w.place, command: w.command, project: w.project, since: w.since })
  }
  return { heavy, light, waiters, longest_wait_ms: num(v.longest_wait_ms) ? v.longest_wait_ms : 0, admission_held: v.admission_held === true }
}

export function parseSlow(v: unknown): FleetSlow | null {
  if (!isRec(v) || !num(v.at)) return null
  const queue = parseQueue(v.queue)
  return { at: v.at, top_cpu: parseProcs(v.top_cpu), top_mem: parseProcs(v.top_mem), ...(queue ? { queue } : {}) }
}

const errorCode = (v: unknown): FleetErrorCode | null => (v === null || v === undefined || v === '' ? null : oneOf(ERROR_CODES, v) ? v : 'parse')

/** A status the client does not know is an error it cannot read (`parse`), never `ok`. */
function statusOf(v: unknown, errorWire: unknown): { status: FleetHostStatus; error: FleetErrorCode | null } {
  if (!oneOf(HOST_STATUSES, v)) return { status: 'error', error: 'parse' }
  return { status: v, error: v === 'ok' ? null : errorCode(errorWire) }
}

export function parseHost(v: unknown): FleetHost | null {
  if (!isRec(v) || !str(v.id) || v.id === '' || !oneOf(HOST_KINDS, v.kind) || !str(v.alias)) return null
  const { status, error } = statusOf(v.status, v.error)
  return {
    id: v.id, kind: v.kind, alias: v.alias,
    name: str(v.name) ? v.name : v.alias,
    icon: str(v.icon) ? v.icon : '',
    ...(isRec(v.colors) ? { colors: v.colors as FleetHost['colors'] } : {}),
    ...(str(v.icon_weight) ? { icon_weight: v.icon_weight } : {}),
    os: str(v.os) ? v.os : '',
    status, error,
    last_ok_at: num(v.last_ok_at) ? v.last_ok_at : null,
    fast: parseFast(v.fast), slow: parseSlow(v.slow),
  }
}

export function parseFleetAnswer(raw: unknown): FleetAnswer | null {
  if (!isRec(raw)) return null
  if (raw.role === 'primary') {
    if (!num(raw.term) || !Array.isArray(raw.hosts)) return null
    const hosts = raw.hosts.map(parseHost).filter((h): h is FleetHost => h !== null)
    return { role: 'primary', term: raw.term, at: num(raw.at) ? raw.at : 0, hosts }
  }
  if (raw.role === 'standby') return num(raw.term) && str(raw.primary) ? { role: 'standby', term: raw.term, primary: raw.primary } : null
  if (raw.role === 'off') return { role: 'off', reason: str(raw.reason) ? raw.reason : '' }
  return null
}

/** A `fleet.changed` payload: `{term, primary, hosts}`; a host row without an id is dropped, an unreadable part is left out. */
export function parseFleetPush(raw: unknown): FleetPush | null {
  if (!isRec(raw) || !num(raw.term) || !str(raw.primary) || !Array.isArray(raw.hosts)) return null
  const hosts: FleetPushHost[] = []
  for (const h of raw.hosts) {
    if (!isRec(h) || !str(h.id) || h.id === '') continue
    const row: FleetPushHost = { id: h.id }
    const fast = parseFast(h.fast)
    if (fast) row.fast = fast
    const slow = parseSlow(h.slow)
    if (slow) row.slow = slow
    if (h.status !== undefined) { const st = statusOf(h.status, h.error); row.status = st.status; row.error = st.error }
    else if (h.error !== undefined) row.error = errorCode(h.error)
    if (h.kind !== undefined) { const full = parseHost(h); if (full) row.full = full }
    hosts.push(row)
  }
  return { term: raw.term, primary: raw.primary, hosts }
}
