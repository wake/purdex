// spa/src/lib/fleet/probe-check.ts — the connection check of a probe host (fleet spec §6 `POST /api/fleet/probe-check`, §8.2):
// ask ONE daemon host to reach the host through its own SSH alias once. Admin-only route, called through `hostFetch` like
// config-api's admin routes. The answer's marks and error code become the words of the check line (`checkMarks`).
import { hostFetch } from '../host-api'
import { ERROR_CODES, type FleetErrorCode } from './types'

export type CheckMark = 'ok' | 'failed' | 'unknown'
export interface ProbeCheckAnswer { ssh: CheckMark; measure: CheckMark; error: FleetErrorCode | null }
export type ProbeCheckOutcome =
  | { kind: 'answer'; answer: ProbeCheckAnswer }
  /** 404: a daemon before F-2c. */
  | { kind: 'unsupported' }
  | { kind: 'error'; text: string }

const mark = (v: unknown): CheckMark => (v === 'ok' || v === 'failed' ? v : 'unknown')
const code = (v: unknown): FleetErrorCode | null => ((ERROR_CODES as readonly unknown[]).includes(v) ? (v as FleetErrorCode) : null)

export async function runProbeCheck(hostId: string, sshAlias: string): Promise<ProbeCheckOutcome> {
  try {
    const res = await hostFetch(hostId, '/api/fleet/probe-check', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ ssh_alias: sshAlias }),
    })
    if (res.status === 404) return { kind: 'unsupported' }
    if (!res.ok) return { kind: 'error', text: `${res.status}` }
    const b: unknown = await res.json()
    const o = typeof b === 'object' && b !== null ? (b as Record<string, unknown>) : {}
    return { kind: 'answer', answer: { ssh: mark(o.ssh), measure: mark(o.measure), error: code(o.error) } }
  } catch (err) {
    return { kind: 'error', text: err instanceof Error ? err.message : String(err) }
  }
}

export interface MarkView { mark: CheckMark; reasonKey: string | null }
export interface CheckView { ssh: MarkView; measure: MarkView; /** One reason for both marks (the host could not be reached). */ note: string | null }

const SSH_REASON: Partial<Record<FleetErrorCode, string>> = {
  auth: 'fleet_hosts.check.auth',
  host_key: 'fleet_hosts.check.host_key',
  alias_gone: 'fleet_hosts.check.alias_gone',
}

/** The two marks and the reason words of one answer. `unknown` stays unknown: it is never shown as a pass. */
export function checkMarks(a: ProbeCheckAnswer): CheckView {
  const unreached = a.ssh === 'unknown' && a.measure === 'unknown' && (a.error === 'timeout' || a.error === 'unreachable')
  return {
    ssh: { mark: a.ssh, reasonKey: a.ssh === 'failed' ? (a.error && SSH_REASON[a.error]) || 'fleet_hosts.check.ssh_failed' : null },
    measure: { mark: a.measure, reasonKey: a.measure === 'failed' ? 'fleet_hosts.check.no_reading' : null },
    note: unreached ? 'fleet_hosts.check.unreachable' : null,
  }
}
