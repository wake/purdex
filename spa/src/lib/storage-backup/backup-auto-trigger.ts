/**
 * backup-auto-trigger — the persistent, debounced auto-backup driver (Phase 2b).
 *
 * Lives in a persistent layer (app bootstrap), NOT in `StoragePane` (R1-C1), so
 * editing a `/buffer` file with the Storage pane closed still backs up. It
 * subscribes to the In-App backend's `onMutation`, debounces ~2 s, and calls
 * `backupNow` for the host resolved AT MUTATION TIME (`activeHostId ??
 * hostOrder[0]`).
 *
 * Host capture is **cancel-only, never retarget** (R1-P1d / R2-P2a): a pending
 * backup is cancelled if the resolved host changes before the timer fires — a
 * stale mutation must not be backed up to the wrong daemon. A fresh mutation
 * then schedules anew for the new host.
 */
import { getFsBackend, supportsMutationEvents, type SupportsMutationEvents } from '../fs-backend'
import { useHostStore } from '../../stores/useHostStore'
import { useBackupStore } from '../../stores/useBackupStore'

export interface BackupAutoTriggerOptions {
  backend: SupportsMutationEvents
  /** Resolve the backup target host (`activeHostId ?? hostOrder[0]`). */
  resolveHostId: () => string | undefined
  /** Subscribe to host-store changes; returns an unsubscribe fn. */
  subscribeHost: (cb: () => void) => () => void
  backupNow: (hostId: string) => void | Promise<void>
  /** Debounce window in ms (default 2000). */
  delayMs?: number
}

export interface BackupAutoTrigger {
  dispose: () => void
}

export function createBackupAutoTrigger(opts: BackupAutoTriggerOptions): BackupAutoTrigger {
  const { backend, resolveHostId, subscribeHost, backupNow, delayMs = 2000 } = opts

  let timer: ReturnType<typeof setTimeout> | null = null
  let pendingHost: string | null = null

  const clearPending = () => {
    if (timer !== null) {
      clearTimeout(timer)
      timer = null
    }
    pendingHost = null
  }

  const unsubMutation = backend.onMutation(() => {
    const host = resolveHostId()
    if (!host) return // no target host — nothing to back up to
    // Reset the debounce, capturing this (latest) mutation's resolved host.
    clearPending()
    pendingHost = host
    timer = setTimeout(() => {
      const target = pendingHost
      timer = null
      pendingHost = null
      if (target) void backupNow(target)
    }, delayMs)
  })

  // Cancel (never retarget) a pending backup if the resolved host changes before
  // it fires — covers both an active-host switch and a hostOrder[0] fallback
  // change (R2-P2a). An unrelated host-store change that leaves the resolved
  // host unchanged keeps the pending backup intact.
  const unsubHost = subscribeHost(() => {
    if (timer !== null && resolveHostId() !== pendingHost) {
      clearPending()
    }
  })

  return {
    dispose: () => {
      unsubMutation()
      unsubHost()
      clearPending()
    },
  }
}

/**
 * Wire the auto-trigger to the live In-App backend + host/backup stores. Called
 * once at app bootstrap (`main.tsx`) — the persistent layer (R1-C1). Returns
 * `null` if the In-App backend is missing or lacks the mutation-events
 * capability (e.g. a backend that does not support backups).
 */
export function startBackupAutoTrigger(): BackupAutoTrigger | null {
  const backend = getFsBackend({ type: 'inapp' })
  if (!supportsMutationEvents(backend)) return null
  return createBackupAutoTrigger({
    backend: backend as SupportsMutationEvents,
    resolveHostId: () => {
      const s = useHostStore.getState()
      return s.activeHostId ?? s.hostOrder[0]
    },
    subscribeHost: (cb) => useHostStore.subscribe(cb),
    backupNow: (hostId) => useBackupStore.getState().backupNow(hostId),
  })
}
