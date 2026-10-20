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
    if (endedAt === null) return null
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
    if (o && !seen.has(o.id)) { seen.add(o.id); observations.push(o) }
  }
  // The daemon's own "not known" answer says `known: false`; wwd's answer has no such field (known).
  const known = v.known !== false
  const nextUntil = str(v.next_until) && time(v.next_until) !== null ? v.next_until : null
  return { known, observations, truncated: v.truncated === true, nextUntil }
}

/**
 * `GET /api/pool/hosts/{alias}/observations?since=&until=` from the source daemon, which asks that host's wwd. `since` is RFC 3339; `until` is the previous page's `nextUntil`, as it was given.
 * Whether the daemon announces `pool.observations.v1` is the caller's to check.
 */
export async function fetchObservations(sourceHostId: string, alias: string, query: { since: string; until?: string }): Promise<ObservationsPage> {
  let path = `/api/pool/hosts/${encodeURIComponent(alias)}/observations?since=${encodeURIComponent(query.since)}`
  if (query.until) path += `&until=${encodeURIComponent(query.until)}`
  const page = parseObservations(await readJson(sourceHostId, path))
  if (!page) throw unreadable(path)
  return page
}
