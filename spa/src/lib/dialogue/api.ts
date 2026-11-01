// spa/src/lib/dialogue/api.ts — the daemon's dialogue routes (plan T11). Same transport and errors as the team routes
// (`send`, approval-api.ts: pinned to a configured host, never an active-host fallback); every failure is an
// `ApprovalApiError` whose `code` is the daemon's error code. Answers are shape-checked minimally: the UI never sees an
// undefined array or counter.
import { ApprovalApiError, send } from '../team/approval-api'
import type { Recap, RecapsResponse, RecapState, RecapTrigger, TurnRange, TurnSummary } from './types'

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const str = (v: unknown): string => (typeof v === 'string' ? v : '')
const num = (v: unknown): number => (typeof v === 'number' && Number.isFinite(v) ? v : 0)
const strings = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [])

function parseRecap(v: unknown): Recap | null {
  if (!isRecord(v) || typeof v.id !== 'number' || !Number.isFinite(v.id)) return null
  return {
    id: v.id, at: num(v.at), from_no: num(v.from_no), to_no: num(v.to_no), trigger: str(v.trigger), headline: str(v.headline),
    done: strings(v.done), now: str(v.now), decided: strings(v.decided), waiting: strings(v.waiting), next: str(v.next),
  }
}

function parseTurnSummary(v: unknown): TurnSummary | null {
  if (!isRecord(v) || typeof v.turn_no !== 'number' || !Number.isFinite(v.turn_no)) return null
  return { turn_no: v.turn_no, at: num(v.at), title: str(v.title), summary: str(v.summary), push: str(v.push) }
}

function qs(params: Record<string, number | undefined>): string {
  const q = Object.entries(params).filter(([, v]) => v !== undefined).map(([k, v]) => `${k}=${v}`)
  return q.length ? `?${q.join('&')}` : ''
}

const route = (sessionId: string): string => `/api/dialogue/${encodeURIComponent(sessionId)}`

/** `GET /api/dialogue/{sid}/recaps?limit=` — newest first, plus the three flags the UI reads its state from. */
export async function fetchRecaps(hostId: string, sessionId: string, limit?: number): Promise<RecapsResponse> {
  const raw = await send<unknown>(hostId, `${route(sessionId)}/recaps${qs({ limit })}`, { method: 'GET' })
  if (!isRecord(raw)) throw new ApprovalApiError(200, 'bad_response', 'the recaps answer is not the wire shape')
  const recaps = Array.isArray(raw.recaps) ? raw.recaps.map(parseRecap).filter((r): r is Recap => r !== null) : []
  return { recaps, can_generate: raw.can_generate === true, newest_turn_no: num(raw.newest_turn_no), last_recap_to_no: num(raw.last_recap_to_no) }
}

/** `GET /api/dialogue/{sid}/turn-summaries?from=&to=&limit=` — done summaries, oldest first. */
export async function fetchTurnSummaries(hostId: string, sessionId: string, range: TurnRange = {}): Promise<TurnSummary[]> {
  const raw = await send<unknown>(hostId, `${route(sessionId)}/turn-summaries${qs({ from: range.from, to: range.to, limit: range.limit })}`, { method: 'GET' })
  if (!isRecord(raw)) throw new ApprovalApiError(200, 'bad_response', 'the turn summaries answer is not the wire shape')
  return Array.isArray(raw.turn_summaries) ? raw.turn_summaries.map(parseTurnSummary).filter((t): t is TurnSummary => t !== null) : []
}

const STATES: readonly string[] = ['queued', 'up_to_date', 'unavailable']

/** `POST /api/dialogue/{sid}/recap` — asks for a recap; the answer says whether one was queued. */
export async function requestRecap(hostId: string, sessionId: string, trigger: RecapTrigger): Promise<RecapState> {
  const raw = await send<unknown>(hostId, `${route(sessionId)}/recap`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ trigger }),
  })
  const state = isRecord(raw) ? raw.state : undefined
  if (typeof state !== 'string' || !STATES.includes(state)) throw new ApprovalApiError(200, 'bad_response', 'the recap answer has no known state')
  return state as RecapState
}
