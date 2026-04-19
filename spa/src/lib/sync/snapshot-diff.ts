import { deepEqual } from './three-way-merge'
import type { SyncBundle } from './types'

export interface ContributorDiff {
  id: string
  status: 'identical' | 'changed' | 'missing-in-snapshot' | 'missing-in-current'
}

/**
 * Deep compare two bundles, ignoring envelope fields (version, timestamp,
 * device). Used for dedup on createSnapshot.
 */
export function equalExceptEnvelope(a: SyncBundle, b: SyncBundle): boolean {
  return deepEqual(a.collections, b.collections)
}

/**
 * Per-contributor diff: for each contributor id present in either bundle,
 * return identical | changed | missing-in-snapshot | missing-in-current.
 * Used by SnapshotDetail to show a diff summary.
 */
export function computeSnapshotDiff(
  snapshot: SyncBundle,
  current: SyncBundle,
): ContributorDiff[] {
  const ids = new Set<string>([
    ...Object.keys(snapshot.collections),
    ...Object.keys(current.collections),
  ])
  return Array.from(ids).map((id) => {
    const s = snapshot.collections[id]
    const c = current.collections[id]
    if (s === undefined) return { id, status: 'missing-in-snapshot' as const }
    if (c === undefined) return { id, status: 'missing-in-current' as const }
    return { id, status: deepEqual(s, c) ? ('identical' as const) : ('changed' as const) }
  })
}
