// spa/src/lib/names/persona-backfill.ts — brings a host that connected (or came back) up to the source's personas
// (name pool v2 spec §4.3). A daemon that was offline during a save, or is new, holds older items; the App writes it the
// source's. Content decides, not the stamp: a stamp moves on every write, so a host that was edited elsewhere would
// look "newer" while missing roles the source has.
import { mirrorPersonas } from './persona-mirror'
import { savePending } from './persona-draft-memory'
import { itemsHash } from './persona-hash'
import { usePersonaStore } from '../../stores/usePersonaStore'
import { resolvedSourceId } from '../../stores/usePersonaSourceStore'

// A (host, source content) pair is tried once per session. A daemon that normalizes what it stores (trims, reorders
// fields) would otherwise differ from the source forever and be rewritten on every load.
const tried = new Set<string>()

export function resetBackfillMemory(): void {
  tried.clear()
}

/** Every loaded host other than the source whose items differ from the source's gets the source's items. */
export async function backfillHosts(): Promise<void> {
  const sourceId = resolvedSourceId()
  if (sourceId === null || savePending()) return
  const { byHost } = usePersonaStore.getState()
  const src = byHost[sourceId]
  // An offline source (error, or nothing read yet) is not a copy to spread.
  if (!src?.collection || src.status === 'error') return
  const sourceHash = await itemsHash(src.collection.items)
  for (const [hostId, entry] of Object.entries(byHost)) {
    if (hostId === sourceId || !entry.collection) continue
    if (savePending()) return
    if ((await itemsHash(entry.collection.items)) === sourceHash) continue
    const key = `${hostId}:${sourceHash}`
    if (tried.has(key)) continue
    // Only a copy that went through is remembered: a host that dropped, or a write that failed, gets another chance on
    // its next load. A daemon that took the write but stores something different is what the memory is for.
    // A save that began after the snapshot was taken has written newer items; the snapshot must not go over them. The
    // save reloads the source before it ends, so a moved revision (or a save still pending) means the snapshot is old.
    const snapshotRevision = src.collection.revision
    const moved = () => savePending() || usePersonaStore.getState().byHost[sourceId]?.collection?.revision !== snapshotRevision
    if (moved()) return
    const out = await mirrorPersonas(sourceId, src.collection.items, snapshotRevision, hostId, moved)
    if (out.report.mirrored.includes(hostId)) tried.add(key)
  }
}
