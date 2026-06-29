import { useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useBackupStore } from '../../../stores/useBackupStore'
import { BackupHistoryList } from './BackupHistoryList'
import { BackupSnapshotModal } from './BackupSnapshotModal'
import type { SnapshotSummary } from '../../../lib/storage-backup/backup-api'
import type { RestoreConflict } from '../../../lib/storage-backup/restore-guard'
import { runRestore } from '../../../lib/storage-backup/restore-wiring'

/**
 * Relative "time ago" for a past epoch-ms timestamp, reusing the same i18n keys
 * (`settings.sync.time.*`) as the Sync section. Inlined here (rather than
 * importing SyncSection's private helper) so the backup sidebar stays
 * self-contained and Sync remains untouched.
 */
function formatRelativeTime(
  t: ReturnType<typeof useI18nStore.getState>['t'],
  ms: number,
): string {
  const diffSec = Math.max(0, Math.floor((Date.now() - ms) / 1000))
  if (diffSec < 60) return t('settings.sync.time.secondsAgo', { n: diffSec })
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return t('settings.sync.time.minutesAgo', { n: diffMin })
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return t('settings.sync.time.hoursAgo', { n: diffHr })
  const diffDay = Math.floor(diffHr / 24)
  return t('settings.sync.time.daysAgo', { n: diffDay })
}

/**
 * The Storage pane's right-sidebar backup status (Phase 2b), replacing the
 * subsystem-1 "Backups (coming soon)" placeholder. Reflects the ACTIVE host's
 * backup state from `useBackupStore`:
 *   - `status:'error'` → an inline red banner carrying the error (never silent);
 *   - `status:'backing-up'` → an amber "備份中…" line;
 *   - a `lastBackupAt` → a relative "上次備份 {time}" line;
 *   - otherwise → "尚未備份".
 *
 * Cross-device refresh arrives via `applyRemoteBackupDone` (T2b-4); this panel
 * only reflects store state.
 */
export function BackupStatusSidebar() {
  const t = useI18nStore((s) => s.t)
  const hostId = useHostStore((s) => s.activeHostId ?? s.hostOrder[0])
  const state = useBackupStore((s) => (hostId ? s.byHost[hostId] : undefined))

  const status = state?.status ?? 'idle'
  const lastBackupAt = state?.lastBackupAt ?? null
  const lastError = state?.lastError ?? null

  const [selected, setSelected] = useState<SnapshotSummary | null>(null)
  const [restoreBusy, setRestoreBusy] = useState(false)
  const [conflicts, setConflicts] = useState<RestoreConflict[] | null>(null)
  const [restoreError, setRestoreError] = useState<string | null>(null)
  const [restoredOk, setRestoredOk] = useState(false)

  const handleSelect = (snapshot: SnapshotSummary) => {
    setSelected(snapshot)
    setConflicts(null)
    setRestoreError(null)
    setRestoredOk(false)
  }

  const handleRestore = async () => {
    if (!hostId || !selected) return
    setRestoreBusy(true)
    setConflicts(null)
    setRestoreError(null)
    try {
      const result = await runRestore(hostId, selected.id)
      if (result.status === 'blocked') {
        // Block only — list conflicts, never implicitly save/discard (codex P3).
        setConflicts(result.conflicts)
      } else {
        // Done: reconciliation already ran inside runRestore; close + signal.
        setSelected(null)
        setRestoredOk(true)
      }
    } catch (err) {
      setRestoreError(err instanceof Error ? err.message : String(err))
    } finally {
      setRestoreBusy(false)
    }
  }

  return (
    <aside
      data-testid="storage-backups-panel"
      className="flex w-48 shrink-0 flex-col overflow-y-auto border-l border-border-subtle p-3 text-xs text-text-secondary"
    >
      <div className="mb-2 font-medium text-text-primary">
        {t('editor.buffers.backup.title')}
      </div>

      {status === 'error' && (
        <div data-testid="backup-error" className="text-red-400">
          {t('editor.buffers.backup.error', { message: lastError ?? '' })}
        </div>
      )}

      {status === 'backing-up' && (
        <div data-testid="backup-progress" className="text-amber-400">
          {t('editor.buffers.backup.inProgress')}
        </div>
      )}

      {status !== 'backing-up' && lastBackupAt !== null && (
        <div data-testid="backup-last">
          {t('editor.buffers.backup.lastBackup', {
            time: formatRelativeTime(t, lastBackupAt),
          })}
        </div>
      )}

      {status === 'idle' && lastBackupAt === null && (
        <div data-testid="backup-never">{t('editor.buffers.backup.never')}</div>
      )}

      {restoredOk && (
        <div data-testid="backup-restore-success" className="mt-1 text-emerald-400">
          {t('editor.buffers.backup.restore.success')}
        </div>
      )}

      <BackupHistoryList hostId={hostId} onSelect={handleSelect} />

      {hostId && selected && (
        <BackupSnapshotModal
          hostId={hostId}
          snapshot={selected}
          onClose={() => setSelected(null)}
          onRestore={handleRestore}
          restoreDisabled={status === 'backing-up'}
          restoreBusy={restoreBusy}
          conflicts={conflicts}
          restoreError={restoreError}
        />
      )}
    </aside>
  )
}
