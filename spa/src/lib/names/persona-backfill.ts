// spa/src/lib/names/persona-backfill.ts — brings a host that connected (or came back) up to the source's personas
// (name pool v2 spec §4.3). A daemon that was offline during a save, or is new, holds older items; the App writes it the
// source's. Content decides, not the stamp: a stamp moves on every write, so a host that was edited elsewhere would
// look "newer" while missing roles the source has.
import { mirrorPersonas } from './persona-mirror'
import { savePending, subscribeSave } from './persona-draft-memory'
import { itemsHash } from './persona-hash'
import { usePersonaStore } from '../../stores/usePersonaStore'
import { resolvedSourceId } from '../../stores/usePersonaSourceStore'

// A (host, source content) pair is tried once per session. A daemon that normalizes what it stores (trims, reorders
// fields) would otherwise differ from the source forever and be rewritten on every load.
const tried = new Set<string>()
let waitingForSave = false

export function resetBackfillMemory(): void {
  tried.clear()
  waitingForSave = false
}

/**
 * A save is the one writer that is allowed to win, so the backfill steps aside while one is in flight and runs again
 * once it ends: whatever the backfill wrote from an older snapshot is then compared with the saved items and corrected.
 */
function runAfterSave(): void {
  // The save may already be over (the revision moved, nothing is pending): there is no end to wait for. Each run takes
  // a fresh snapshot, so this ends as soon as the source stops moving.
  if (!savePending()) { void backfillHosts().catch(() => { /* the next load tries again */ }); return }
  if (waitingForSave) return
  waitingForSave = true
  const off = subscribeSave(() => {
    if (savePending()) return
    off()
    waitingForSave = false
    void backfillHosts().catch(() => { /* the next load tries again */ })
  })
}

/** Every loaded host other than the source whose items differ from the source's gets the source's items. */
export async function backfillHosts(): Promise<void> {
  const sourceId = resolvedSourceId()
  if (sourceId === null) return
  if (savePending()) return runAfterSave()
  const { byHost } = usePersonaStore.getState()
  const src = byHost[sourceId]
  // An offline source (error, or nothing read yet) is not a copy to spread.
  if (!src?.collection || src.status === 'error') return
  const snapshotRevision = src.collection.revision
  // A save that began after the snapshot was taken has written newer items; the snapshot must not go over them. The
  // save reloads the source before it ends, so a moved revision (or a save still pending) means the snapshot is old.
  const moved = () => savePending() || usePersonaStore.getState().byHost[sourceId]?.collection?.revision !== snapshotRevision
  const sourceHash = await itemsHash(src.collection.items)
  for (const [hostId, entry] of Object.entries(byHost)) {
    if (hostId === sourceId || !entry.collection) continue
    if (moved()) return runAfterSave()
    if ((await itemsHash(entry.collection.items)) === sourceHash) continue
    const key = `${hostId}:${sourceHash}`
    if (tried.has(key)) continue
    const out = await mirrorPersonas(sourceId, src.collection.items, snapshotRevision, hostId, moved)
    // Only a copy that went through is remembered: a host that dropped, or a write that failed, gets another chance on
    // its next load. A daemon that took the write but stores something different is what the memory is for.
    if (out.report.mirrored.includes(hostId)) tried.add(key)
    // The check before the write cannot see a save that starts while the write is out; if one did, the host now holds
    // the older items and is corrected once that save ends.
    if (moved()) return runAfterSave()
  }
}
