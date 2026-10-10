// spa/src/lib/workbook/api.ts — the daemon's workbook read routes (spec §9). Same transport and errors as the team
// routes (`send`, approval-api.ts: pinned to a configured host, never an active-host fallback). A 404 is a typed
// result (`not_found`: the conversation was never written, or an older daemon without the route), not a throw.
import { ApprovalApiError, send } from '../team/approval-api'
import { parseConversation, parseEntries, parseTodosPage } from './parse'
import { WORKBOOK_PROVIDER, type ConversationPage, type TodoState, type WorkbookEntry, type WorkbookTodo } from './types'

export interface PageQuery {
  /** Entries per page (the daemon defaults to 50, caps at 200). */
  limit?: number
  /** An entry id: the page holds the entries older than it. */
  before?: number
}

export type ConversationResult = { kind: 'ok'; page: ConversationPage } | { kind: 'not_found' }

function qs(params: Record<string, number | string | undefined>): string {
  const q = Object.entries(params).filter(([, v]) => v !== undefined).map(([k, v]) => `${k}=${encodeURIComponent(String(v))}`)
  return q.length ? `?${q.join('&')}` : ''
}

/** `GET /api/workbook/conversations/{provider}/{session_id}`; any session of the conversation names it. */
export async function fetchConversation(hostId: string, provider: string, sessionId: string, q: PageQuery = {}): Promise<ConversationResult> {
  let raw: unknown
  try {
    raw = await send<unknown>(hostId, `/api/workbook/conversations/${encodeURIComponent(provider)}/${encodeURIComponent(sessionId)}${qs({ limit: q.limit, before: q.before })}`, { method: 'GET' })
  } catch (e) {
    if (e instanceof ApprovalApiError && e.status === 404) return { kind: 'not_found' }
    throw e
  }
  const page = parseConversation(raw)
  if (!page) throw new ApprovalApiError(200, 'bad_response', 'the workbook conversation is not the wire shape')
  return { kind: 'ok', page }
}

export type TodosResult = { kind: 'ok'; todos: WorkbookTodo[] } | { kind: 'not_found' }
export type RefreshResult = { kind: 'accepted'; entryId: number } | { kind: 'not_live' } | { kind: 'refresh_pending' }

const convRoute = (sessionId: string): string => `/api/workbook/conversations/${encodeURIComponent(WORKBOOK_PROVIDER)}/${encodeURIComponent(sessionId)}`

/** v2 `GET …/todos?state=&limit=&before=` — the open list, or the done record paged newest first (`before` = a todo id). */
export async function fetchTodos(hostId: string, sessionId: string, q: { state: TodoState; limit?: number; before?: number }): Promise<TodosResult> {
  let raw: unknown
  try {
    raw = await send<unknown>(hostId, `${convRoute(sessionId)}/todos${qs({ state: q.state, limit: q.limit, before: q.before })}`, { method: 'GET' })
  } catch (e) {
    if (e instanceof ApprovalApiError && e.status === 404) return { kind: 'not_found' }
    throw e
  }
  const todos = parseTodosPage(raw)
  if (!todos) throw new ApprovalApiError(200, 'bad_response', 'the workbook todos are not the wire shape')
  return { kind: 'ok', todos }
}

/** v2 `POST …/refresh` — 202 `{entry_id}`; 409 `not_live` / `refresh_pending` are results, not errors (the caller decides
 *  what to tell the user). Everything else (network, 5xx, an unknown 409) still throws. */
export async function postRefresh(hostId: string, sessionId: string): Promise<RefreshResult> {
  let raw: unknown
  try {
    raw = await send<unknown>(hostId, `${convRoute(sessionId)}/refresh`, { method: 'POST' })
  } catch (e) {
    if (e instanceof ApprovalApiError && e.status === 409 && (e.code === 'not_live' || e.code === 'refresh_pending')) return e.code === 'not_live' ? { kind: 'not_live' } : { kind: 'refresh_pending' }
    throw e
  }
  const id = isRecord(raw) ? raw.entry_id : undefined
  if (typeof id !== 'number' || !Number.isSafeInteger(id) || id <= 0) throw new ApprovalApiError(202, 'bad_response', 'the refresh answer has no entry_id')
  return { kind: 'accepted', entryId: id }
}

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)

/** `GET /api/workbook/entries` — across the host's conversations. */
export async function fetchEntries(hostId: string, q: { since?: number; until?: number; thingDone?: boolean; limit?: number } = {}): Promise<WorkbookEntry[]> {
  const r = await send<{ entries?: unknown }>(hostId, `/api/workbook/entries${qs({ since: q.since, until: q.until, thing_done: q.thingDone ? 1 : undefined, limit: q.limit })}`, { method: 'GET' })
  return parseEntries(r?.entries)
}
