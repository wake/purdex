// spa/src/lib/devices-api.ts — the client of a daemon's device management routes (QR pairing spec §3.4; handler in
// internal/module/devices/handler.go). Admin token only, which is what `hostFetch` sends.
//
//   GET    /api/devices                    200 {devices: [row]}   (rows carry no token and no hash)
//   DELETE /api/devices?pairing_id=<uuid>  204 (idempotent: every device of the pairing, whether or not any was live)
//
// A 404 is a daemon without `devices.v1` (the routes do not exist): `unsupported`, not an error to retry — there is
// nothing to revoke on such a host, and nothing to list.
//
// NOTHING HERE THROWS OR REJECTS. Each exchange is raced against a 10 s timer (an AbortController + setTimeout, the
// host-transfer-api.ts pattern: fake timers cannot advance `AbortSignal.timeout`, and a transport that ignores its
// signal must still end on time). An unknown host id is never sent to: `hostFetch` falls back to another host's
// address for an id that is not in the store.

import { useHostStore } from '../stores/useHostStore'
import { hostFetch } from './host-api'

/** One device token row as the daemon lists it (times in ms; 0 = never). */
export interface DeviceRow {
  id: string
  pairing_id: string
  profile_id: string
  label: string
  created_at: number
  created_by: string
  use_by: number
  first_used_at: number
  last_used_at: number
  revoked_at: number
}

export type DevicesFailureReason = 'unsupported' | 'unauthorized' | 'network' | 'timeout' | 'malformed' | 'unknown_host'

export interface DevicesFailure {
  kind: 'failed'
  reason: DevicesFailureReason
  /** HTTP status; 0 when nothing was received. */
  status: number
}

export type ListResult = { kind: 'ok'; rows: DeviceRow[] } | DevicesFailure
export type RevokeResult = { kind: 'ok' } | { kind: 'unsupported' } | DevicesFailure

export const DEVICES_TIMEOUT_MS = 10_000

function fail(reason: DevicesFailureReason, status: number): DevicesFailure {
  return { kind: 'failed', reason, status }
}

function num(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : 0
}

function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

function parseRow(v: unknown): DeviceRow | null {
  if (typeof v !== 'object' || v === null || Array.isArray(v)) return null
  const r = v as Record<string, unknown>
  if (typeof r.id !== 'string' || r.id === '' || typeof r.pairing_id !== 'string' || r.pairing_id === '') return null
  return {
    id: r.id,
    pairing_id: r.pairing_id,
    profile_id: str(r.profile_id),
    label: str(r.label),
    created_at: num(r.created_at),
    created_by: str(r.created_by),
    use_by: num(r.use_by),
    first_used_at: num(r.first_used_at),
    last_used_at: num(r.last_used_at),
    revoked_at: num(r.revoked_at),
  }
}

type Raw = { kind: 'res'; status: number; text: string } | DevicesFailure

async function call(hostId: string, method: 'GET' | 'DELETE', path: string): Promise<Raw> {
  if (!useHostStore.getState().hosts[hostId]) return fail('unknown_host', 0)
  const controller = new AbortController()
  let timer: ReturnType<typeof setTimeout> | undefined
  const timedOut = new Promise<Raw>((resolve) => {
    timer = setTimeout(() => {
      controller.abort()
      resolve(fail('timeout', 0))
    }, DEVICES_TIMEOUT_MS)
  })
  const run = async (): Promise<Raw> => {
    let res: Response
    try {
      res = await hostFetch(hostId, path, { method, signal: controller.signal })
    } catch {
      return fail('network', 0)
    }
    try {
      return { kind: 'res', status: res.status, text: await res.text() }
    } catch {
      return fail('network', res.status)
    }
  }
  try {
    return await Promise.race([run(), timedOut])
  } finally {
    clearTimeout(timer)
  }
}

export async function listDevices(hostId: string): Promise<ListResult> {
  const r = await call(hostId, 'GET', '/api/devices')
  if (r.kind === 'failed') return r
  if (r.status === 404) return fail('unsupported', 404)
  if (r.status === 401 || r.status === 403) return fail('unauthorized', r.status)
  if (r.status !== 200) return fail('malformed', r.status)
  let json: unknown
  try {
    json = JSON.parse(r.text)
  } catch {
    return fail('malformed', 200)
  }
  const list = typeof json === 'object' && json !== null ? (json as { devices?: unknown }).devices : undefined
  if (!Array.isArray(list)) return fail('malformed', 200)
  const rows: DeviceRow[] = []
  for (const item of list) {
    const row = parseRow(item)
    if (row) rows.push(row)
  }
  return { kind: 'ok', rows }
}

/** Revoke every device token of a pairing on one host. Idempotent: 204 whether or not any was live. */
export async function revokePairing(hostId: string, pairingId: string): Promise<RevokeResult> {
  const r = await call(hostId, 'DELETE', `/api/devices?pairing_id=${encodeURIComponent(pairingId)}`)
  if (r.kind === 'failed') return r
  if (r.status === 204) return { kind: 'ok' }
  if (r.status === 404) return { kind: 'unsupported' }
  if (r.status === 401 || r.status === 403) return fail('unauthorized', r.status)
  return fail('malformed', r.status)
}
