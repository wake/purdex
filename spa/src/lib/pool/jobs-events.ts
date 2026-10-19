// spa/src/lib/pool/jobs-events.ts — the daemon's `jobs.changed` host event (wwd spec §14a): `{host, job_id, state}`, a notice that one job moved. It carries no job: the App reads that job again (`GET /api/pool/jobs/{id}`), which the driver does.
// Called from useMultiHostEventWs with the per-host closure's hostId, after its connection-key check. A frame that is not that shape is dropped.
import { jobsDriver } from './jobs-driver-live'

export const JOBS_EVENT_TYPE = 'jobs.changed'
export const isJobsEvent = (type: string): boolean => type === JOBS_EVENT_TYPE

export interface JobsChanged { host: string; jobId: string; state: string }

export function parseJobsChanged(value: unknown): JobsChanged | null {
  let body: unknown = value
  if (typeof value === 'string') {
    try { body = JSON.parse(value) } catch { return null }
  }
  if (typeof body !== 'object' || body === null || Array.isArray(body)) return null
  const r = body as Record<string, unknown>
  if (typeof r.host !== 'string' || r.host === '' || typeof r.job_id !== 'string' || r.job_id === '' || typeof r.state !== 'string') return null
  return { host: r.host, jobId: r.job_id, state: r.state }
}

export function handleJobsEvent(_hostId: string, value: unknown): void {
  const e = parseJobsChanged(value)
  if (e) void jobsDriver.jobChanged(e.host, e.jobId)
}
