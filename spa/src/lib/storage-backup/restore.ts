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
  | {
      status: 'done'
      restorePointId: number
      changed: RestoreChange
      /**
       * Root-relative paths that are FILES in the restored tree. Pane
       * reconciliation needs this to tell a content change (still a file →
       * reload/remount) from a kind change (a path that became a directory →
       * the open file pane must close, not reload a directory). (codex 2c-2 R1)
       */
      restoredFiles: string[]
    }

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

  // (2) Require the atomic-replace capability and capture the tree REVISION NOW —
  // before pre-restore. This is the anchor for the apply-time race guard: any
  // local `/buffer` write between here and the apply bumps the revision, and
  // replaceTree (below) re-checks it INSIDE its own transaction, so a concurrent
  // write can never be silently overwritten (codex 2c-1 R3, Critical). A second
  // manifest scan would be non-atomic and lossy — the revision is the only sound
  // guard.
  const backend = deps.backend
  if (!supportsReplaceTree(backend)) {
    throw new Error('restore: backend does not support replaceTree')
  }
  const beforeRevision = await backend.getRevision()

  // (3) Baseline manifest for the change diff (2c-2 pane reconciliation). The
  // revision above — not this snapshot — is the data-safety anchor.
  const before = await buildManifest(backend)

  // (4) Pre-restore safety snapshot (always posts; id = restore-point). With
  // forcePost, a success ALWAYS yields a numeric id (a real row or the
  // content-keyed head); `null` means the snapshot failed (daemon down, build /
  // post error). Restore is destructive, so abort BEFORE touching the tree — a
  // wipe with no restore-point would leave nothing to roll back to (R2-Pa,
  // codex 2c-1 R1 P1). Nothing has mutated yet, so throwing is safe.
  const restorePointId = await deps.preRestore()
  if (restorePointId === null) {
    throw new Error('restore: pre-restore safety snapshot failed; aborting (no restore-point)')
  }

  // (5) Fetch + verify EVERY file blob into memory before any mutation. The blob
  // FETCH is deduped by hash (one download per distinct content), but the
  // per-entry SIZE assertion runs for every file entry — a corrupt manifest with
  // the same hash but a conflicting declared size must not pass (codex 2c-1 R2).
  const detail = await deps.getSnapshot(deps.hostId, deps.snapshotId)
  const blobs = new Map<string, Uint8Array>()
  for (const entry of detail.manifest) {
    if (entry.kind !== 'file') continue
    let bytes = blobs.get(entry.hash)
    if (!bytes) {
      bytes = await deps.getBlob(deps.hostId, entry.hash)
      const hash = await sha256Hex(bytes)
      if (hash !== entry.hash) {
        throw new Error(`restore: blob hash mismatch for ${entry.path} (expected ${entry.hash})`)
      }
      blobs.set(entry.hash, bytes)
    }
    if (bytes.byteLength !== entry.size) {
      throw new Error(`restore: blob size mismatch for ${entry.path}`)
    }
  }

  // (6) Pre-apply guard for un-flushed editor state. Dirty/locked buffers live in
  // Zustand (NOT IndexedDB), so the revision check cannot see them — re-run the
  // conflict guard here. (The IDB race — a concurrent committed `/buffer` write —
  // is handled atomically by replaceTree's revision check below, not by a second
  // non-atomic manifest scan.)
  if (deps.findConflicts().length > 0) {
    throw new Error('restore: a buffer became dirty/locked during restore; aborted (no overwrite)')
  }

  // (7) Apply atomically. replaceTree re-checks `beforeRevision` inside its txn
  // and throws TreeRevisionMismatchError (no mutation) if a local write landed
  // since step 2 — the only fully-atomic overwrite guard.
  const replaceEntries: ReplaceEntry[] = detail.manifest.map((e) =>
    e.kind === 'dir'
      ? { relPath: e.path, isDir: true }
      : { relPath: e.path, isDir: false, bytes: blobs.get(e.hash) },
  )
  await backend.replaceTree(STORAGE_ROOT, replaceEntries, beforeRevision)

  // (8) Report the diff + restored file set for 2c-2 pane reconciliation.
  const changed = diffManifests(before.entries, detail.manifest)
  const restoredFiles = detail.manifest.filter((e) => e.kind === 'file').map((e) => e.path)
  return { status: 'done', restorePointId, changed, restoredFiles }
}
