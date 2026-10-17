// spa/src/lib/journal/api.ts — the daemon's journal routes (daemon plan §8, J-1e). Same transport and errors as the
// workbook routes (`send`: pinned to a configured host, never an active-host fallback). Three answers are results, not
// errors: 404 (no unit yet, or an older daemon) → `none`; 409 `{lead_host_id, lead_session_id}` (this session is a member
// whose lead lives on another daemon) → `remote`; anything else that is not 2xx still throws an ApprovalApiError. A 200 that
// is not the wire shape throws `bad_response`. All four endpoints share these outcomes (the caller follows `remote`).
import { ApprovalApiError, send } from '../team/approval-api'
import { parseJournalPage, parseRemoteLead, parseSessions, parseRoadmap, parseTasks } from './parse'
import type { JournalPage, JournalSessions, JournalTask, RemoteLead, RoadmapFeature } from './types'

export type Outcome<T> = { kind: 'ok'; value: T } | { kind: 'none' } | ({ kind: 'remote' } & RemoteLead)

export const JOURNAL_PAGE_LIMIT = 50

const qs = (params: Record<string, number | string | undefined>): string => {
  const q = Object.entries(params).filter(([, v]) => v !== undefined).map(([k, v]) => `${k}=${encodeURIComponent(String(v))}`)
  return q.length ? `?${q.join('&')}` : ''
}

async function call<T>(hostId: string, path: string, init: RequestInit, parse: (raw: unknown) => T | null, what: string): Promise<Outcome<T>> {
  let raw: unknown
  try {
    raw = await send<unknown>(hostId, path, init)
  } catch (e) {
    if (e instanceof ApprovalApiError) {
      if (e.status === 404) return { kind: 'none' }
      if (e.status === 409) {
        const lead = parseRemoteLead(e.body)
        if (lead) return { kind: 'remote', ...lead }
      }
    }
    throw e
  }
  const value = parse(raw)
  if (value === null) throw new ApprovalApiError(200, 'bad_response', `the journal ${what} is not the wire shape`)
  return { kind: 'ok', value }
}

/** `GET /api/journal?session_id&before&limit` — `before` is the cursor the caller built from the oldest entry it holds (`<month>:<id>`). */
export function getJournal(hostId: string, sessionId: string, before?: string, limit: number = JOURNAL_PAGE_LIMIT): Promise<Outcome<JournalPage>> {
  return call(hostId, `/api/journal${qs({ session_id: sessionId, before, limit })}`, { method: 'GET' }, parseJournalPage, 'page')
}

/**
 * `GET /api/journal?session_id[&page]` (journal.sessions.v1): one leader session's entries. Without `page` it is the current session.
 * NEVER add `limit` or `before` here: the daemon takes a request with either (and no `page`) for an old before-paging read and answers
 * that instead of a session page.
 */
export function getJournalPage(hostId: string, sessionId: string, page?: string): Promise<Outcome<JournalPage>> {
  return call(hostId, `/api/journal${qs({ session_id: sessionId, page })}`, { method: 'GET' }, parseJournalPage, 'session page')
}

/** `GET /api/journal/sessions?session_id` — the leader's chain over relay and /clear, oldest first (journal.sessions.v1). */
export function getJournalSessions(hostId: string, sessionId: string): Promise<Outcome<JournalSessions>> {
  return call(hostId, `/api/journal/sessions${qs({ session_id: sessionId })}`, { method: 'GET' }, parseSessions, 'sessions')
}

export function getTasks(hostId: string, sessionId: string): Promise<Outcome<JournalTask[]>> {
  return call(hostId, `/api/journal/tasks${qs({ session_id: sessionId })}`, { method: 'GET' }, parseTasks, 'tasks')
}

export function getRoadmap(hostId: string, sessionId: string): Promise<Outcome<RoadmapFeature[]>> {
  return call(hostId, `/api/journal/roadmap${qs({ session_id: sessionId })}`, { method: 'GET' }, parseRoadmap, 'roadmap')
}

/** `PUT /api/journal/confirm` — check (`done`) or uncheck item `idx` of a 待確認 by hand (journal.confirm.v1). The answer's body is not read (204). */
export function setConfirmItem(hostId: string, sessionId: string, entryId: string, idx: number, done: boolean): Promise<Outcome<true>> {
  return call(hostId, '/api/journal/confirm', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ session_id: sessionId, entry_id: entryId, idx, done }),
  }, () => true as const, 'confirm answer')
}
