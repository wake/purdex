// spa/src/lib/host-api.ts — Host-aware API layer
import { useHostStore } from '../stores/useHostStore'

/* ─── Core helpers ─── */

export function hostFetch(hostId: string, path: string, init?: RequestInit): Promise<Response> {
  const { getDaemonBase, getAuthHeaders } = useHostStore.getState()
  const base = getDaemonBase(hostId)
  const headers = new Headers(init?.headers)
  const auth = getAuthHeaders(hostId)
  for (const [k, v] of Object.entries(auth)) {
    headers.set(k, v)
  }
  return fetch(`${base}${path}`, { ...init, headers })
}

export function hostWsUrl(hostId: string, path: string): string {
  const base = useHostStore.getState().getWsBase(hostId)
  return `${base}${path}`
}

export async function fetchWsTicket(hostId: string): Promise<string> {
  const res = await hostFetch(hostId, '/api/ws-ticket', { method: 'POST' })
  if (!res.ok) throw new Error(`ws-ticket failed: ${res.status}`)
  const data = await res.json()
  return data.ticket
}

/* ─── API functions ─── */

export function fetchHealth(hostId: string) {
  return hostFetch(hostId, '/api/health')
}

export function fetchInfo(hostId: string) {
  return hostFetch(hostId, '/api/info')
}

export function fetchHooksStatus(hostId: string) {
  return hostFetch(hostId, '/api/hooks/status')
}

export function installHooks(hostId: string) {
  return hostFetch(hostId, '/api/hooks/install', { method: 'POST' })
}

export function removeHooks(hostId: string) {
  return hostFetch(hostId, '/api/hooks/remove', { method: 'POST' })
}

export function fetchUploadStats(hostId: string) {
  return hostFetch(hostId, '/api/upload/stats')
}

export function fetchUploadFiles(hostId: string) {
  return hostFetch(hostId, '/api/upload/files')
}

export function deleteUploadFile(hostId: string, session: string, filename: string) {
  return hostFetch(hostId, `/api/upload/files/${session}/${filename}`, { method: 'DELETE' })
}

export function deleteUploadSession(hostId: string, session: string) {
  return hostFetch(hostId, `/api/upload/files/${session}`, { method: 'DELETE' })
}

export function deleteAllUploads(hostId: string) {
  return hostFetch(hostId, '/api/upload/files', { method: 'DELETE' })
}

export function renameSession(hostId: string, code: string, name: string) {
  return hostFetch(hostId, `/api/sessions/${code}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  })
}

/* ─── Pairing API (Phase 5a) ─── */

/** POST /api/pair/verify — Quick mode: verify pairing secret, get setupSecret. */
export async function fetchPairVerify(
  base: string,
  secret: string,
): Promise<{ setupSecret: string }> {
  const res = await fetch(`${base}/api/pair/verify`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ secret }),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new PairingError(res.status, text)
  }
  return res.json()
}

/** POST /api/pair/setup — Quick mode: set token on daemon. */
export async function fetchPairSetup(
  base: string,
  setupSecret: string,
  token: string,
): Promise<{ ok: boolean }> {
  const res = await fetch(`${base}/api/pair/setup`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ setupSecret, token }),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new PairingError(res.status, text)
  }
  return res.json()
}

/** POST /api/token/auth — General mode: confirm runtime token. */
export async function fetchTokenAuth(
  base: string,
  token: string,
): Promise<{ ok: boolean }> {
  const res = await fetch(`${base}/api/token/auth`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${token}` },
  })
  if (res.status === 409) {
    // already_confirmed — treat as success per spec
    return { ok: true }
  }
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new PairingError(res.status, text)
  }
  return res.json()
}

export class PairingError extends Error {
  constructor(
    public status: number,
    public body: string,
  ) {
    super(`Pairing failed: HTTP ${status}`)
  }
}
