import { useEffect, useState } from 'react'
import { X, File as FileIcon, Folder } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import {
  getSnapshot,
  type SnapshotSummary,
  type SnapshotDetail,
} from '../../../lib/storage-backup/backup-api'
import type { RestoreConflict } from '../../../lib/storage-backup/restore-guard'

interface BackupSnapshotModalProps {
  hostId: string
  snapshot: SnapshotSummary
  onClose: () => void
  /** Trigger the restore flow (Phase 2c T7 supplies the live handler). */
  onRestore?: () => void
  /** Disable Restore (e.g. host is currently backing up — T7). */
  restoreDisabled?: boolean
  /** Restore in progress — disables + relabels the button (T7). */
  restoreBusy?: boolean
  /** Pre-flight conflicts that blocked the restore; restore was NOT applied (T7). */
  conflicts?: RestoreConflict[] | null
  /** Inline restore error; the tree is guaranteed untouched/rolled back (T7). */
  restoreError?: string | null
}

/** Human-readable byte size. */
function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

/**
 * The snapshot manifest viewer (Phase 2c T6). Opened from a history row, it
 * fetches `getSnapshot(id)` and lists each manifest entry (path / kind / size /
 * words) WITHOUT downloading any blob, plus a header (device / time / trigger /
 * fork) and Close + Restore actions. The Restore wiring is Phase 2c T7.
 */
export function BackupSnapshotModal({
  hostId,
  snapshot,
  onClose,
  onRestore,
  restoreDisabled,
  restoreBusy,
  conflicts,
  restoreError,
}: BackupSnapshotModalProps) {
  const t = useI18nStore((s) => s.t)
  const [detail, setDetail] = useState<SnapshotDetail | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setDetail(null)
    setError(null)
    getSnapshot(hostId, snapshot.id)
      .then((d) => {
        if (!cancelled) setDetail(d)
      })
      .catch((e: unknown) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e))
      })
    return () => {
      cancelled = true
    }
  }, [hostId, snapshot.id])

  const createdAt = new Date(snapshot.createdAt * 1000).toLocaleString()

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      data-testid="backup-snapshot-modal"
    >
      <div className="flex max-h-[80vh] w-[520px] flex-col rounded-lg border border-border-default bg-surface-primary shadow-lg">
        {/* Header */}
        <div className="flex items-start justify-between border-b border-border-subtle px-4 py-3">
          <div className="min-w-0">
            <h3 className="flex items-center gap-2 text-sm font-medium text-text-primary">
              {t('editor.buffers.backup.viewer.title')}
              {snapshot.isFork && (
                <span className="rounded bg-amber-500/20 px-1 text-[10px] text-amber-400">
                  {t('editor.buffers.backup.viewer.fork')}
                </span>
              )}
            </h3>
            <p className="mt-1 truncate text-xs text-text-muted">
              {t('editor.buffers.backup.viewer.device', { device: snapshot.device })}
            </p>
            <p className="truncate text-xs text-text-muted">
              {t('editor.buffers.backup.viewer.time', { time: createdAt })}
            </p>
            <p className="truncate text-xs text-text-muted">
              {t('editor.buffers.backup.viewer.trigger', { trigger: snapshot.trigger })}
            </p>
          </div>
          <button
            data-testid="backup-snapshot-close"
            aria-label={t('editor.buffers.backup.viewer.close')}
            onClick={onClose}
            className="rounded p-1 text-text-muted hover:text-text-primary"
          >
            <X size={16} />
          </button>
        </div>

        {/* Body — manifest file list (no blob download) */}
        <div className="min-h-[120px] flex-1 overflow-y-auto px-4 py-3 text-xs">
          {error && (
            <p data-testid="backup-snapshot-error" className="text-red-400">
              {t('editor.buffers.backup.viewer.error', { message: error })}
            </p>
          )}
          {!error && detail === null && (
            <p data-testid="backup-snapshot-loading" className="text-text-muted">
              {t('editor.buffers.backup.viewer.loading')}
            </p>
          )}
          {!error && detail !== null && detail.manifest.length === 0 && (
            <p data-testid="backup-snapshot-empty" className="text-text-muted">
              {t('editor.buffers.backup.viewer.empty')}
            </p>
          )}
          {!error && detail !== null && detail.manifest.length > 0 && (
            <>
              <div className="mb-1 font-medium text-text-secondary">
                {t('editor.buffers.backup.viewer.files', { count: detail.manifest.length })}
              </div>
              <ul className="flex flex-col gap-0.5">
                {detail.manifest.map((entry) => (
                  <li
                    key={entry.path}
                    data-testid="backup-snapshot-entry"
                    className="flex items-center justify-between gap-2 text-text-secondary"
                  >
                    <span className="flex min-w-0 items-center gap-1.5">
                      {entry.kind === 'dir' ? (
                        <Folder size={12} className="shrink-0 text-text-muted" />
                      ) : (
                        <FileIcon size={12} className="shrink-0 text-text-muted" />
                      )}
                      <span className="truncate">{entry.path}</span>
                    </span>
                    {entry.kind === 'file' && (
                      <span className="shrink-0 text-[10px] text-text-muted">
                        {formatBytes(entry.size)} · {entry.words}w
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            </>
          )}
        </div>

        {/* Blocked-by-conflicts notice — restore was NOT applied (T7, codex P3:
            list only, no implicit save/discard). */}
        {conflicts && conflicts.length > 0 && (
          <div
            data-testid="backup-restore-conflicts"
            className="border-t border-border-subtle px-4 py-3 text-xs text-amber-400"
          >
            <div className="font-medium">{t('editor.buffers.backup.restore.blockedTitle')}</div>
            <div className="mt-0.5 text-text-muted">{t('editor.buffers.backup.restore.blockedHint')}</div>
            <ul className="mt-1 flex flex-col gap-0.5">
              {conflicts.map((c, i) => (
                <li key={`${c.type}-${c.tabId}-${c.filePath}-${i}`} className="truncate">
                  {c.type === 'dirty'
                    ? t('editor.buffers.backup.restore.conflictDirty', { path: c.filePath })
                    : t('editor.buffers.backup.restore.conflictLocked', { path: c.filePath })}
                </li>
              ))}
            </ul>
          </div>
        )}

        {restoreError && (
          <div
            data-testid="backup-restore-error"
            className="border-t border-border-subtle px-4 py-3 text-xs text-red-400"
          >
            {t('editor.buffers.backup.restore.error', { message: restoreError })}
          </div>
        )}

        {/* Footer */}
        <div className="flex items-center justify-end gap-2 border-t border-border-subtle px-4 py-3">
          <button
            onClick={onClose}
            className="rounded-md px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary"
          >
            {t('editor.buffers.backup.viewer.close')}
          </button>
          <button
            data-testid="backup-snapshot-restore"
            onClick={() => onRestore?.()}
            disabled={restoreDisabled || restoreBusy}
            className="rounded-md bg-accent px-3 py-1.5 text-xs text-text-inverse hover:bg-accent-hover disabled:opacity-50"
          >
            {restoreBusy
              ? t('editor.buffers.backup.viewer.restoring')
              : t('editor.buffers.backup.viewer.restore')}
          </button>
        </div>
      </div>
    </div>
  )
}
