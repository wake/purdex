/**
 * restore — the client-orchestrated restore flow (spec §4.4, 2c). Headless and
 * dependency-injected (no store/React import): 2c-1 unit-tests it with spies,
 * 2c-2 supplies live deps (live guard, the backup engine's pre-restore, the read
 * API client, the In-App backend).
 *
 * Order is contractual and load-bearing:
 *   1. Guard FIRST — before any lock/network/IDB. Blocked ⇒ return immediately.
 *   2. Pre-restore safety snapshot (forcePost, via the backup engine's per-host
 *      single-flight) — always reaches the daemon so a restore-point exists; its
 *      returned id (new row OR content-keyed head) is the restore-point.
 *   3. Fetch + verify EVERY file blob (sha256 + size) into memory.
 *   4. Only then apply the restore as ONE atomic replaceTree txn.
 * Any failure in step 3 throws BEFORE any IDB mutation, so the tree is never
 * left half-applied (R2-Pa). Pane reconciliation is 2c-2, not here.
 */
import { buildManifest, type ManifestEntry } from './manifest'
import { sha256Hex } from '../crypto-hash'
import { STORAGE_ROOT } from '../storage-paths'
import { supportsReplaceTree, type FsBackend, type ReplaceEntry } from '../fs-backend'
import type { SnapshotDetail } from './backup-api'
import type { RestoreConflict } from './restore-guard'

/** Path-level diff of the pre-restore tree vs the restored snapshot. */
export interface RestoreChange {
  added: string[]
  removed: string[]
  modified: string[]
}

export type RestoreResult =
  | { status: 'blocked'; conflicts: RestoreConflict[] }
  | { status: 'done'; restorePointId: number; changed: RestoreChange }

export interface RestoreSnapshotDeps {
  hostId: string
  /** The snapshot to restore to. */
  snapshotId: number
  /** The In-App backend (must support replaceTree). */
  backend: FsBackend
  /** Pre-flight conflict check (see restore-guard). */
  findConflicts: () => RestoreConflict[]
  /**
   * Take the pre-restore safety snapshot and return its id (the restore-point).
   * Live wiring calls `backupNow(hostId, { trigger:'pre-restore', forcePost:true })`,
   * so this also serialises against any in-flight auto-backup.
   */
  preRestore: () => Promise<number | null>
  getSnapshot: (hostId: string, id: number) => Promise<SnapshotDetail>
  getBlob: (hostId: string, hash: string) => Promise<Uint8Array>
}

/** `kind:hash` signature so a same-path file↔dir flip or content change counts. */
function entrySig(e: ManifestEntry): string {
  return `${e.kind}:${e.hash}`
}

function diffManifests(before: ManifestEntry[], after: ManifestEntry[]): RestoreChange {
  const beforeByPath = new Map(before.map((e) => [e.path, e]))
  const afterByPath = new Map(after.map((e) => [e.path, e]))
  const added: string[] = []
  const removed: string[] = []
  const modified: string[] = []
  for (const [path, entry] of afterByPath) {
    const prev = beforeByPath.get(path)
    if (!prev) added.push(path)
    else if (entrySig(prev) !== entrySig(entry)) modified.push(path)
  }
  for (const path of beforeByPath.keys()) {
    if (!afterByPath.has(path)) removed.push(path)
  }
  return { added, removed, modified }
}

export async function restoreSnapshot(deps: RestoreSnapshotDeps): Promise<RestoreResult> {
  // (1) Guard first — nothing acquired, nothing written if blocked.
  const conflicts = deps.findConflicts()
  if (conflicts.length > 0) return { status: 'blocked', conflicts }

  // (2) Pre-restore safety snapshot (always posts; id = restore-point). With
  // forcePost, a success ALWAYS yields a numeric id (a real row or the
  // content-keyed head); `null` means the snapshot failed (daemon down, build /
  // post error). Restore is destructive, so abort BEFORE touching the tree — a
  // wipe with no restore-point would leave nothing to roll back to (R2-Pa,
  // codex 2c-1 R1 P1). Nothing has mutated yet, so throwing is safe.
  const restorePointId = await deps.preRestore()
  if (restorePointId === null) {
    throw new Error('restore: pre-restore safety snapshot failed; aborting (no restore-point)')
  }

  // Capture the current (pre-restore) tree for the change diff. Read-only — the
  // tree is still untouched at this point.
  const before = await buildManifest(deps.backend)

  // (3) Fetch + verify EVERY file blob into memory before any mutation.
  const detail = await deps.getSnapshot(deps.hostId, deps.snapshotId)
  const blobs = new Map<string, Uint8Array>()
  for (const entry of detail.manifest) {
    if (entry.kind !== 'file') continue
    if (blobs.has(entry.hash)) continue
    const bytes = await deps.getBlob(deps.hostId, entry.hash)
    const hash = await sha256Hex(bytes)
    if (hash !== entry.hash) {
      throw new Error(`restore: blob hash mismatch for ${entry.path} (expected ${entry.hash})`)
    }
    if (bytes.byteLength !== entry.size) {
      throw new Error(`restore: blob size mismatch for ${entry.path}`)
    }
    blobs.set(entry.hash, bytes)
  }

  // (4) Apply atomically. The guard above means this is the FIRST IDB mutation.
  if (!supportsReplaceTree(deps.backend)) {
    throw new Error('restore: backend does not support replaceTree')
  }
  const replaceEntries: ReplaceEntry[] = detail.manifest.map((e) =>
    e.kind === 'dir'
      ? { relPath: e.path, isDir: true }
      : { relPath: e.path, isDir: false, bytes: blobs.get(e.hash) },
  )
  await deps.backend.replaceTree(STORAGE_ROOT, replaceEntries)

  // (5) Report the diff for 2c-2 pane reconciliation.
  const changed = diffManifests(before.entries, detail.manifest)
  return { status: 'done', restorePointId, changed }
}
