// spa/src/lib/pairing-transport.ts — pairing's HTTP: one raced-against-a-timer call, the create-status table and the
// best-effort revoke (moved verbatim out of pairing.ts, #2238).
import { pinnedHostFetch } from './host-api'
import { PAIRING_HTTP_TIMEOUT_MS, type PairingFailureReason } from './pairing-types'

export type Raw = { kind: 'res'; status: number; json: unknown } | { kind: 'failed'; reason: 'network' | 'timeout' }

export async function call(hostId: string, method: string, path: string, body?: unknown): Promise<Raw> {
  const controller = new AbortController()
  let timer: ReturnType<typeof setTimeout> | undefined
  const timedOut = new Promise<Raw>((resolve) => {
    timer = setTimeout(() => {
      controller.abort()
      resolve({ kind: 'failed', reason: 'timeout' })
    }, PAIRING_HTTP_TIMEOUT_MS)
  })
  const run = async (): Promise<Raw> => {
    let res: Response
    try {
      res = await pinnedHostFetch(hostId, path, {
        method,
        ...(body !== undefined ? { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) } : {}),
        signal: controller.signal,
      })
    } catch {
      return { kind: 'failed', reason: 'network' }
    }
    let json: unknown
    try {
      const text = await res.text()
      try {
        json = text === '' ? undefined : JSON.parse(text)
      } catch {
        json = undefined
      }
    } catch {
      return { kind: 'failed', reason: 'network' }
    }
    return { kind: 'res', status: res.status, json }
  }
  try {
    return await Promise.race([run(), timedOut])
  } finally {
    clearTimeout(timer)
  }
}

export function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

export const CREATE_STATUS: Record<number, PairingFailureReason> = {
  400: 'bad_payload',
  401: 'unauthorized',
  403: 'no_token',
  404: 'unsupported',
  413: 'too_large',
  429: 'capacity',
  503: 'unavailable',
}

/** Best-effort `DELETE /api/devices?pairing_id=` on each host; the hosts it could not revoke on. */
export async function revokeHosts(hostIds: readonly string[], pairingId: string): Promise<string[]> {
  const results = await Promise.all(
    hostIds.map(async (hostId) => {
      const r = await call(hostId, 'DELETE', `/api/devices?pairing_id=${encodeURIComponent(pairingId)}`)
      return r.kind === 'res' && r.status >= 200 && r.status < 300 ? null : hostId
    }),
  )
  return results.filter((h): h is string => h !== null)
}
