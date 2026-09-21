// spa/src/components/settings/profile/StopSyncControl.tsx — Settings › Profile › Current: "Stop sync", its
// confirmation, its OUTCOME, and what is left to do when the outcome was half of one.
//
// WHAT A DETACH IS (lib/profile/start.ts, `detachMaster`). The master is cleared at once — "the user said stop,
// so it stops, NOW" — and the daemon is told afterwards, best effort, for up to 15 s. So there are two facts and
// they can differ: this device HAS stopped syncing (always), and the daemon may not know. The second one matters
// to everybody else: the daemon keeps this device's attachment, and an attached profile cannot be deleted by
// anyone. It is written down by start.ts (`useProfileStore.pendingDetach`) and said HERE until a retry gets
// through or the user gives up — across a reload, because the master it was about is gone and nothing else
// remembers which attachment is left.
//
// WHY THIS IS ITS OWN COMPONENT, MOUNTED OUTSIDE THE "ATTACHED" HALF OF THE BLOCK. The confirmation has to stay up,
// `busy`, until the answer arrives — and the half of the block that holds everything else about a master is
// unmounted the moment the master is cleared, i.e. one microtask after Confirm.
import { useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useProfileStore } from '../../../stores/useProfileStore'
import { useHostStore } from '../../../stores/useHostStore'
import { detachMaster, retryPendingDetach } from '../../../lib/profile/start'
import { ConfirmDialog } from '../../ConfirmDialog'
import { SettingItem } from '../SettingItem'

const BTN =
  'shrink-0 flex items-center gap-1.5 rounded-md border border-border-default px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed'

export function StopSyncControl({ attached }: { attached: boolean }) {
  const t = useI18nStore((s) => s.t)
  const left = useProfileStore((s) => s.pendingDetach)
  const clearPendingDetach = useProfileStore((s) => s.clearPendingDetach)
  const leftHostName = useHostStore((s) => (left === null ? null : (s.hosts[left.hostId]?.name ?? null)))
  const [confirming, setConfirming] = useState(false)
  const [stopping, setStopping] = useState(false)
  const [retrying, setRetrying] = useState(false)
  const [retryFailed, setRetryFailed] = useState(false)

  const stop = async () => {
    if (stopping) return
    setStopping(true)
    try {
      // The answer is not needed here: a daemon that was not told is in the store by the time this returns.
      await detachMaster()
    } catch {
      // start.ts does not throw; if it ever does, the dialog must still let go
    } finally {
      setStopping(false)
      setConfirming(false)
    }
  }

  const retry = async () => {
    if (retrying) return
    setRetrying(true)
    setRetryFailed(false)
    try {
      const r = await retryPendingDetach()
      setRetryFailed(!r.ok)
    } catch {
      setRetryFailed(true)
    } finally {
      setRetrying(false)
    }
  }

  return (
    <>
      {attached && (
        <SettingItem label={t('settings.profile.current.stop')} description={t('settings.profile.current.stop_desc')}>
          <button type="button" data-testid="profile-stop-sync" onClick={() => setConfirming(true)} className={BTN}>
            {t('settings.profile.current.stop')}
          </button>
        </SettingItem>
      )}

      {left !== null && (
        <div data-testid="profile-detach-leftover" data-host={left.hostId} data-profile={left.profileId} role="status" className="mt-2 text-xs text-yellow-500">
          <p>{t('settings.profile.detach.not_told', { host: leftHostName ?? left.hostId, detail: left.detail })}</p>
          <p>{t('settings.profile.detach.consequence')}</p>
          <div className="mt-1.5 flex flex-wrap items-center gap-2">
            <button type="button" data-testid="profile-detach-retry" aria-busy={retrying} disabled={retrying} onClick={() => void retry()} className={BTN}>
              <ArrowsClockwise size={14} className={retrying ? 'animate-spin' : ''} />
              {t('settings.profile.detach.retry')}
            </button>
            <button
              type="button"
              data-testid="profile-detach-dismiss"
              title={t('settings.profile.detach.dismiss_hint')}
              disabled={retrying}
              onClick={() => clearPendingDetach(left.hostId, left.profileId)}
              className={BTN}
            >
              {t('settings.profile.detach.dismiss')}
            </button>
            {retryFailed && (
              <span data-testid="profile-detach-retry-failed" className="text-text-secondary">{t('settings.profile.detach.retry_failed')}</span>
            )}
          </div>
        </div>
      )}

      {confirming && (
        <ConfirmDialog
          testIdPrefix="profile-stop-sync"
          busy={stopping}
          title={t('settings.profile.current.stop_title')}
          body={t('settings.profile.current.stop_body')}
          confirmLabel={t('settings.profile.current.stop')}
          onCancel={() => setConfirming(false)}
          onConfirm={() => void stop()}
        />
      )}
    </>
  )
}
