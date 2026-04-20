import { useMemo } from 'react'
import { useSyncStore } from '../../../../../lib/sync/use-sync-store'
import { __getActiveEngine } from '../../../../../lib/sync/register-sync'
import { computeSnapshotDiff, type ContributorDiff } from '../../../../../lib/sync/snapshot-diff'
import type { StoredSnapshot } from '../../../../../lib/sync/snapshot-types'

export function useSnapshotDiff(snapshot: StoredSnapshot | null): ContributorDiff[] | null {
  const clientId = useSyncStore((s) => s.clientId) ?? 'unknown'

  return useMemo(() => {
    if (!snapshot) return null
    const engine = __getActiveEngine()
    // Diff against the full contributor registry, not just enabledModules —
    // a snapshot may contain data for currently-disabled contributors, and
    // serializing with only enabled ids would misreport those as
    // "missing-in-current" when they are in fact still registered.
    // Mirror createPreOperationSnapshot which also uses the full registry.
    const allIds = engine.getContributors().map((c) => c.id)
    const current = engine.serialize(clientId, allIds)
    return computeSnapshotDiff(snapshot.bundle, current)
  }, [snapshot, clientId])
}
