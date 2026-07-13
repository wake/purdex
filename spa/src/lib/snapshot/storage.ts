import { browserStorage } from '../storage/browser-backend'
import type { WorkspaceSnapshot } from './types'

export const SNAPSHOT_KEY = 'purdex-workspace-snapshot'
export const SNAPSHOT_PREV_KEY = 'purdex-workspace-snapshot-prev'

function readSnapshotFromKey(key: string): WorkspaceSnapshot | null {
  const raw = browserStorage.getItem(key) as string | null
  if (raw == null) return null
  try {
    const parsed = JSON.parse(raw) as WorkspaceSnapshot
    if (parsed?.version !== 1) return null
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
