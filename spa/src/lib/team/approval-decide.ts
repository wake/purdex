// spa/src/lib/team/approval-decide.ts — one decision, sent once (lead-team spec §6.3, §6.5, §9.4). Shared by the
// dialog's click and by the reconnect resend (P3b), so both agree on what each answer means:
//   200                      → closed (ours: no toast), then back to the requester's tab (U22, approval-goto.ts);
//   409 + approval           → someone else got there first: close, toast who;
//   network, host down       → the daemon is restarting: keep the decision for the reconnect snapshot;
//   network, host connected  → nothing would resend it (the resend rides the reconnect snapshot): toast, leave it open —
//                              except a resend FROM the queue (`fromQueue`), which always goes back to the queue: the
//                              host runtime may still read `connected` when the daemon has just died again, and a
//                              decision the person already made must not be lost to that window;
//   host_removed             → this device no longer has the host: drop the request, nothing to replay against;
//   404                      → the daemon no longer knows the request: close, toast the failure;
//   anything else            → toast the failure, leave the request open.
// And whatever the answer, when the host was forgotten while it was out (`hostEpoch` moved: removed or re-pointed, #1978)
// the answer belongs to the old daemon: nothing is written to the store or the UI, and no decision is queued.
// `toastClosed` lives here too (the plan had it in approval-ws.ts, which P3b creates): the WS `closed` branch and the
// 409 path must word the "handled elsewhere" toast the same way.
//
// A 409 is never read as "our own lost answer": `decided_by.label` is `Purdex.app @ <hostname>` for every window on
// one machine (and `Purdex.app` when the hostname is unknown), so matching it against our label would swallow a real
// loser's toast (U6). `decidedHere` only disambiguates the WS `closed` that follows our own 200. Telling our own
// retry from a sibling window needs a stable per-renderer client id on the wire — a follow-up, not this file.
import { useApprovalStore, type Decision } from '../../stores/useApprovalStore'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { hostLabel, hostLookOf } from '../host-look'
import { ApprovalApiError, decideApproval, setSelfRelayPause } from './approval-api'
import { closedToastText } from './approval-format'
import { gotoRequester } from './approval-goto'
import { clientDescriptor } from './client-label'
import { startAdoptionWait } from './adoption-wait'
import { adoptPayloadOf, type Approval, type Grant } from './types'

export type DecideOutcome = 'closed' | 'decided_elsewhere' | 'queued' | 'failed'

/** The "handled elsewhere" toast (spec §6.3): who closed it, on which host, for which session. */
export function toastClosed(hostId: string, approval: Approval): void {
  const t = useI18nStore.getState().t
  useUndoToast.getState().show(closedToastText(t, hostLabel(hostId, hostLookOf(hostId)), approval))
}

/** The failure toast: the error code, with the daemon's detail when it gave one (`network: Failed to fetch`). */
function toastFailed(err: ApprovalApiError): void {
  const t = useI18nStore.getState().t
  useUndoToast.getState().show(t('approval.toast.failed', { code: err.detail !== '' ? `${err.code}: ${err.detail}` : err.code }))
}

export interface SubmitOptions {
  /** The decision was taken from the reconnect queue (approval-ws.ts): a network failure re-queues it, whatever the host status. */
  fromQueue?: boolean
  /** A pause queued with the decision (「這個 session 不再詢問」 clicked while disconnected): sent first, best effort;
   *  a network failure keeps it with the decision if that is re-queued too. */
  pauseSession?: string
  /** The host's `hostEpoch` when the person clicked, for a caller that awaited something of its own first (the dialog's
   *  pause). Default: the value now. A forget since then voids the send and its answer. */
  epoch?: number
}

