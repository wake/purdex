// spa/src/components/hosts/peers/RotationControls.tsx — one direction's
// rotation controls (Phase D spec §7.3): the pending badge and its reading,
// then EXACTLY ONE of Commit / Cancel / nothing decided by `rotationOffer(row)`
// — from the row alone, never from this page's memory of its own push — and
// only from a row the loader read AFTER the evidence dial (`stale` false).
// With no rotation pending: the Rotate button (mint → push, then the caller
// refreshes and the fresh row decides), or a disabled one saying why not.
//
// Commit and Cancel are the only places on the page that call
// commitRotation / cancelRotation, always with two args: no body, no `force`
// (spec D-7). A 409 (the peer dialled with the other token between the read
// and the click) is shown as the daemon wrote it and the row is re-read.
import { useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { cancelRotation, commitRotation, type PeerHostRow } from '../../../lib/host-api'
import { rotationOffer } from '../../../lib/peer-pairing'
import { rotateDirection, type Ref } from '../../../lib/peer-pairing-actions'
import { actionApi, errText, type BoundRunFlow } from './flow'

export interface RotationProps {
  /** Whose entry's inbound token this direction presents. */
  holder: Ref
  /** The holder's row: `entry` for the inbound line, `returnEntry` for the outbound line, the candidate's return entry on a pending candidate. */
  row: PeerHostRow
  /** The side's `gateStale` from the loader: true ⇒ no Commit/Cancel, whatever the row says. */
  stale: boolean
  /** Did THIS run dial the holder from the presenter? false ⇒ the reading is "as of the peer's last dial". */
  evidenceDialled: boolean
  /** Stores the minted token on the presenter (PUT on its entry, or POST creating it); null ⇒ Rotate absent with the tooltip. */
  push: ((token: string) => Promise<void>) | null
  label: 'rotate' | 'retry_return' | 'create_return'
  /** 'peer-inbound' | 'peer-outbound' | 'peers-cand-<hostId>' — the prefix of every test id here. */
  testId: string
  busy: boolean
  /** Refresh (spec §7.4) after a Commit or Cancel. */
  onDone: () => void
  /** Runs the Rotate flow under the owner's key; the runner refreshes afterwards. */
  runFlow: BoundRunFlow
}

const NOTE: Record<'' | 'current' | 'prev', string> = {
  current: 'peers.rotation_current',
  prev: 'peers.rotation_prev',
  '': 'peers.rotation_none',
}

const BTN = 'text-xs px-2 py-0.5 rounded cursor-pointer disabled:opacity-50 disabled:cursor-default'

export function RotationControls({ holder, row, stale, evidenceDialled, push, label, testId, busy, onDone, runFlow }: RotationProps) {
  const t = useI18nStore((s) => s.t)
  const [acting, setActing] = useState(false)
  const [gateError, setGateError] = useState('')
  const locked = busy || acting

  // Commit / Cancel: one call, two args, then the refresh. The error text is
  // the daemon's and survives the refresh until the next click.
  const gate = async (fn: typeof commitRotation) => {
    setActing(true)
    setGateError('')
    try {
      await fn(holder.hostId, holder.alias)
    } catch (e) {
      setGateError(errText(e))
    } finally {
      setActing(false)
    }
    onDone()
  }

  const rotate = () => {
    if (!push) return
    setGateError('')
    void runFlow(async (report) => {
      const out = await rotateDirection(holder, push, actionApi(), report)
      if (out.kind === 'pushed') return { hint: t('peers.pushed_hint') }
      const step = out.kind === 'rotate-failed' ? 'mint' : 'push'
      return { error: t('peers.flow_error', { step: t(`peers.step.${step}`), error: out.error }) }
    })
  }

  const offer = rotationOffer(row)
  const gateErrorEl = gateError && (
    <span data-testid={`${testId}-gate-error`} className="text-xs text-status-error whitespace-pre-wrap">{gateError}</span>
  )

  if (offer !== null) {
    return (
      <>
        <span data-testid={`${testId}-rotation`} data-offer={offer} className="text-xs text-status-warning">
          {t('peers.rotation_pending')} — {t(NOTE[row.last_inbound_auth])}
        </span>
        {stale ? (
          // A pending side the loader could not re-read after the dial (or has
          // not yet, while a run is out): no button, and once the run has
          // settled, say so. Refresh is the way out.
          !busy && <span data-testid={`${testId}-stale`} className="text-xs text-text-muted">{t('peers.rotation_stale')}</span>
        ) : (
          <>
            {offer === 'commit' && (
              <button type="button" data-testid={`${testId}-commit`} disabled={locked} onClick={() => void gate(commitRotation)}
                className={`${BTN} bg-accent text-white`}>{t('peers.commit')}</button>
            )}
            {offer === 'cancel' && (
              <button type="button" data-testid={`${testId}-cancel`} disabled={locked} onClick={() => void gate(cancelRotation)}
                className={`${BTN} bg-surface-tertiary text-text-secondary hover:text-text-primary`}>{t('peers.cancel_rotation')}</button>
            )}
            {!evidenceDialled && (
              <span data-testid={`${testId}-as-of`} className="text-xs text-text-muted">{t('peers.rotation_as_of_last_dial')}</span>
            )}
          </>
        )}
        {gateErrorEl}
      </>
    )
  }

  if (push) {
    return (
      <>
        <button type="button" data-testid={`${testId}-rotate`} disabled={locked} onClick={rotate}
          className={`${BTN} bg-surface-tertiary text-text-secondary hover:text-text-primary`}>{t(`peers.${label}`)}</button>
        {gateErrorEl}
      </>
    )
  }

  return (
    <>
      <button type="button" data-testid={`${testId}-rotate-unavailable`} disabled title={t('peers.rotate_unavailable')}
        className={`${BTN} bg-surface-tertiary text-text-muted`}>{t('peers.rotate')}</button>
      {gateErrorEl}
    </>
  )
}
