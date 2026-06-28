/**
 * useBackupStore — the Storage backup engine + per-host status (Phase 2b).
 *
 * `backupNow(hostId)` walks the In-App `/buffer` tree into a canonical manifest,
 * negotiates + uploads the blobs the host's daemon lacks, then posts the
 * snapshot, advancing this host's `parentId` lineage. State is keyed by hostId
 * (`byHost`) so backing up to host A never pollutes host B's lineage — the
 * device-A-backs-up / device-B-restores model (spec §4.6, Design 1/3).
 *
 * Lineage convergence (R2-P1a): ANY successful `postSnapshot` — a real write OR
 * a server no-op that returns the head id — advances `lastSnapshotId` to the
 * returned id and records `lastManifestJSON`, so a lagging local lineage whose
 * content already equals head does not re-post the same no-op forever.
 *
 * `applyRemoteBackupDone` is the cross-device refresh (a `backup:done` from a
 * DIFFERENT device): it updates ONLY `lastBackupAt`/status and MUST NEVER touch
 * `lastSnapshotId`/`lastManifestJSON` — those are own-lineage and only
 * `backupNow` may advance them (R1-P1a).
 */
import { create } from 'zustand'
import { buildManifest } from '../lib/storage-backup/manifest'
import { postMissing, putBlob, postSnapshot } from '../lib/storage-backup/backup-api'
import { getFsBackend } from '../lib/fs-backend'
import { useSyncStore } from '../lib/sync/use-sync-store'

/** Fixed logical store for the In-App tree (spec §4.1 — single store today). */
const STORE_ID = 'inapp:buffer'

export interface HostBackupState {
  status: 'idle' | 'backing-up' | 'error'
  lastBackupAt: number | null
  lastError: string | null
  /** This host's own last successful snapshotId — the next post's `parentId`. */
  lastSnapshotId: number | null
  /** JSON of the last successfully posted manifest — the client no-op key. */
  lastManifestJSON: string | null
}

/** The `backup:done` host-event payload (spec §4.6). */
export interface RemoteBackupDonePayload {
  storeId: string
  snapshotId: number
  currentHeadId: number
  device: string
  trigger: string
  createdAt: number
}

interface BackupStore {
  byHost: Record<string, HostBackupState>
  backupNow: (hostId: string) => Promise<void>
  applyRemoteBackupDone: (hostId: string, payload: RemoteBackupDonePayload) => void
}

const initialHostState = (): HostBackupState => ({
  status: 'idle',
  lastBackupAt: null,
  lastError: null,
  lastSnapshotId: null,
  lastManifestJSON: null,
})

const errMsg = (err: unknown): string => (err instanceof Error ? err.message : String(err))

export const useBackupStore = create<BackupStore>((set, get) => {
  const patch = (hostId: string, p: Partial<HostBackupState>) =>
    set((s) => {
      const prev = s.byHost[hostId] ?? initialHostState()
      return { byHost: { ...s.byHost, [hostId]: { ...prev, ...p } } }
    })

  return {
    byHost: {},

    backupNow: async (hostId) => {
      const backend = getFsBackend({ type: 'inapp' })
      if (!backend) {
        patch(hostId, { status: 'error', lastError: 'InApp backend unavailable' })
        return
      }
      // Snapshot this host's lineage at entry — the parentId is this host's OWN
      // prior snapshotId, captured before any await so it can't pick up a
      // concurrent advance (Design 5).
      const current = get().byHost[hostId] ?? initialHostState()

      let built
      try {
        built = await buildManifest(backend)
      } catch (err) {
        patch(hostId, { status: 'error', lastError: errMsg(err) })
        return
      }

      // Client-side no-op suppression (spec §4.6): a tree identical to the last
      // successfully posted manifest skips the whole round-trip.
      const manifestJSON = JSON.stringify(built.entries)
      if (manifestJSON === current.lastManifestJSON) return

      patch(hostId, { status: 'backing-up', lastError: null })
      try {
        const hashes = Array.from(built.blobs.keys())
        const missing = hashes.length > 0 ? await postMissing(hostId, hashes) : []
        for (const hash of missing) {
          const bytes = built.blobs.get(hash)
          if (!bytes) continue
          await putBlob(hostId, hash, bytes)
        }
        const res = await postSnapshot(hostId, {
          storeId: STORE_ID,
          device: useSyncStore.getState().getClientId(),
          parentId: current.lastSnapshotId,
          trigger: 'auto',
          manifest: built.entries,
        })
        // Converge lineage on ANY successful post (written OR server no-op).
        patch(hostId, {
          status: 'idle',
          lastBackupAt: Date.now(),
          lastError: null,
          lastSnapshotId: res.snapshotId,
          lastManifestJSON: manifestJSON,
        })
      } catch (err) {
        patch(hostId, { status: 'error', lastError: errMsg(err) })
      }
    },

    applyRemoteBackupDone: (hostId, payload) => {
      // Status/time ONLY — never the own lineage (R1-P1a). `createdAt` is epoch
      // seconds (spec §4.6); fall back to now if absent.
      patch(hostId, {
        status: 'idle',
        lastBackupAt: payload.createdAt ? payload.createdAt * 1000 : Date.now(),
      })
    },
  }
})
