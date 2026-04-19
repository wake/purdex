import { useMemo } from 'react'
import { useSyncStore } from '../../../../../lib/sync/use-sync-store'
import { __getActiveEngine } from '../../../../../lib/sync/register-sync'
import { computeSnapshotDiff, type ContributorDiff } from '../../../../../lib/sync/snapshot-diff'
import type { StoredSnapshot } from '../../../../../lib/sync/snapshot-types'

export function useSnapshotDiff(snapshot: StoredSnapshot | null): ContributorDiff[] | null {
  const clientId = useSyncStore((s) => s.clientId) ?? 'unknown'
  const enabledModules = useSyncStore((s) => s.enabledModules)

  return useMemo(() => {
    if (!snapshot) return null
    const engine = __getActiveEngine()
    const enabled = enabledModules.length > 0
      ? enabledModules
      : engine.getContributors().map((c) => c.id)
    const current = engine.serialize(clientId, enabled)
    return computeSnapshotDiff(snapshot.bundle, current)
  }, [snapshot, clientId, enabledModules])
}
