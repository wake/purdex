// spa/src/lib/pool/observations-api.ts — a host's observations (`pool.observations.v1`, `GET /api/pool/hosts/{alias}/observations`; Go: internal/wwd/wire/observation.go, internal/module/pool/observations.go): the process trees that
// belong to no job and used more than a threshold. Admin tokens only (a device token gets 403, `forbidden`). The trust boundary: a record that lacks what names it (id, signature, via, a start time) or whose time is broken is dropped,
// the others stay; a `via` the App does not know stays as the word; a missing figure is 0. The answer's `next_until` is a cursor wwd wrote with a zone and a fraction of a second: it is kept as the string it came as and sent back
// untouched (cutting it skips records), and since it is inclusive a record can be on two pages, so a record is one by its id.
import { readJson, unreadable } from './api'
import { isRec, num, str, time } from './parse'

export interface PoolObservation {
  id: string
  host: string
  /** The session's ref; `orphan` when none could be named. */
  session: string
  /** `session` or `codex`; anything else stays as the word. */
  via: string
  /** Redacted, at most 200 characters. */
  command: string
  signature: string
  startedAt: number
  /** null while the tree is still running. */
  endedAt: number | null
  cpuSeconds: number
  /** The most it used at once, in percent of one core. */
  cpuPeak: number
  memPeakMb: number
  procsPeak: number
}

export interface ObservationsPage {
  /** The host the answer says it is about (the answering wwd's own alias). */
  host: string
  /** false: that host's wwd predates the route, not an error. */
  known: boolean
  /** In wwd's order, the oldest first. */
  observations: PoolObservation[]
  truncated: boolean
  /** The cursor for the next (older) page, exactly as wwd wrote it; null when there is none. */
  nextUntil: string | null
}

const figure = (v: unknown): number => (num(v) ? v : 0)

function parseObservation(v: unknown): PoolObservation | null {
  if (!isRec(v)) return null
  const { id, via, signature } = v
  if (!str(id) || id === '' || !str(via) || via === '' || !str(signature) || signature === '') return null
  const startedAt = time(v.started_at)
  if (startedAt === null) return null
  let endedAt: number | null = null
  if (v.ended_at !== null && v.ended_at !== undefined) {
    endedAt = time(v.ended_at)
    if (endedAt === null || endedAt < startedAt) return null // ended before it began: the Go contract refuses it too (wire.Observation.Validate)
  }
  return {
    id, via, signature, startedAt, endedAt,
    host: str(v.host) ? v.host : '',
    session: str(v.session) ? v.session : '',
    command: str(v.command) ? v.command : '',
    cpuSeconds: figure(v.cpu_seconds), cpuPeak: figure(v.cpu_peak), memPeakMb: figure(v.mem_peak_mb), procsPeak: figure(v.procs_peak),
  }
}

export function parseObservations(v: unknown): ObservationsPage | null {
  if (!isRec(v)) return null
  if (!str(v.host) || v.host === '') return null
  if (v.observations !== null && !Array.isArray(v.observations)) return null
  if (v.observations === undefined) return null
  const seen = new Set<string>()
  const observations: PoolObservation[] = []
  for (const row of (v.observations as unknown[] | null) ?? []) {
    const o = parseObservation(row)
    // A record that names another host than the answer is about does not belong here.
    if (o && (o.host === '' || o.host === v.host) && !seen.has(o.id)) { seen.add(o.id); observations.push(o) }
  }
  // The daemon's own "not known" answer says `known: false`; wwd's answer has no such field (known).
  const known = v.known !== false
  const nextUntil = str(v.next_until) && time(v.next_until) !== null ? v.next_until : null
  return { host: v.host, known, observations, truncated: v.truncated === true, nextUntil }
}

/**
 * `GET /api/pool/hosts/{alias}/observations?since=&until=` from the source daemon, which asks that host's wwd. `since` is RFC 3339; `until` is the previous page's `nextUntil`, as it was given.
 * Whether the daemon announces `pool.observations.v1` is the caller's to check.
 */
