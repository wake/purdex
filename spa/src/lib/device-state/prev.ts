import { buildSnapshot } from '../snapshot/capture'
import { writePrevSnapshot } from '../snapshot/storage'
import type { SessionMeta, WorkspaceSnapshot } from '../snapshot/types'

/**
 * Back up the current world to the `-prev` key as a STRUCTURE-ONLY snapshot
 * before a device-state replace (spec §4.4). Every captured session is marked
 * `restorable: false`, so a later `undoLastRestore` (which replays `-prev`
 * through `restoreAll`) only reattaches still-live sessions and marks the rest
 * terminated — it can never call `createSession`. The snapshot returned by
 * `build` is not mutated.
 */
export async function writeDeviceStatePrev(
  now: number,
  build: typeof buildSnapshot = buildSnapshot,
): Promise<void> {
  const snap = await build(now)
  const sessionMeta: WorkspaceSnapshot['sessionMeta'] = {}
  for (const [hostId, perHost] of Object.entries(snap.sessionMeta)) {
    const next: Record<string, SessionMeta> = {}
    for (const [code, meta] of Object.entries(perHost)) {
      next[code] = { ...meta, restorable: false }
    }
    sessionMeta[hostId] = next
  }
  writePrevSnapshot({ ...snap, sessionMeta })
}
