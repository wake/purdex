import { buildSnapshot } from '../snapshot/capture'
import { writePrevSnapshot } from '../snapshot/storage'
import type { SessionMeta, WorkspaceSnapshot } from '../snapshot/types'

/**
 * Build (in memory only) the STRUCTURE-ONLY `-prev` backup of the current world
 * taken before a device-state restore (spec §4.4). Every captured session is
 * marked `restorable: false`, so a later `undoLastRestore` (which replays
 * `-prev` through `restoreAll`) only reattaches still-live sessions and marks
 * the rest terminated — it can never call `createSession`. The snapshot
 * returned by `build` is not mutated, and nothing is written to storage.
 */
export async function buildDeviceStatePrev(
  now: number,
  build: typeof buildSnapshot = buildSnapshot,
): Promise<WorkspaceSnapshot> {
  const snap = await build(now)
  const sessionMeta: WorkspaceSnapshot['sessionMeta'] = {}
  for (const [hostId, perHost] of Object.entries(snap.sessionMeta)) {
    const next: Record<string, SessionMeta> = {}
    for (const [code, meta] of Object.entries(perHost)) {
      next[code] = { ...meta, restorable: false }
    }
    sessionMeta[hostId] = next
  }
  return { ...snap, sessionMeta }
}

/** Build the structure-only backup and write it to the `-prev` key. */
export async function writeDeviceStatePrev(
  now: number,
  build: typeof buildSnapshot = buildSnapshot,
): Promise<void> {
  writePrevSnapshot(await buildDeviceStatePrev(now, build))
}
