// spa/src/components/settings/profile/StopSyncControl.tsx — Settings › Profile › Current: "Stop sync", its
// confirmation, its OUTCOME, and what is left to do when the outcome was half of one.
//
// WHAT A DETACH IS (lib/profile/start.ts, `detachMaster`). The master is cleared at once — "the user said stop,
// so it stops, NOW" — and the daemon is told afterwards, best effort, for up to 15 s. So there are two facts and
// they can differ: this device HAS stopped syncing (always), and the daemon may not know. The second one matters
// to everybody else: the daemon keeps this device's attachment, and an attached profile cannot be deleted by
// anyone. It is written down by start.ts (`useProfileStore.pendingDetaches`) and said HERE until a retry gets
// through or the user gives up — across a reload, because the master it was about is gone and nothing else
// remembers which attachment is left. ONE NOTICE PER ATTACHMENT (P3d-3, review F3): switching master twice can
// leave two, and each has its own Try again, its own Dismiss and its own "tried" — a retry of one says nothing
// about the other.
//
// THE ATTACHMENT IS ON ONE DAEMON (review F4): the one the master was attached at, whose address the record
// carries (`endpoint`, as `masterEndpoint`). A host's address is the user's to edit, and a retry
// that followed the host id to a new address would remove this device from a profile of the same id on ANOTHER
// daemon. start.ts refuses to send such a retry; here the same comparison — `endpointOfHost`, the one writer of
// the form — decides what is OFFERED: Try again only while the host still has that address; otherwise both
// addresses and what to do, and Dismiss. A host that is gone, and a record older than the field, likewise.
//
// WHY THIS IS ITS OWN COMPONENT, MOUNTED OUTSIDE THE "ATTACHED" HALF OF THE BLOCK. The confirmation has to stay up,
// `busy`, until the answer arrives — and the half of the block that holds everything else about a master is
// unmounted the moment the master is cleared, i.e. one microtask after Confirm.
import { useEffect, useRef, useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { endpointOfHost, pendingDetachKey, useProfileStore, type PendingDetach } from '../../../stores/useProfileStore'
import { useHostStore } from '../../../stores/useHostStore'
import { detachMaster, dismissPendingDetach, retryPendingDetach } from '../../../lib/profile/start'
import { ConfirmDialog } from '../../ConfirmDialog'
import { SettingItem } from '../SettingItem'
import { pendingDetachTestId } from './profile-rules'

const BTN =
  'shrink-0 flex items-center gap-1.5 rounded-md border border-border-default px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed'

type LeftState = 'retryable' | 'endpoint-changed' | 'host-gone' | 'endpoint-unknown'

/** start.ts, `dropAttachment`, in the same order: what it would answer decides what is offered. */
function leftStateOf(left: PendingDetach, hostNow: { ip: string; port: number } | undefined): LeftState {
  if (hostNow === undefined) return 'host-gone'
  if (left.endpoint === null) return 'endpoint-unknown'
  return endpointOfHost(hostNow) === left.endpoint ? 'retryable' : 'endpoint-changed'
}

/** A `detail` that is one of start.ts's "not sent" reasons is no reason to show: the state says it in words. */
const NOT_SENT = new Set(['endpoint-changed', 'host-gone', 'endpoint-unknown'])

export function StopSyncControl({ attached }: { attached: boolean }) {
  const t = useI18nStore((s) => s.t)
  const lefts = useProfileStore((s) => s.pendingDetaches)
  const [confirming, setConfirming] = useState(false)
  const [stopping, setStopping] = useState(false)

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

  return (
    <>
      {attached && (
        <SettingItem label={t('settings.profile.current.stop')} description={t('settings.profile.current.stop_desc')}>
          <button type="button" data-testid="profile-stop-sync" onClick={() => setConfirming(true)} className={BTN}>
            {t('settings.profile.current.stop')}
          </button>
        </SettingItem>
      )}

      {lefts.length > 0 && (
        <ul className="flex flex-col">
          {lefts.map((left) => (
            <li key={pendingDetachKey(left)} data-testid={`profile-detach-item-${pendingDetachTestId(left)}`}>
              <LeftoverItem left={left} />
            </li>
          ))}
        </ul>
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

/** One record, with the state of ITS retry. Keyed by the record's key, so a record that goes takes its state with it. */
function LeftoverItem({ left }: { left: PendingDetach }) {
  const hostNow = useHostStore((s) => s.hosts[left.hostId])
  const [retrying, setRetrying] = useState(false)
  const [retryFailed, setRetryFailed] = useState(false)
  const key = pendingDetachKey(left)
  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])

  const retry = async () => {
    if (retrying) return
    setRetrying(true)
    setRetryFailed(false)
    let failed = true
    try {
      const r = await retryPendingDetach(key)
      // Only a request that went out and failed was "tried"; one start.ts would not send shows as its state.
      failed = !r.ok && r.reason === 'daemon-not-told'
    } catch {
      // start.ts does not throw; if it ever does, it was tried and did not get through
    }
    // A retry that got through has removed the record — and this component with it.
    if (!alive.current) return
    setRetryFailed(failed)
    setRetrying(false)
  }

  return <Leftover left={left} hostName={hostNow?.name ?? left.hostId} state={leftStateOf(left, hostNow)} hostNow={hostNow} retrying={retrying} retryFailed={retryFailed} onRetry={() => void retry()} onDismiss={() => void dismissPendingDetach(key)} />
}

interface LeftoverProps {
  left: PendingDetach
  hostName: string
  state: LeftState
  hostNow: { ip: string; port: number } | undefined
  retrying: boolean
  retryFailed: boolean
  onRetry: () => void
  onDismiss: () => void
}

function Leftover({ left, hostName, state, hostNow, retrying, retryFailed, onRetry, onDismiss }: LeftoverProps) {
  const t = useI18nStore((s) => s.t)
  const was = left.endpoint ?? ''
  return (
    <div data-testid="profile-detach-leftover" data-state={state} data-host={left.hostId} data-profile={left.profileId} role="status" className="mt-2 text-xs text-yellow-500">
      <p>
        {state === 'retryable' && !NOT_SENT.has(left.detail)
          ? t('settings.profile.detach.not_told', { host: hostName, detail: left.detail })
          : t('settings.profile.detach.not_told_plain', { host: hostName })}
      </p>
      {state === 'endpoint-changed' && hostNow !== undefined && (
        <>
          <p>{t('settings.profile.detach.endpoint_changed', { host: hostName, was, now: endpointOfHost(hostNow) })}</p>
          <p>{t('settings.profile.detach.endpoint_changed_how')}</p>
        </>
      )}
      {state === 'host-gone' && (
        <>
          <p>{left.endpoint === null ? t('settings.profile.detach.host_gone_unknown') : t('settings.profile.detach.host_gone', { was })}</p>
          <p>{t('settings.profile.detach.host_gone_how')}</p>
        </>
      )}
      {state === 'endpoint-unknown' && <p>{t('settings.profile.detach.endpoint_unknown')}</p>}
      <p>{t(state === 'retryable' ? 'settings.profile.detach.consequence' : 'settings.profile.detach.consequence_plain')}</p>
      <div className="mt-1.5 flex flex-wrap items-center gap-2">
        {state === 'retryable' && (
          <button type="button" data-testid="profile-detach-retry" aria-busy={retrying} disabled={retrying} onClick={onRetry} className={BTN}>
            <ArrowsClockwise size={14} className={retrying ? 'animate-spin' : ''} />
            {t('settings.profile.detach.retry')}
          </button>
        )}
        <button type="button" data-testid="profile-detach-dismiss" title={t('settings.profile.detach.dismiss_hint')} disabled={retrying} onClick={onDismiss} className={BTN}>
          {t('settings.profile.detach.dismiss')}
        </button>
        {state === 'retryable' && retryFailed && (
          <span data-testid="profile-detach-retry-failed" className="text-text-secondary">{t('settings.profile.detach.retry_failed')}</span>
        )}
      </div>
    </div>
  )
}
