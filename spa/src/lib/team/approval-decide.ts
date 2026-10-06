// spa/src/lib/team/approval-decide.ts — one decision, sent once (lead-team spec §6.3, §6.5, §9.4). Shared by the
// dialog's click and by the reconnect resend (P3b), so both agree on what each answer means:
//   200                      → closed (ours: no toast);
//   409 + approval           → someone else got there first: close, toast who;
//   network, host down       → the daemon is restarting: keep the decision for the reconnect snapshot;
//   network, host connected  → nothing would resend it (the resend rides the reconnect snapshot): toast, leave it open;
//   host_removed             → this device no longer has the host: drop the request, nothing to replay against;
//   404                      → the daemon no longer knows the request: close, toast the failure;
//   anything else            → toast the failure, leave the request open.
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
import { ApprovalApiError, decideApproval } from './approval-api'
import { closedToastText } from './approval-format'
import { clientDescriptor } from './client-label'
import type { Approval, Grant } from './types'

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

export async function submitDecision(hostId: string, approval: Approval, decision: Decision, grant?: Grant): Promise<DecideOutcome> {
  const client = await clientDescriptor()
  // Before the send: the daemon's `closed` broadcast can outrun the HTTP answer, and it must read as ours.
  useApprovalStore.getState().markDecidedHere(hostId, approval.id)
  try {
    const closed = await decideApproval(hostId, approval.id, {
      decision,
      ...(decision === 'approve' && grant ? { grant } : {}),
      client,
    })
    useApprovalStore.getState().applyClosed(hostId, closed)
    return 'closed'
  } catch (e: unknown) {
    const err = e instanceof ApprovalApiError ? e : new ApprovalApiError(0, 'unknown', e instanceof Error ? e.message : String(e))
    const store = useApprovalStore.getState()
    if (err.code === 'network') {
      // Never reached the daemon (or the answer was lost): a `closed` that arrives meanwhile is someone else's.
      store.unmarkDecidedHere(hostId, approval.id)
      // Queue only while the host is down: the queue is drained by the reconnect snapshot (P3b), so with the WS still
      // `connected` nothing would ever resend it — the person clicks again instead.
      if (useHostStore.getState().runtime[hostId]?.status !== 'connected') {
        store.queueDecision(hostId, approval, decision, grant)
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
