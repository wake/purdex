// spa/src/lib/team/approval-decide.ts — one decision, sent once (lead-team spec §6.3, §6.5, §9.4). Shared by the
// dialog's click and by the reconnect resend (P3b), so both agree on what each answer means:
//   200                      → closed (ours: no toast);
//   409 + approval           → someone else got there first: close, toast who (unless it was this app's own lost answer);
//   network (status 0)       → the daemon is restarting: keep the decision for the reconnect snapshot;
//   host_removed             → this device no longer has the host: drop the request, nothing to replay against;
//   404                      → the daemon no longer knows the request: close, toast the failure;
//   anything else            → toast the failure, leave the request open.
// `toastClosed` lives here too (the plan had it in approval-ws.ts, which P3b creates): the WS `closed` branch and the
// 409 path must word the "handled elsewhere" toast the same way.
import { useApprovalStore, type Decision } from '../../stores/useApprovalStore'
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
      store.queueDecision(hostId, approval, decision, grant)
      return 'queued'
    }
    if (err.code === 'host_removed') {
      store.applyClosed(hostId, approval)
      return 'failed'
    }
    if (err.status === 409 && err.approval) {
      store.applyClosed(hostId, err.approval)
      if (err.approval.decided_by?.label !== client.label) toastClosed(hostId, err.approval)
      return 'decided_elsewhere'
    }
    if (err.status === 404) {
      store.applyClosed(hostId, approval)
    } else {
      store.unmarkDecidedHere(hostId, approval.id)
    }
    const t = useI18nStore.getState().t
    useUndoToast.getState().show(t('approval.toast.failed', { code: err.code }))
    return 'failed'
  }
}
