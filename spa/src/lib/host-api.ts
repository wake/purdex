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
  const { getWsBase, hosts } = useHostStore.getState()
  const base = getWsBase(hostId)
  const url = new URL(`${base}${path}`)
  const host = hosts[hostId]
  if (host?.token) url.searchParams.set('token', host.token)
  return url.toString()
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
