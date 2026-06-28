/**
 * backup-api — thin client over the Phase 2a daemon backup endpoints
 * (`/api/backup/...`, spec §4.2), driven through the host-aware `hostFetch`
 * (base URL + bearer auth per host). Each call targets one host's daemon — the
 * engine (T2b-3) resolves which host before calling.
 *
 * Errors surface the HTTP status so the engine can distinguish a `409`
 * (blob-not-yet-uploaded), `400` (rejected manifest), `413` (overflow) etc. and
 * record a non-silent failure.
 */
import { hostFetch } from '../host-api'
import type { ManifestEntry } from './manifest'

export interface SnapshotRequest {
  storeId: string
  device: string
  /** The client's own last successful snapshotId for this store (NULL on first). */
  parentId: number | null
  trigger: string
  manifest: ManifestEntry[]
}

export interface SnapshotResponse {
  snapshotId: number
  isFork: boolean
  currentHeadId: number
}

/** Negotiate which of `hashes` the daemon lacks. Returns the missing subset. */
export async function postMissing(hostId: string, hashes: string[]): Promise<string[]> {
  const res = await hostFetch(hostId, '/api/backup/missing', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ hashes }),
  })
  if (!res.ok) throw new Error(`backup/missing failed: ${res.status}`)
  const data = (await res.json()) as { missing: string[] }
  return data.missing
}

/**
 * Upload one blob's raw bytes under its content hash. The daemon recomputes the
 * sha256 and rejects a mismatch; an existing hash is idempotent (also `204`).
 * Resolves on `204`, throws (with the status) otherwise.
 */
export async function putBlob(hostId: string, hash: string, bytes: Uint8Array): Promise<void> {
  const res = await hostFetch(hostId, `/api/backup/blob/${hash}`, {
    method: 'PUT',
    // A Uint8Array is a valid fetch body (BufferSource). Cast around the TS 5.7
    // `Uint8Array<ArrayBufferLike>` vs `BodyInit` mismatch; the exact bytes are
    // sent raw (the URL carries the content hash the daemon re-verifies).
    body: bytes as unknown as BodyInit,
  })
  if (res.status !== 204) throw new Error(`backup/blob PUT failed: ${res.status}`)
}

/**
 * Append a snapshot. Returns the created (or no-op-suppressed head) `snapshotId`
 * plus `isFork`/`currentHeadId`. Throws surfacing the status on any non-2xx
 * (e.g. `409` missing blob, `400` rejected manifest, `413` overflow).
 */
export async function postSnapshot(
  hostId: string,
  req: SnapshotRequest,
): Promise<SnapshotResponse> {
  const res = await hostFetch(hostId, '/api/backup/snapshot', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!res.ok) throw new Error(`backup/snapshot failed: ${res.status}`)
  return (await res.json()) as SnapshotResponse
}
