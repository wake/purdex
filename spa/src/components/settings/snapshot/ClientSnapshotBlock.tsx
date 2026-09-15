import { ArrowCounterClockwise, ArrowsClockwise, Camera } from '@phosphor-icons/react'
import { SettingItem } from '../SettingItem'
import { DeviceStateSection } from '../device-state/DeviceStateSection'
import { readPrevSnapshot } from '../../../lib/snapshot/storage'
import { captureSnapshot } from '../../../lib/snapshot/capture'
import { restoreAll, restoreTabLayout, undoLastRestore, SNAPSHOT_LOCK_OWNER } from '../../../lib/snapshot/restore'
import type { RestoreReport, WorkspaceSnapshot } from '../../../lib/snapshot/types'
import {
  CAPTURE_OWNER,
  errMessage,
  formatRelativeTime,
  restoreAction,
  type SnapshotActions,
  type Status,
  type TFn,
} from './shared'
import { TabsBlock } from './TabsBlock'

const BTN = 'flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active disabled:opacity-50 disabled:cursor-not-allowed'

/**
 * Everything that acts on THIS DEVICE's whole workspace across all hosts:
 * capture, restore all / tab layout, undo, and device-state backup (which
 * lives on the dev host's daemon). Rendered on one host page only.
 *
 * It owns NO action state: `actions` is the host page's single
 * `useSnapshotActions` instance, so a capture here and a rebuild in the host
 * section share one single-flight guard (plan amendment A1). The status line
 * is rendered once, by the owner.
 */
export function ClientSnapshotBlock({ snap, onRefresh, showNoDevHint, actions, t }: {
  snap: WorkspaceSnapshot | null
  onRefresh: () => void
  showNoDevHint: boolean
  actions: SnapshotActions
  t: TFn
}) {
  const { busy, lockedOut, run } = actions
  // Read fresh each render: every action re-renders, so Undo re-enables once a
  // restore writes the `-prev` backup.
  const hasPrev = readPrevSnapshot() !== null

  const handleCapture = () =>
    void run(CAPTURE_OWNER, 'settings.snapshot.toast.capturing', async (): Promise<Status> => {
      try {
        // Only the UI layer reads the clock — the engine takes `now` as a param.
        const res = await captureSnapshot(Date.now())
        return {
          tone: 'success',
          message: t('settings.snapshot.toast.captured', { total: res.total, unresolved: res.unresolved }),
          attrs: { 'data-total': res.total, 'data-unresolved': res.unresolved },
        }
      } catch (e) {
        return { tone: 'error', message: t('settings.snapshot.toast.captureFailed', { reason: errMessage(e) }) }
      }
    }, onRefresh)

  // Restore/undo mutate live sessions and the `-prev` backup, so the page
  // re-reads the snapshot afterwards.
  const restore = (owner: string, action: () => Promise<RestoreReport | null>) =>
    void run(owner, 'settings.snapshot.toast.restoring', () => restoreAction(t, action), onRefresh)

  return (
    <section data-testid="snapshot-client-block" className="mt-8 border-t border-border-default pt-6">
      <h3 className="text-sm text-text-primary">{t('hosts.snapshots.client_title')}</h3>
      <p className="text-xs text-text-secondary mb-2">{t('hosts.snapshots.client_desc')}</p>
      {showNoDevHint && (
        <p data-testid="snapshot-client-no-dev-hint" className="text-xs text-status-warning mb-2">
          {t('hosts.snapshots.client_no_dev')}
        </p>
      )}

      <SettingItem
        label={t('settings.snapshot.capture')}
        description={
          snap
            ? t('settings.snapshot.capturedAt', { time: formatRelativeTime(t, snap.capturedAt) })
            : t('settings.snapshot.neverCaptured')
        }
      >
        <button
          type="button"
          data-testid="snapshot-capture-btn"
          onClick={handleCapture}
          disabled={busy || lockedOut(CAPTURE_OWNER)}
          className={BTN}
        >
          <Camera size={14} className={busy ? 'animate-pulse' : ''} />
          {t('settings.snapshot.capture')}
        </button>
      </SettingItem>

      {snap && (
        <>
          <SettingItem label={t('settings.snapshot.restore.label')} description={t('settings.snapshot.restore.description')}>
            <div className="flex items-center gap-2">
              <button
                type="button"
                data-testid="snapshot-restore-all-btn"
                onClick={() => restore(SNAPSHOT_LOCK_OWNER.restoreAll, () => restoreAll(snap))}
                disabled={busy || lockedOut(SNAPSHOT_LOCK_OWNER.restoreAll)}
                className={BTN}
              >
                <ArrowsClockwise size={14} />
                {t('settings.snapshot.restore.all')}
              </button>
              <button
                type="button"
                data-testid="snapshot-undo-btn"
                onClick={() => restore(SNAPSHOT_LOCK_OWNER.undo, () => undoLastRestore())}
                disabled={busy || !hasPrev || lockedOut(SNAPSHOT_LOCK_OWNER.undo)}
                className={BTN}
              >
                <ArrowCounterClockwise size={14} />
                {t('settings.snapshot.restore.undo')}
              </button>
            </div>
          </SettingItem>
          <TabsBlock
            snap={snap}
            busy={busy || lockedOut(SNAPSHOT_LOCK_OWNER.restoreLayout)}
            onRestoreLayout={() => restore(SNAPSHOT_LOCK_OWNER.restoreLayout, () => restoreTabLayout(snap))}
            t={t}
          />
        </>
      )}

      <DeviceStateSection onRestored={onRefresh} />
    </section>
  )
}
