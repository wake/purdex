// spa/src/lib/host-transfer-api.ts — the client of the relay daemon's host transfer routes (host ownership spec
// §6.2 / §6.3; H4b). Status → body, as written by internal/module/hosttransfer/handler.go (every answer of the
// module is JSON `{reason}` except the two 200s):
//
//   POST /api/host-transfer          body {hosts: [...]}
//     200 {code, expiresAt}  400 bad_payload  413 too_large  429 capacity  503 unavailable
//   POST /api/host-transfer/redeem   body {code}
//     200 {hosts: [...]}     400 bad_request  404 invalid_code  429 rate_limited + Retry-After  503 unavailable
//   both: 403 no_token (the relay has no token at all), 401 unauthorized.
//
// A 404 whose body is not `{"reason":"invalid_code"}` is a daemon without the routes (older than H4a):
// `unsupported`, never "invalid code".
//
// NOTHING HERE THROWS OR REJECTS; every call resolves to a discriminated union. Timeouts are an AbortController +
// setTimeout raced against the exchange — the reason is profile/api.ts's (fake timers cannot advance
// `AbortSignal.timeout`, and a transport that ignores its signal must still end on time).
//
// AN UNKNOWN RELAY IS NEVER SENT TO: `hostFetch` falls back to another host's address for an id that is not in the
// store, which here would hand the shared tokens to the wrong daemon.

import { useHostStore } from '../stores/useHostStore'
import type { HostConfig } from '../stores/useHostStore'
import { hostFetch } from './host-api'

/** The look fields a payload row may carry (spec §6.4.1). */
export type TransferLook = Partial<Pick<HostConfig, 'colors' | 'color' | 'icon' | 'iconWeight'>>

/** One payload row (spec §6.4.1). */
export interface TransferRow {
  name: string
  ip: string
  port: number
  token: string
  daemonId?: string
  look?: TransferLook
}

export type TransferFailureReason =
  | 'bad_payload' //  create 400
  | 'bad_request' //  redeem 400
  | 'too_large' //    413
  | 'capacity' //     create 429: too many open codes on the relay
  | 'rate_limited' // redeem 429: wait `retryAfterS`
  | 'invalid_code' // redeem 404: unknown, expired or already used
  | 'unavailable' //  503 (also a redeem after the relay's store stopped)
  | 'unauthorized' // 401
  | 'no_token' //     403: the relay daemon has no token, so it refuses to hold credentials
  | 'unsupported' //  404 without the module's body: the relay is older than H4a
  | 'network' //      fetch rejected / body failed to arrive
  | 'timeout'
  | 'malformed' //    any other status, or a 200 that is not what the protocol says
  | 'unknown_host' // the relay id is not in the host store; nothing was sent

export interface TransferFailure {
  kind: 'failed'
  reason: TransferFailureReason
  /** HTTP status; 0 when nothing was received. */
  status: number
  /** Only with `rate_limited` (from `Retry-After`, when present). */
  retryAfterS?: number
}

export type CreateResult = { kind: 'ok'; code: string; expiresAt: number } | TransferFailure
export type RedeemResult = { kind: 'ok'; hosts: unknown[] } | TransferFailure

export const TRANSFER_TIMEOUT_MS = 10_000

const COMMON: Record<number, TransferFailureReason> = {
  401: 'unauthorized',
  403: 'no_token',
  413: 'too_large',
  503: 'unavailable',
}
const CREATE_STATUS: Record<number, TransferFailureReason> = { ...COMMON, 400: 'bad_payload', 429: 'capacity' }
const REDEEM_STATUS: Record<number, TransferFailureReason> = { ...COMMON, 400: 'bad_request', 429: 'rate_limited' }

function fail(reason: TransferFailureReason, status: number): TransferFailure {
  return { kind: 'failed', reason, status }
}

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function parseJson(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    return undefined
  }
}

async function exchange<T>(
  relayHostId: string,
  path: string,
  body: unknown,
  statusMap: Record<number, TransferFailureReason>,
  on200: (json: unknown) => T | null,
  on404: (json: unknown) => TransferFailureReason,
): Promise<T | TransferFailure> {
  if (!useHostStore.getState().hosts[relayHostId]) return fail('unknown_host', 0)
  const controller = new AbortController()
  let timer: ReturnType<typeof setTimeout> | undefined
  const timedOut = new Promise<TransferFailure>((resolve) => {
    timer = setTimeout(() => {
      controller.abort()
      resolve(fail('timeout', 0))
    }, TRANSFER_TIMEOUT_MS)
  })
  const run = async (): Promise<T | TransferFailure> => {
    let res: Response
    try {
      res = await hostFetch(relayHostId, path, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        signal: controller.signal,
      })
    } catch {
      return fail('network', 0)
    }
    let text: string
    try {
      text = await res.text()
    } catch {
      return fail('network', res.status)
    }
    const json = parseJson(text)
    if (res.status === 200) return on200(json) ?? fail('malformed', 200)
    if (res.status === 404) return fail(on404(json), 404)
    const reason = statusMap[res.status]
    if (!reason) return fail('malformed', res.status)
    const out = fail(reason, res.status)
    if (reason === 'rate_limited') {
      const s = Number(res.headers.get('Retry-After'))
      if (Number.isInteger(s) && s > 0) out.retryAfterS = s
    }
    return out
  }
  try {
    return await Promise.race([run(), timedOut])
  } finally {
    clearTimeout(timer)
  }
}

/** Parks `rows` on the relay under a fresh code (spec §6.2). */
export function createTransfer(relayHostId: string, rows: TransferRow[]): Promise<CreateResult> {
  return exchange<CreateResult>(
    relayHostId,
    '/api/host-transfer',
    { hosts: rows },
    CREATE_STATUS,
    (json) => {
      if (!isPlainObject(json)) return null
      const { code, expiresAt } = json
      if (typeof code !== 'string' || code === '' || typeof expiresAt !== 'number' || !Number.isFinite(expiresAt)) return null
      return { kind: 'ok', code, expiresAt }
    },
    () => 'unsupported',
  )
}

/** Trades `code` (as typed; the daemon normalises it) for the parked rows, once (spec §6.3). */
export function redeemTransfer(relayHostId: string, code: string): Promise<RedeemResult> {
  return exchange<RedeemResult>(
    relayHostId,
    '/api/host-transfer/redeem',
    { code },
    REDEEM_STATUS,
    (json) => (isPlainObject(json) && Array.isArray(json.hosts) ? { kind: 'ok', hosts: json.hosts } : null),
    (json) => (isPlainObject(json) && json.reason === 'invalid_code' ? 'invalid_code' : 'unsupported'),
  )
}

/** `ABCD2345` → `ABCD-2345`; anything that is not eight characters is shown as is. */
export function formatTransferCode(code: string): string {
  return code.length === 8 ? `${code.slice(0, 4)}-${code.slice(4)}` : code
}
