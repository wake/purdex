// spa/src/lib/pool/jobs-api.ts — reads of `/api/pool/jobs*` from one daemon (`jobs.v1`, internal/module/jobs): the list page, one job with its log, a full log. A refused read throws a PoolReadError carrying the classification
// (errors.ts); an answer that parses as nothing is outside the contract (wwd_bad_response). `host` names another host's jobs: the daemon forwards through its wwd (the peer view, without fast-lane or ad-hoc jobs);
// left out, it is the daemon's own.
import { readJson, readText, unreadable } from './api'
import { parseJobDetail, parseJobsPage } from './jobs-parse'
import type { JobDetail, JobsPage } from './jobs-types'

export interface JobsQuery {
  host?: string
  state?: string
  session?: string
  /** A cursor from an earlier page (`0` reads from the start). */
  after?: string
  limit?: number
}

const withHost = (path: string, host?: string): string => (host ? `${path}?host=${encodeURIComponent(host)}` : path)

/** `GET /api/pool/jobs`. The query is written in a fixed order. */
export async function fetchJobsPage(daemonId: string, q: JobsQuery): Promise<JobsPage> {
  const params = new URLSearchParams()
  if (q.host) params.set('host', q.host)
  if (q.state) params.set('state', q.state)
  if (q.session) params.set('session', q.session)
  if (q.after !== undefined) params.set('after', q.after)
  if (q.limit !== undefined) params.set('limit', String(q.limit))
  const qs = params.toString()
  const path = qs === '' ? '/api/pool/jobs' : `/api/pool/jobs?${qs}`
  const page = parseJobsPage(await readJson(daemonId, path))
  if (!page) throw unreadable(path)
  return page
}

/** `GET /api/pool/jobs/{id}`: the job and its stored log (null until stored, and when over 1 MiB — then `fetchJobLog`). */
export async function fetchJob(daemonId: string, id: string, host?: string): Promise<JobDetail> {
  const path = withHost(`/api/pool/jobs/${encodeURIComponent(id)}`, host)
  const detail = parseJobDetail(await readJson(daemonId, path))
  if (!detail) throw unreadable(path)
  return detail
}

/** `GET /api/pool/jobs/{id}/log`: the whole stored log as text. */
export function fetchJobLog(daemonId: string, id: string, host?: string): Promise<string> {
  return readText(daemonId, withHost(`/api/pool/jobs/${encodeURIComponent(id)}/log`, host))
}
