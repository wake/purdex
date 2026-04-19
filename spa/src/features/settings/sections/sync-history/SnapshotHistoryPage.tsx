import { useEffect, useState } from 'react'
import { HistoryTabs } from './HistoryTabs'
import { HistoryList } from './HistoryList'
import { SnapshotDetail } from './SnapshotDetail'
import { SnapshotRestoreDialog } from './SnapshotRestoreDialog'
import { useLocalHistory } from './hooks/useLocalHistory'
import { useSnapshotDiff } from './hooks/useSnapshotDiff'
import { useSyncStore } from '../../../../lib/sync/use-sync-store'
import { getSnapshotStore } from '../../../../lib/sync/snapshot-store-instance'
import type { StoredSnapshot } from '../../../../lib/sync/snapshot-types'

export function SnapshotHistoryPage() {
  const { items, loading, error, refresh } = useLocalHistory()
  const [activeTab, setActiveTab] = useState<'local' | 'remote'>('local')
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [selectedSnap, setSelectedSnap] = useState<StoredSnapshot | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [restoring, setRestoring] = useState(false)

  const activeProviderId = useSyncStore((s) => s.activeProviderId)
  const pendingConflicts = useSyncStore((s) => s.pendingConflicts)
  const restoreFromSnapshot = useSyncStore((s) => s.restoreFromSnapshot)

  const diff = useSnapshotDiff(selectedSnap)

  useEffect(() => {
    if (!selectedId) {
      setSelectedSnap(null)
      return
    }
    let cancelled = false
    void getSnapshotStore().getLocal(selectedId).then((snap) => {
      if (!cancelled) setSelectedSnap(snap)
    })
    return () => { cancelled = true }
  }, [selectedId])

  async function handleRestore() {
    if (!selectedSnap) return
    setRestoring(true)
    try {
      await restoreFromSnapshot(selectedSnap, 'local')
      setDialogOpen(false)
      await refresh()
    } finally {
      setRestoring(false)
    }
  }

  return (
    <div className="flex h-full flex-col">
      <HistoryTabs
        active={activeTab}
        remoteAvailable={activeProviderId === 'daemon'}
        onChange={setActiveTab}
      />
      <div className="flex flex-1 overflow-hidden">
        <div className="w-80 overflow-y-auto border-r border-text-subtle/20">
          {activeTab === 'local' ? (
            <HistoryList
              items={items}
              loading={loading}
              error={error}
              selectedId={selectedId}
              onSelect={setSelectedId}
              onRetry={refresh}
            />
          ) : (
            <div className="p-6 text-sm text-text-muted">Remote tab: PR B</div>
          )}
        </div>
        <div className="flex-1 overflow-y-auto">
          <SnapshotDetail
            snapshot={selectedSnap}
            diff={diff}
            onRestore={() => setDialogOpen(true)}
            restoring={restoring}
          />
        </div>
      </div>
      <SnapshotRestoreDialog
        open={dialogOpen}
        pendingConflictCount={pendingConflicts.length}
        onCancel={() => setDialogOpen(false)}
        onConfirm={handleRestore}
        restoring={restoring}
      />
    </div>
  )
}
