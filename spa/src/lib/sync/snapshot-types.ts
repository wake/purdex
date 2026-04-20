import type { SyncBundle } from './types'

export type SnapshotTrigger =
  | 'auto'         // reserved for future auto-sync feature; P1 does not emit this
  | 'manual'
  | 'pre-import'
  | 'pre-restore'

export interface SnapshotMetadata {
  id: string
  timestamp: number           // ms epoch
  device: string              // SyncBundle.device (top-level)
  trigger: SnapshotTrigger
  bundleSize: number          // bytes (new TextEncoder().encode(json).byteLength)
  contributorIds: string[]    // keys of bundle.collections
  isSessionPristine: boolean  // session-start pre-op snapshot, never evicted
}

export interface StoredSnapshot extends SnapshotMetadata {
  bundle: SyncBundle
}

export interface CompactionResult {
  kept: string[]
  evicted: string[]
}
