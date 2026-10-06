// spa/src/lib/team/approval-ws.ts — the daemon's `approval.request` host event (lead-team spec §6.2):
//   {op:"snapshot", approvals:[…]}  to each new subscriber — replaces this host's open set (PD2);
//   {op:"opened", approval}         on create;
//   {op:"closed", approval}         on every close, carrying decided_by / decided_at.
// Called from useMultiHostEventWs with the per-host closure's hostId. Store first, then the side effects.
// This is the trust boundary: every approval on the wire is checked whole (`isApproval`, types.ts) before the store
// or the dialog sees it, and a frame with any structural error is dropped whole — in particular a snapshot whose
// `approvals` is not an array of valid approvals is NOT an empty set; reading it as one would wipe the host's open
// requests and its queued decisions.
// `toastClosed` and `submitDecision` live in approval-decide.ts (the 409 path words the toast the same way); that file
// imports nothing from here, so there is no import cycle.
import { useApprovalStore } from '../../stores/useApprovalStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { hostLabel, hostLookOf } from '../host-look'
import { submitDecision, toastClosed } from './approval-decide'
import { approvalKindLabel, approvalSessionLabel } from './approval-format'
import { notifyApprovalOpened } from './approval-notify'
import { isApproval, type Approval, type ApprovalEventValue } from './types'

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/** A frame with a known `op` whose body fails the wire shape: logged once per frame, then ignored whole. */
function rejectFrame(op: string, what: string): null {
  console.warn(`[approval-ws] ignoring ${op} frame: ${what}`)
  return null
}

/** The event's `value`: JSON text or an object; null when it is not an approval event we understand or it is malformed. */
export function parseApprovalEvent(value: unknown): ApprovalEventValue | null {
  let o: unknown = value
  if (typeof value === 'string') {
    try {
      o = JSON.parse(value)
    } catch {
      return null
    }
  }
  if (!isRecord(o)) return null
  if (o.op === 'opened' || o.op === 'closed') {
    return isApproval(o.approval) ? { op: o.op, approval: o.approval } : rejectFrame(o.op, 'approval is not the wire shape')
  }
  if (o.op === 'snapshot') {
    if (!Array.isArray(o.approvals)) return rejectFrame('snapshot', 'approvals is not an array')
    const approvals: Approval[] = []
    for (const [i, a] of o.approvals.entries()) {
      if (!isApproval(a)) return rejectFrame('snapshot', `approvals[${i}] is not the wire shape`)
      approvals.push(a)
    }
    return { op: 'snapshot', approvals }
  }
  return null
}

/** The request a decision was queued for is gone from the reconnect snapshot (spec §9.4): say so, send nothing. */
export function toastEndedWhileAway(hostId: string, approval: Approval): void {
  const t = useI18nStore.getState().t
  useUndoToast.getState().show(t('approval.toast.ended_while_away', {
    host: hostLabel(hostId, hostLookOf(hostId)),
    session: approvalSessionLabel(approval.origin),
    kind: approvalKindLabel(t, approval.kind),
  }))
}

export function handleApprovalEvent(hostId: string, value: unknown): void {
  const ev = parseApprovalEvent(value)
  if (!ev) return
  const store = useApprovalStore.getState()
  if (ev.op === 'snapshot') {
    // A new connection (the daemon came back): the queue was filled while it was gone. Take it BEFORE the
    // snapshot replaces the set, so each queued decision is sent at most once per reconnect. A resend that meets
    // the network down again re-queues itself inside submitDecision (`fromQueue`: whatever the host runtime says —
    // it may still read `connected` when the daemon has just died again), for the snapshot after that.
    const queued = store.takeQueued(hostId)
    const vanished = new Set(store.applySnapshot(hostId, ev.approvals))
    for (const q of queued) {
      if (vanished.has(q.approval.id)) toastEndedWhileAway(hostId, q.approval)
      else void submitDecision(hostId, q.approval, q.decision, q.grant, { fromQueue: true })
    }
    return
  }
  if (ev.op === 'opened') {
    // Announce only what was actually added: a duplicate opened (two sockets, a replay) is silent.
    if (store.applyOpened(hostId, ev.approval)) notifyApprovalOpened(hostId, ev.approval)
    return
  }
  // closed: the dialog closes everywhere; only a decision made elsewhere is announced.
  if (store.applyClosed(hostId, ev.approval) === 'elsewhere') toastClosed(hostId, ev.approval)
}
