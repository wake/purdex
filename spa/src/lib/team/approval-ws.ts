// spa/src/lib/team/approval-ws.ts — the daemon's `approval.request` host event (lead-team spec §6.2):
//   {op:"snapshot", approvals:[…]}  to each new subscriber — replaces this host's open set (PD2);
//   {op:"opened", approval}         on create;
//   {op:"closed", approval}         on every close, carrying decided_by / decided_at.
// Called from useMultiHostEventWs with the per-host closure's hostId. Store first, then the side effects.
// `toastClosed` lives in approval-decide.ts (the 409 path words the toast the same way); it is imported, not redefined.
import { useApprovalStore } from '../../stores/useApprovalStore'
import { toastClosed } from './approval-decide'
import type { Approval, ApprovalEventValue } from './types'

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function isApproval(v: unknown): v is Approval {
  return isRecord(v)
    && typeof v.id === 'string' && v.id !== ''
    && typeof v.state === 'string'
    && isRecord(v.origin)
    && typeof v.created_at === 'number'
}

/** The event's `value`: JSON text or an object; null when it is not an approval event we understand. */
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
  if ((o.op === 'opened' || o.op === 'closed') && isApproval(o.approval)) return { op: o.op, approval: o.approval }
  if (o.op === 'snapshot') return { op: 'snapshot', approvals: Array.isArray(o.approvals) ? o.approvals.filter(isApproval) : [] }
  return null
}

export function handleApprovalEvent(hostId: string, value: unknown): void {
  const ev = parseApprovalEvent(value)
  if (!ev) return
  const store = useApprovalStore.getState()
  if (ev.op === 'snapshot') {
    store.applySnapshot(hostId, ev.approvals)
    return
  }
  if (ev.op === 'opened') {
    store.applyOpened(hostId, ev.approval)
    return
  }
  // closed: the dialog closes everywhere; only a decision made elsewhere is announced.
  if (store.applyClosed(hostId, ev.approval) === 'elsewhere') toastClosed(hostId, ev.approval)
}