export async function submitDecision(hostId: string, approval: Approval, decision: Decision, grant?: Grant, opts: SubmitOptions = {}): Promise<DecideOutcome> {
  const epoch = opts.epoch ?? (useApprovalStore.getState().hostEpoch[hostId] ?? 0)
  // The host was removed or re-pointed since the click: this decision is the old daemon's, the id is now another's.
  const forgotten = (): boolean => (useApprovalStore.getState().hostEpoch[hostId] ?? 0) !== epoch
  if (forgotten()) return 'failed'
  let pauseLeft = opts.pauseSession
  if (pauseLeft) {
    try {
      await setSelfRelayPause(hostId, pauseLeft, 'off')
      pauseLeft = undefined
    } catch (e: unknown) {
      if (forgotten()) return 'failed'
      const code = e instanceof ApprovalApiError ? e.code : 'network'
      if (code !== 'network') {
        pauseLeft = undefined
        useUndoToast.getState().show(useI18nStore.getState().t('approval.dialog.pause_failed', { code }))
      }
    }
  }
  const client = await clientDescriptor()
  if (forgotten()) return 'failed'
  // Before the send: the daemon's `closed` broadcast can outrun the HTTP answer, and it must read as ours.
  useApprovalStore.getState().markDecidedHere(hostId, approval.id)
  try {
    const closed = await decideApproval(hostId, approval.id, {
      decision,
      ...(decision === 'approve' && grant ? { grant } : {}),
      client,
    })
    if (forgotten()) return 'failed'
    useApprovalStore.getState().applyClosed(hostId, closed)
    // U22 (a): a decision made here — a click, or a queued one resent on reconnect — goes back to the requester's tab,
    // behind the next dialog when another request is open. Only this 200 does: never a 409, a network failure or the
    // WS `closed` branch. A navigation that fails must not turn a decision the daemon took into a failure.
    try {
      gotoRequester(hostId, closed)
    } catch (e: unknown) {
      console.warn('[approval-decide] could not switch to the requester:', e)
    }
    // A REMOTE adopt (X3c): the approve only recorded consent (a `joining` membership); the outcome is waited on outside
    // the dialog, which this 200 has just closed. A local target is done at the approve, as before.
    if (decision === 'approve' && approval.kind === 'adopt') {
      const adopt = adoptPayloadOf(approval)
      if (adopt.target_host_id !== '') startAdoptionWait(hostId, approval.id, adopt)
    }
    if (pauseLeft) {
      // The pause hit the network but the decision went through (PR #1742 attacker A-1): say so, as the
      // connected path does — the session was not paused and may ask again.
      useUndoToast.getState().show(useI18nStore.getState().t('approval.dialog.pause_failed', { code: 'network' }))
    }
    return 'closed'
  } catch (e: unknown) {
    if (forgotten()) return 'failed'
    const err = e instanceof ApprovalApiError ? e : new ApprovalApiError(0, 'unknown', e instanceof Error ? e.message : String(e))
    const store = useApprovalStore.getState()
    if (err.code === 'network') {
      // Never reached the daemon (or the answer was lost): a `closed` that arrives meanwhile is someone else's.
      store.unmarkDecidedHere(hostId, approval.id)
      // Queue while the host is down: the queue is drained by the reconnect snapshot (P3b), so with the WS still
      // `connected` nothing would ever resend a fresh click — the person clicks again instead. A decision that came
      // FROM the queue is re-queued regardless: the runtime lags the daemon dying again, and it was already made.
      if (opts.fromQueue || useHostStore.getState().runtime[hostId]?.status !== 'connected') {
        store.queueDecision(hostId, approval, decision, grant, pauseLeft)
        return 'queued'
      }
      toastFailed(err)
      return 'failed'
    }
    if (err.code === 'host_removed') {
      store.applyClosed(hostId, approval)
      return 'failed'
    }
    if (err.status === 409 && err.approval) {
      // `applyClosed` also drops our `decidedHere` mark. Always toast: see the header on why the label is no identity.
      store.applyClosed(hostId, err.approval)
      toastClosed(hostId, err.approval)
      return 'decided_elsewhere'
    }
    if (err.status === 404) {
      store.applyClosed(hostId, approval)
    } else {
      store.unmarkDecidedHere(hostId, approval.id)
    }
    toastFailed(err)
    return 'failed'
  }
}
