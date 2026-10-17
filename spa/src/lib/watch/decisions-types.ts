// spa/src/lib/watch/decisions-types.ts — the shape of `GET /api/gate/decisions` (internal/gate/decision.go `Decision`), read
// leniently: a record without a parsable time is dropped; a field this App does not know is ignored, and a usage field that
// is absent (a record before 743, or a command that never started) stays undefined.
export interface GateAppliedParam { value: string; source: string }

export interface GateDecision {
  /** RFC 3339: when the gate started waiting (the command started at `time + wait_s`). */
  time: string
  argv: string[]
  cwd?: string
  session?: string
  class?: string
  /** slot | no-slot | off | nested | refused | interrupted | orphaned | packaging | none */
  outcome: string
  waitS: number
  waitReasons: string[]
  applied: Record<string, GateAppliedParam>
  exit: number
  durationS: number
  cpuS?: number
  maxRssMb?: number
  majorFaults?: number
}

export interface GateDecisions { records: GateDecision[]; skipped: number }

const isObj = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const num = (v: unknown): number | undefined => (typeof v === 'number' && Number.isFinite(v) && v >= 0 ? v : undefined)
const str = (v: unknown): string | undefined => (typeof v === 'string' && v !== '' ? v : undefined)
const strings = (v: unknown): string[] => (Array.isArray(v) ? v.filter((a): a is string => typeof a === 'string') : [])

function parseRecord(raw: unknown): GateDecision | null {
  if (!isObj(raw) || typeof raw.time !== 'string' || Number.isNaN(Date.parse(raw.time))) return null
  const applied: Record<string, GateAppliedParam> = {}
  if (isObj(raw.applied)) {
    for (const [k, v] of Object.entries(raw.applied)) {
      if (isObj(v) && typeof v.value === 'string') applied[k] = { value: v.value, source: typeof v.source === 'string' ? v.source : '' }
    }
  }
  return {
    time: raw.time,
    argv: strings(raw.argv),
    cwd: str(raw.cwd), session: str(raw.session), class: str(raw.class),
    outcome: typeof raw.outcome === 'string' ? raw.outcome : '',
    waitS: num(raw.wait_s) ?? 0,
    waitReasons: strings(raw.wait_reasons),
    applied,
    exit: typeof raw.exit === 'number' && Number.isFinite(raw.exit) ? raw.exit : 0,
    durationS: num(raw.duration_s) ?? 0,
    cpuS: num(raw.cpu_s), maxRssMb: num(raw.max_rss_mb), majorFaults: num(raw.major_faults),
  }
}

/** null = unreadable (no `records` array). `skipped` is the daemon's count of lines it could not parse. */
export function parseGateDecisions(raw: unknown): GateDecisions | null {
  if (!isObj(raw) || !Array.isArray(raw.records)) return null
  return { records: raw.records.map(parseRecord).filter((r): r is GateDecision => r !== null), skipped: num(raw.skipped) ?? 0 }
}
