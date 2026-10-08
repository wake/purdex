// spa/src/lib/team/approval-notify.ts — the system notification for a new approval request (lead-team spec §6.3):
// `<主機>：<session> 申請成為 lead` through the existing Electron `showNotification` IPC. Raised on `opened` only; a
// snapshot (reconnect) re-shows the dialog but must not re-announce. Electron main dedups on the bare `broadcastTs`
// number across this device's windows, so it must identify the REQUEST, not its `created_at`: two requests created
// in the same millisecond (two hosts, or two sessions on one) would otherwise lose one notification. `approvalBroadcastTs`
// hashes `<hostId>\0<approval.id>` — identical across the windows of one device, distinct per request. U14: the App is
// the only client — no browser fallback.
import { hostLabel, hostLookOf } from '../host-look'
import { useI18nStore } from '../../stores/useI18nStore'
import { fnv1a32, FNV_OFFSET_32 } from './fnv1a'
import { approvalSessionLabel } from './approval-format'
import { leadPayloadOf, selfRelayPayloadOf, type Approval } from './types'

/**
 * The dedup key Electron gets for a request's notification: a stable 53-bit non-negative integer (a safe integer, so
 * it survives the IPC and a `Set<number>` unchanged) — the top 21 bits from a second FNV-1a basis, the low 32 from the
 * standard one. Pure in (hostId, id): every window of this device computes the same value for the same request.
 */
export function approvalBroadcastTs(hostId: string, id: string): number {
  const key = `${hostId}\u0000${id}`
  const lo = fnv1a32(key, FNV_OFFSET_32)
  const hi = fnv1a32(key, FNV_OFFSET_32 ^ 0x5bd1e995) >>> 11
  return hi * 0x1_0000_0000 + lo
}

export function notifyApprovalOpened(hostId: string, approval: Approval): void {
  if (!window.electronAPI?.showNotification) return
  const t = useI18nStore.getState().t
  const host = hostLabel(hostId, hostLookOf(hostId))
  const session = approvalSessionLabel(approval.origin)
  // The kind decides the words (spec §8.7): a relay request names the usage, a lead request its reason.
  const selfRelay = approval.kind === 'self_relay'
  const pct = selfRelay ? Math.round(selfRelayPayloadOf(approval).used_percentage) : 0
  void window.electronAPI.showNotification({
    title: selfRelay ? t('approval.notify.title_self_relay', { host, session, pct }) : t('approval.notify.title', { host, session }),
    body: selfRelay ? t('approval.dialog.self_relay_note') : leadPayloadOf(approval).reason,
    sessionCode: '',
    eventName: 'ApprovalRequest',
    broadcastTs: approvalBroadcastTs(hostId, approval.id),
    // The request id: the click restores a minimized dialog only while THIS request is still open (U22 (b)).
    action: { kind: 'open-approval', hostId, requestId: approval.id },
  })
}
