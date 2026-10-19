// spa/src/lib/pool/jobs-control-api.ts — what the App does to a job (`jobs.control.v1`, internal/module/jobs/control.go; admin token only — a device token answers 403): cancel it, or set its manual place. Nothing here is cached: the job
// store's next read (`jobs.changed`) decides what the job now is. A refusal throws the PoolChangeError of control-api (the classification and wwd's own words).
import { send } from './control-api'

export const JOBS_CONTROL_CAPABILITY = 'jobs.control.v1'

/** What the daemon takes as a place; `normal` gives the score back the say (the job's `manual` is then `''`). */
export type PositionChoice = 'front' | 'back' | 'hold' | 'normal'

const jobPath = (id: string) => `/api/pool/jobs/${encodeURIComponent(id)}`

/** `POST /api/pool/jobs/{id}/cancel?host=<origin>`: the daemon's wwd cancels it, or forwards the cancel to the origin's wwd. `host` is always sent: it is one clear value, and a daemon that is not the origin's forwards. */
export async function cancelJob(daemonId: string, origin: string, id: string): Promise<void> {
  await send(daemonId, 'POST', `${jobPath(id)}/cancel?host=${encodeURIComponent(origin)}`)
}

/**
 * `POST /api/pool/jobs/{id}/position`: only to the origin's own daemon. `host` names the origin all the same: a daemon that is not that host refuses (400 `host_not_local`) instead of changing a job of another host's queue,
 * so a wrong idea of which daemon is whose cannot become a change on the wrong host.
 */
export async function setJobPosition(daemonId: string, origin: string, id: string, position: PositionChoice): Promise<void> {
  await send(daemonId, 'POST', `${jobPath(id)}/position?host=${encodeURIComponent(origin)}`, { position })
}
