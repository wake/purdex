/**
 * backup-ws-dispatch — routes a `backup:done` host-event (spec §4.6) into the
 * backup store. Mirrors the agent-ws dispatch seam: `useMultiHostEventWs` does
 * the per-host parse/dispatch and delegates the typed handling here so it is
 * unit-testable (R1-P2d).
 *
 * The daemon broadcasts `backup:done` once per COMMITTED snapshot (a
 * no-op-suppressed post broadcasts nothing). 2b consumes it ONLY to refresh
 * when a DIFFERENT device posted — a device's own post already refreshed its
 * status locally via `backupNow`, and the remote head must never pollute the
 * own lineage (R1-P1a).
 */
import type { HostEvent } from '../host-events'
import { useSyncStore } from '../sync/use-sync-store'
import { useBackupStore, type RemoteBackupDonePayload } from '../../stores/useBackupStore'

export function dispatchBackupWsEvent(hostId: string, event: HostEvent): void {
  let payload: RemoteBackupDonePayload
  try {
    payload = JSON.parse(event.value) as RemoteBackupDonePayload
  } catch {
    return // malformed — ignore, never throw on the WS path
  }
  // Own-device events are already reflected locally by backupNow; ignore them.
  if (payload.device === useSyncStore.getState().getClientId()) return
  useBackupStore.getState().applyRemoteBackupDone(hostId, payload)
}
