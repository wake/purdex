// spa/src/lib/team/approval-notify.ts — the system notification for a new approval request (lead-team spec §6.3):
// `<主機>：<session> 申請成為 lead` through the existing Electron `showNotification` IPC. Raised on `opened` only; a
// snapshot (reconnect) re-shows the dialog but must not re-announce. `broadcastTs` is the request's `created_at`:
// Electron main dedups on it across this device's windows. U14: the App is the only client — no browser fallback.
import { getPlatformCapabilities } from '../platform'
import { hostLabel, hostLookOf } from '../host-look'
import { useI18nStore } from '../../stores/useI18nStore'
import { approvalSessionLabel } from './approval-format'
import { leadPayloadOf, type Approval } from './types'

export function notifyApprovalOpened(hostId: string, approval: Approval): void {
  if (!getPlatformCapabilities().canNotification || !window.electronAPI?.showNotification) return
  const t = useI18nStore.getState().t
  void window.electronAPI.showNotification({
    title: t('approval.notify.title', { host: hostLabel(hostId, hostLookOf(hostId)), session: approvalSessionLabel(approval.origin) }),
    body: leadPayloadOf(approval).reason,
    sessionCode: '',
    eventName: 'ApprovalRequest',
    broadcastTs: approval.created_at,
    action: { kind: 'open-approval', hostId },
  })
}
