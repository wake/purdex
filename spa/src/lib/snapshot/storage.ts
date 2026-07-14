import { browserStorage } from '../storage/browser-backend'
import type { WorkspaceSnapshot } from './types'

export const SNAPSHOT_KEY = 'purdex-workspace-snapshot'
export const SNAPSHOT_PREV_KEY = 'purdex-workspace-snapshot-prev'

/** True for a non-null, non-array plain object. */
function isPlainObject(x: unknown): x is Record<string, unknown> {
  return typeof x === 'object' && x !== null && !Array.isArray(x)
}

/**
 * Lightweight runtime shape guard at the read boundary. A corrupt-but-`version:1`
 * blob would otherwise flow into restore and crash MID-FLIGHT (after daemon side
 * effects) instead of being rejected up front. Only basic top-level type/shape is
 * checked here — deep per-tab/per-session validation is out of scope
 * (validateSnapshotConsistency covers navigation refs during restore).
 */
function isWellFormedSnapshot(x: unknown): x is WorkspaceSnapshot {
  if (!isPlainObject(x)) return false
  if (!isPlainObject(x.tabs)) return false
  if (!Array.isArray(x.tabOrder)) return false
  if (x.activeTabId !== null && typeof x.activeTabId !== 'string') return false
  if (!Array.isArray(x.workspaces)) return false
  if (x.activeWorkspaceId !== null && typeof x.activeWorkspaceId !== 'string') return false
  if (!isPlainObject(x.sessionMeta)) return false
  return true
}

function readSnapshotFromKey(key: string): WorkspaceSnapshot | null {
  const raw = browserStorage.getItem(key) as string | null
  if (raw == null) return null
  try {
    const parsed = JSON.parse(raw) as unknown
    if (!isPlainObject(parsed) || parsed.version !== 1) return null
    if (!isWellFormedSnapshot(parsed)) return null
    return parsed
  } catch {
    return null
  }
}

function writeSnapshotToKey(key: string, snap: WorkspaceSnapshot): void {
  browserStorage.setItem(key, JSON.stringify(snap))
}

export function readSnapshot(): WorkspaceSnapshot | null {
  return readSnapshotFromKey(SNAPSHOT_KEY)
}

export function writeSnapshot(snap: WorkspaceSnapshot): void {
  writeSnapshotToKey(SNAPSHOT_KEY, snap)
}

export function readPrevSnapshot(): WorkspaceSnapshot | null {
  return readSnapshotFromKey(SNAPSHOT_PREV_KEY)
}

export function writePrevSnapshot(snap: WorkspaceSnapshot): void {
  writeSnapshotToKey(SNAPSHOT_PREV_KEY, snap)
}

/**
 * Return a NEW snapshot with the rebuild-target cwd of one captured session
 * (composite key `[hostId][sessionCode]`) replaced by the user's trimmed input.
 * The input snapshot is never mutated — only the changed host map + entry are
 * spread into fresh objects.
 *
 * A non-empty cwd is authoritative: it sets `restorable: true` and clears any
 * `captureError` (e.g. `cwd-probe-failed`), preserving the `restorable ⟺
 * has-usable-cwd` invariant that capture.ts and `computeHealth`'s 🔴 predicate
 * rely on. An empty (or whitespace-only) cwd makes the session structure-only
 * (`restorable: false`, `cwd: undefined`). A missing target entry is a no-op —
 * the original snapshot is returned unchanged.
 */
export function setSessionMetaCwd(
  snap: WorkspaceSnapshot,
  hostId: string,
  sessionCode: string,
  rawCwd: string,
): WorkspaceSnapshot {
  const meta = snap.sessionMeta[hostId]?.[sessionCode]
  if (!meta) return snap

  const cwd = rawCwd.trim()
  const nextEntry = cwd
    ? { ...meta, cwd, restorable: true, captureError: undefined }
    : { ...meta, cwd: undefined, restorable: false, captureError: undefined }

  return {
    ...snap,
    sessionMeta: {
      ...snap.sessionMeta,
      [hostId]: { ...snap.sessionMeta[hostId], [sessionCode]: nextEntry },
    },
  }
}
