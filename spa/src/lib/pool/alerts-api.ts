// spa/src/lib/pool/alerts-api.ts — a host's alerts (`pool.alerts.v1`, `GET /api/pool/hosts/{alias}/alerts`; Go: internal/module/pool/alerts.go, wire.Alert plus `silence` on a silence). The trust boundary: a row that lacks
// what names it (key, kind, level, since, last) or whose time is broken is dropped, the others stay; a kind or level the App does not know stays as the word. The envelope must be `{host, known, alerts}` or the whole answer is null.
import { readJson, unreadable } from './api'
import { isRec, str, time } from './parse'

export type SilenceKind = 'wwd_down' | 'host_down' | 'unknown'
const SILENCE_KINDS: readonly string[] = ['wwd_down', 'host_down', 'unknown']

export interface PoolAlert {
  key: string
  /** The rule that raised it; a kind the App does not know stays as the word. */
  kind: string
  /** `push` or `digest`; anything else stays as the word and is not a push (M36 counts `push` only). */
  level: string
  since: number
  last: number
  /** When it stopped (ms); null: still active. */
  clearedAt: number | null
  detail: string
  /** Only on a silence. */
  silence?: SilenceKind
}

export interface HostAlerts {
  host: string
  /** false: that host's wwd predates the route (no alerts yet), not an error. */
  known: boolean
  alerts: PoolAlert[]
}

function parseAlert(v: unknown): PoolAlert | null {
  if (!isRec(v)) return null
  const { key, kind, level } = v
  if (!str(key) || key === '' || !str(kind) || kind === '' || !str(level) || level === '') return null
  const since = time(v.since)
  const last = time(v.last)
  if (since === null || last === null) return null
  let clearedAt: number | null = null
  if (v.cleared_at !== null && v.cleared_at !== undefined) {
    clearedAt = time(v.cleared_at)
    if (clearedAt === null) return null
  }
  const out: PoolAlert = { key, kind, level, since, last, clearedAt, detail: str(v.detail) ? v.detail : '' }
  // A silence the daemon could not tell apart (`partner_down`, a writer of another release) is `unknown`, as the Go side names it.
  if (kind === 'silence') out.silence = str(v.silence) && SILENCE_KINDS.includes(v.silence) ? (v.silence as SilenceKind) : 'unknown'
  return out
}

export function parseHostAlerts(v: unknown): HostAlerts | null {
  if (!isRec(v)) return null
  if (!str(v.host) || v.host === '' || typeof v.known !== 'boolean') return null
  if (v.alerts !== null && !Array.isArray(v.alerts)) return null
  const alerts: PoolAlert[] = []
  for (const row of (v.alerts as unknown[] | null) ?? []) {
    const a = parseAlert(row)
    if (a) alerts.push(a)
  }
  return { host: v.host, known: v.known, alerts }
}

/** `GET /api/pool/hosts/{alias}/alerts` from the source daemon, which asks that host's wwd. */
export async function fetchHostAlerts(sourceHostId: string, alias: string): Promise<HostAlerts> {
  const path = `/api/pool/hosts/${encodeURIComponent(alias)}/alerts`
  const parsed = parseHostAlerts(await readJson(sourceHostId, path))
  if (!parsed) throw unreadable(path)
  return parsed
}
