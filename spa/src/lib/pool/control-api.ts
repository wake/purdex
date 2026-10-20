// spa/src/lib/pool/control-api.ts — the changes of `pool.control.v1`: a host's worker settings (one whole `PUT`, no CAS in 7b1: two Apps
// changing it at once, the last one wins) and its time-bounded 「暫停」 (`POST` / `DELETE …/exceptions`). Nothing here is cached: the next
// facts decide what the host now has. A refusal throws a PoolChangeError carrying the classification and wwd's own words.
import { hostFetch } from '../host-api'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { classify, type PoolError } from './errors'
import { readJson } from './api'
import { parseExceptionList } from './exceptions'
import type { PoolExceptionDetail, PoolHold, PoolWorker } from './types'

export const CONTROL_CAPABILITY = 'pool.control.v1'

export class PoolChangeError extends Error {
  readonly status: number | null
  readonly poolError: PoolError
  /** What the daemon said about it (`detail`): wwd's reason for a refusal, else ''. */
  readonly detail: string

  constructor(method: string, path: string, status: number | null, poolError: PoolError, detail: string) {
    super(`${method} ${path}: ${status ?? 'no answer'} (${poolError.code})`)
    this.status = status
    this.poolError = poolError
    this.detail = detail
  }
}

/** The daemons that have answered 403 to a change: they gave this App a device token, which can read and cannot change. Remembered for the session. */
const denied = new Set<string>()
export const controlDenied = (hostId: string): boolean => denied.has(hostId)
/** Tests only. */
export const clearControlDenied = (): void => { denied.clear() }

/** The connected daemon a change goes to: the one the pool is read from when it announces `pool.control.v1`, else the first that does (host order). */
export function controlSource(preferred: string | null): string | null {
  const { byHost } = useNexHostStore.getState()
  const { hostOrder, runtime } = useHostStore.getState()
  const can = (id: string) => runtime[id]?.status === 'connected' && byHost[id]?.daemonCapabilities?.includes(CONTROL_CAPABILITY) === true
  if (preferred !== null && can(preferred)) return preferred
  return hostOrder.find(can) ?? null
}

export async function send(hostId: string, method: string, path: string, body?: unknown): Promise<unknown> {
  let res: Response
  try {
    res = await hostFetch(hostId, path, {
      method,
      ...(body === undefined ? {} : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }),
    })
  } catch {
    throw new PoolChangeError(method, path, null, classify(null, null, 'change'), '')
  }
  let text = ''
  try { text = await res.text() } catch { /* a body that cannot be read is classified by its status */ }
  if (!res.ok) {
    let parsed: unknown = null
    try { parsed = JSON.parse(text) } catch { /* a body that is not JSON is classified by its status */ }
    const detail = typeof parsed === 'object' && parsed !== null && typeof (parsed as { detail?: unknown }).detail === 'string' ? (parsed as { detail: string }).detail : ''
    const err = new PoolChangeError(method, path, res.status, classify(res.status, parsed, 'change'), detail)
    if (err.poolError.action === 'read_only') denied.add(hostId)
    throw err
  }
  try { return JSON.parse(text) } catch { return null }
}

const hostPath = (alias: string) => `/api/pool/hosts/${encodeURIComponent(alias)}`

/** `PUT /api/pool/hosts/{alias}/worker`: the whole settings object. */
export async function putWorker(hostId: string, alias: string, worker: PoolWorker): Promise<void> {
  await send(hostId, 'PUT', `${hostPath(alias)}/worker`, { enabled: worker.enabled, slots: { heavy: worker.slots.heavy, light: worker.slots.light }, owner_rule: worker.owner_rule })
}

/** `POST …/exceptions`: a pause bounded by a time (`until`, ms since epoch) — the only kind the App adds — with the classes it lets through and its marks when there are any. */
export async function postPause(hostId: string, alias: string, name: string, untilMs: number, holds: PoolHold[], extra: { allow?: { classes: string[]; repos: string[] }; flags?: string[] } = {}): Promise<void> {
  const body: Record<string, unknown> = { name, until: new Date(untilMs).toISOString(), pid: null, holds }
  if (extra.allow !== undefined) body.allow = extra.allow // an `allow` that is present means "only these": never send an empty one
  if (extra.flags !== undefined && extra.flags.length > 0) body.flags = extra.flags
  await send(hostId, 'POST', `${hostPath(alias)}/exceptions`, body)
}

/** `DELETE …/exceptions/{name}`. */
export async function deletePause(hostId: string, alias: string, name: string): Promise<void> {
  await send(hostId, 'DELETE', `${hostPath(alias)}/exceptions/${encodeURIComponent(name)}`)
}

/** `GET …/exceptions`: the pauses with their holds. null is "not known": an older host answers 200 with `known:false` (or 404), and any other failure of the read (wwd_rejected 404, wwd_error 502, wwd_unavailable 503) is the same to the page. */
export async function fetchPauses(hostId: string, alias: string): Promise<PoolExceptionDetail[] | null> {
  try { return parseExceptionList(await readJson(hostId, `${hostPath(alias)}/exceptions`)) } catch { return null }
}
