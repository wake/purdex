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