export async function fetchObservations(sourceHostId: string, alias: string, query: { since: string; until?: string }): Promise<ObservationsPage> {
  let path = `/api/pool/hosts/${encodeURIComponent(alias)}/observations?since=${encodeURIComponent(query.since)}`
  if (query.until) path += `&until=${encodeURIComponent(query.until)}`
  const page = parseObservations(await readJson(sourceHostId, path))
  // The answer must be about the host that was asked for: a wrong forward or a stale cache must not put another host's commands under this name.
  if (!page || page.host !== alias) throw unreadable(path)
  return page
}

export interface UndeclaredGroup {
  signature: string
  count: number
  /** The total over the group. */
  cpuSeconds: number
  /** The greatest peak in the group. */
  maxMemMb: number
  lastAt: number
  /** The latest one's session ref; `orphan` when none could be named. */
  lastSession: string
  /** `session` or `codex`; anything else stays as the word. */
  lastVia: string
  /** Redacted. */
  lastCommand: string
}

export interface UndeclaredPage {
  host: string
  /** false: that host's wwd predates the route, not an error. */
  known: boolean
  /** Biggest total CPU first, as wwd sorted them. */
  groups: UndeclaredGroup[]
  /** There were more groups than one answer carries: the biggest by CPU are here. */
  truncated: boolean
}

const finite = (v: unknown): v is number => num(v) && Number.isFinite(v) && v >= 0

/** Go: wire.UndeclaredResponse.Validate: a signature, a count above 0, `last_via` session or codex; the figures and the time have to be real, or the group is not what wwd wrote. */
function parseGroup(v: unknown): UndeclaredGroup | null {
  if (!isRec(v)) return null
  const { signature, last_via: via } = v
  if (!str(signature) || signature === '' || (via !== 'session' && via !== 'codex')) return null
  if (!finite(v.count) || v.count <= 0 || !Number.isInteger(v.count)) return null
  if (!finite(v.cpu_seconds) || !finite(v.max_mem_mb)) return null
  const lastAt = time(v.last_at)
  if (lastAt === null) return null
  return {
    signature, count: v.count, lastAt, lastVia: via,
    cpuSeconds: v.cpu_seconds, maxMemMb: v.max_mem_mb,
    lastSession: str(v.last_session) ? v.last_session : '', lastCommand: str(v.last_command) ? v.last_command : '',
  }
}

/**
 * The grouped read (`/undeclared`). Fail closed: one group that breaks the contract (the Go `Validate` refuses the whole answer) makes the page nothing, not a smaller page that reads as a whole one.
 * The one answer with no groups to read is the daemon's own `known: false` (an older wwd).
 */
export function parseUndeclared(v: unknown): UndeclaredPage | null {
  if (!isRec(v)) return null
  if (!str(v.host) || v.host === '') return null
  if (v.known === false) return { host: v.host, known: false, groups: [], truncated: false }
  if (v.groups !== null && !Array.isArray(v.groups)) return null
  if (v.groups === undefined) return null
  const groups: UndeclaredGroup[] = []
  for (const row of (v.groups as unknown[] | null) ?? []) {
    const g = parseGroup(row)
    if (!g) return null
    groups.push(g)
  }
  return { host: v.host, known: true, groups, truncated: v.truncated === true }
}

/**
 * `GET /api/pool/hosts/{alias}/undeclared?since=` from the source daemon, which asks that host's wwd. `since` is RFC 3339, passed on as given. Whether the daemon announces `pool.observations.v1` is the caller's to check.
 */
export async function fetchUndeclared(sourceHostId: string, alias: string, since: string): Promise<UndeclaredPage> {
  const path = `/api/pool/hosts/${encodeURIComponent(alias)}/undeclared?since=${encodeURIComponent(since)}`
  const page = parseUndeclared(await readJson(sourceHostId, path))
  // The answer must be about the host that was asked for.
  if (!page || page.host !== alias) throw unreadable(path)
  return page
}
