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

// Re-export so the read API's consumers (history list, restore engine) have a
// single import surface for the daemon JSON shapes.
export type { ManifestEntry }

/**
 * One history row, summarised from a snapshot's manifest (no blob bytes).
 * Mirrors the daemon `SnapshotSummary` (`internal/module/backup/store.go`),
 * newest-first when returned by `getHistory`.
 */
export interface SnapshotSummary {
  id: number
  device: string
  /** The snapshot this one descends from, or `null` for a lineage root. */
  parentId: number | null
  isFork: boolean
  trigger: string
  /** Unix epoch seconds. */
  createdAt: number
  fileCount: number
  dirCount: number
  totalSize: number
}

/**
 * A single snapshot with its full manifest. Mirrors the daemon
 * `SnapshotDetail`; the restore engine reads `manifest` to fetch + verify blobs.
 */
export interface SnapshotDetail {
  id: number
  storeId: string
  device: string
  parentId: number | null
  isFork: boolean
  trigger: string
  createdAt: number
  manifest: ManifestEntry[]
}

/**
 * Typed error for a `404` from a snapshot/blob read so callers (the restore
 * orchestrator) can distinguish "the requested snapshot no longer exists"
 * (e.g. GC'd) from a transport/server failure. Carries the HTTP status.
 */
export class BackupNotFoundError extends Error {
  status: number
  constructor(message: string, status = 404) {
    super(message)
    this.name = 'BackupNotFoundError'
    this.status = status
  }
}

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

/**
 * List a store's snapshots newest-first (`GET /history?storeId=`). Throws
 * surfacing the status on any non-2xx so the UI can show a non-silent error.
 */
export async function getHistory(
  hostId: string,
  storeId: string,
): Promise<SnapshotSummary[]> {
  const res = await hostFetch(
    hostId,
    `/api/backup/history?storeId=${encodeURIComponent(storeId)}`,
  )
  if (!res.ok) throw new Error(`backup/history failed: ${res.status}`)
  return (await res.json()) as SnapshotSummary[]
}

/**
 * Fetch one snapshot with its full manifest (`GET /snapshot/{id}`). A `404`
 * (snapshot absent / GC'd) throws a typed `BackupNotFoundError`; any other
 * non-2xx throws surfacing the status.
 */
export async function getSnapshot(
  hostId: string,
  id: number,
): Promise<SnapshotDetail> {
  const res = await hostFetch(hostId, `/api/backup/snapshot/${id}`)
  if (res.status === 404) {
    throw new BackupNotFoundError(`backup/snapshot not found: 404`)
  }
  if (!res.ok) throw new Error(`backup/snapshot GET failed: ${res.status}`)
  return (await res.json()) as SnapshotDetail
}

/**
 * Fetch one blob's raw bytes by content hash (`GET /blob/{hash}`). Throws
 * surfacing the status on any non-2xx (`404` absent, `500` server) — the
 * restore engine aborts (before any local mutation) on the first failure.
 */
export async function getBlob(hostId: string, hash: string): Promise<Uint8Array> {
  const res = await hostFetch(hostId, `/api/backup/blob/${hash}`)
  if (!res.ok) throw new Error(`backup/blob GET failed: ${res.status}`)
  const buf = await res.arrayBuffer()
  return new Uint8Array(buf)
}
