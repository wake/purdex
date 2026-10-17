// spa/src/lib/relay/fresh.ts — the daemon's "handoff file written" list (relay v2 contract 5; capability `relay.half.v1`).
// A session whose relay ended `half` (the handoff file is written, the session was NOT cleared) stays on the host's list
// until it is cleared or ends. Two sources carry the same whole list: `GET /api/relay/fresh` (`{sessions:[…]}`) and the host
// event `relay.fresh` (`value` is a JSON string `{op:"snapshot"|"changed", sessions:[…]}`); both REPLACE the host's list.
//
// This is the trust boundary: an entry with a missing or ill-typed field is dropped alone (one warning), a frame that is
// not the envelope is dropped whole (the store keeps what it had — an empty list would read as "every handoff was cleared").
import { hostFetch } from '../host-api'

export const RELAY_FRESH_EVENT_TYPE = 'relay.fresh'
export const RELAY_FRESH_PATH = '/api/relay/fresh'

/** One session with a written handoff file. `code` is the session's tmux code (`/api/sessions`); absent when the session is not in tmux. */
export interface RelayFresh {
  session_id: string
  ref: string
  op_id: string
  path: string
  /** unix ms */
  written_at: number
  code?: string
}

export interface RelayFreshEvent {
  op: 'snapshot' | 'changed'
  sessions: RelayFresh[]
}

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

/** One wire entry as a `RelayFresh`, or null when a field is missing or ill-typed. `code` is optional: kept only as a non-empty string. */
export function parseRelayFreshEntry(v: unknown): RelayFresh | null {
  if (!isRecord(v)) return null
  const { session_id, ref, op_id, path, written_at, code } = v
  if (typeof session_id !== 'string' || session_id === '') return null
  if (typeof ref !== 'string' || typeof op_id !== 'string') return null
  if (typeof path !== 'string' || path === '') return null
  if (typeof written_at !== 'number' || !Number.isFinite(written_at)) return null
  return { session_id, ref, op_id, path, written_at, ...(typeof code === 'string' && code !== '' ? { code } : {}) }
}

/** The entries of a wire `sessions` array, a bad one dropped alone with one warning. */
function parseEntries(sessions: unknown[]): RelayFresh[] {
  const out: RelayFresh[] = []
  let dropped = 0
  for (const raw of sessions) {
    const e = parseRelayFreshEntry(raw)
    if (e) out.push(e)
    else dropped++
  }
  if (dropped > 0) console.warn(`[relay-fresh] dropped ${dropped} malformed entr${dropped === 1 ? 'y' : 'ies'}`)
  return out
}

/** The event's `value` (a JSON string, or already parsed) → the checked event, or the reason the frame is not one. */
export function parseRelayFreshEvent(value: unknown): RelayFreshEvent | string {
  let o: unknown = value
  if (typeof value === 'string') {
    try {
      o = JSON.parse(value)
    } catch {
      return 'value is not JSON'
    }
  }
  if (!isRecord(o)) return 'value is not an object'
  const { op, sessions } = o
  if (op !== 'snapshot' && op !== 'changed') return `unknown op ${JSON.stringify(op)}`
  if (!Array.isArray(sessions)) return `${op}: sessions is not an array`
  return { op, sessions: parseEntries(sessions) }
}

/** `GET /api/relay/fresh`: the body as the list, or null when it is not `{sessions: [...]}`. */
export function parseRelayFreshBody(body: unknown): RelayFresh[] | null {
  if (!isRecord(body) || !Array.isArray(body.sessions)) return null
  return parseEntries(body.sessions)
}

export async function fetchRelayFresh(hostId: string): Promise<RelayFresh[]> {
  const res = await hostFetch(hostId, RELAY_FRESH_PATH)
  if (!res.ok) throw new Error(`GET ${RELAY_FRESH_PATH}: ${res.status}`)
  const list = parseRelayFreshBody(await res.json())
  if (list === null) throw new Error(`GET ${RELAY_FRESH_PATH}: unreadable answer`)
  return list
}
