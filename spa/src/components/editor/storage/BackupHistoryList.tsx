import { useEffect, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useBackupStore } from '../../../stores/useBackupStore'
import { getHistory, type SnapshotSummary } from '../../../lib/storage-backup/backup-api'

/** Fixed logical store for the In-App tree (matches the engine, spec §4.1). */
const STORE_ID = 'inapp:buffer'

/**
 * Relative "time ago" for a past epoch-ms timestamp, reusing the Sync i18n keys.
 * Kept local so the backup sidebar stays self-contained (Sync untouched).
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

/** Map a known trigger to a localized label; fall back to the raw string. */
function triggerLabel(t: ReturnType<typeof useI18nStore.getState>['t'], trigger: string): string {
  if (trigger === 'auto') return t('editor.buffers.backup.trigger.auto')
  if (trigger === 'pre-restore') return t('editor.buffers.backup.trigger.preRestore')
  return trigger
}

interface BackupHistoryListProps {
  hostId: string | undefined
  /** Open the manifest viewer for a row (Phase 2c T6). */
  onSelect?: (snapshot: SnapshotSummary) => void
}

/**
 * The compact snapshot history under the backup status line (Phase 2c T5).
 * Lists `getHistory(host, 'inapp:buffer')` newest-first with device / relative
 * time / trigger and a fork badge. Refetches on mount, active-host switch
 * (`hostId` is an explicit dependency, codex P2-b), own-backup completion, and a
 * cross-device `backup:done` — all three surface through `lastBackupAt`, so it is
 * the single refetch signal alongside `hostId`.
 */
export function BackupHistoryList({ hostId, onSelect }: BackupHistoryListProps) {
  const t = useI18nStore((s) => s.t)
  const lastBackupAt = useBackupStore((s) => (hostId ? s.byHost[hostId]?.lastBackupAt ?? null : null))
  const [rows, setRows] = useState<SnapshotSummary[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    // Defer every setState off the synchronous effect body
    // (react-hooks/set-state-in-effect), matching useStorageTree's pattern.
    Promise.resolve()
      .then(() => {
        if (cancelled) return undefined
        if (!hostId) {
          setRows(null)
          setError(null)
          setLoading(false)
          return undefined
        }
        // Clear the previous host's rows BEFORE awaiting the new fetch, so a
        // host switch never leaves stale (clickable) snapshots from host A
        // visible while hostId is already host B (codex 2c-2 R1 P2).
        setRows(null)
        setLoading(true)
        setError(null)
        return getHistory(hostId, STORE_ID)
      })
      .then((h) => {
        if (cancelled || h === undefined) return
        setRows(h)
        setLoading(false)
      })
      .catch((e: unknown) => {
        if (cancelled) return
        setError(e instanceof Error ? e.message : String(e))
        setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [hostId, lastBackupAt])

  if (!hostId) return null

  return (
    <div className="mt-3" data-testid="backup-history">
      <div className="mb-1 font-medium text-text-primary">{t('editor.buffers.backup.history.heading')}</div>

      {loading && rows === null && (
        <div data-testid="backup-history-loading" className="text-text-muted">
          {t('editor.buffers.backup.history.loading')}
        </div>
      )}

      {error && (
        <div data-testid="backup-history-error" className="text-red-400">
          {t('editor.buffers.backup.history.error', { message: error })}
        </div>
      )}

      {!error && rows !== null && rows.length === 0 && (
        <div data-testid="backup-history-empty" className="text-text-muted">
          {t('editor.buffers.backup.history.empty')}
        </div>
      )}

      {!error && rows !== null && rows.length > 0 && (
        <ul className="flex flex-col gap-1">
          {rows.map((row) => (
            <li key={row.id}>
              <button
                type="button"
                data-testid="backup-history-row"
                onClick={() => onSelect?.(row)}
                className="w-full rounded px-1.5 py-1 text-left hover:bg-surface-hover focus:outline-none focus:ring-1 focus:ring-border-active"
              >
                <div className="flex items-center justify-between gap-1">
                  <span className="truncate text-text-secondary">{formatRelativeTime(t, row.createdAt * 1000)}</span>
                  {row.isFork && (
                    <span
                      data-testid="backup-history-fork"
                      className="shrink-0 rounded bg-amber-500/20 px-1 text-[10px] text-amber-400"
                    >
                      {t('editor.buffers.backup.history.fork')}
                    </span>
                  )}
                </div>
                <div className="flex items-center justify-between gap-1 text-[10px] text-text-muted">
                  <span className="truncate">{row.device}</span>
                  <span className="shrink-0">{triggerLabel(t, row.trigger)}</span>
                </div>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
