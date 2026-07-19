import { STORAGE_KEYS } from './storage/keys'

export function webNotificationPermission(): 'unsupported' | NotificationPermission {
  if (typeof window === 'undefined' || !('Notification' in window)) return 'unsupported'
  return Notification.permission
}

// Supports both the modern Promise-returning API and the legacy callback API.
// A callback is always passed so the legacy form's result is captured directly
// (rather than assuming the callback synchronously mutates Notification.permission,
// which real legacy implementations don't guarantee before the callback runs).
export async function requestWebNotificationPermission(): Promise<'unsupported' | NotificationPermission> {
  if (typeof window === 'undefined' || !('Notification' in window)) return 'unsupported'
  try {
    return await new Promise<NotificationPermission>((resolve, reject) => {
      try {
        const result = Notification.requestPermission((p) => resolve(p)) as unknown
        if (result && typeof (result as Promise<NotificationPermission>).then === 'function') {
          ;(result as Promise<NotificationPermission>).then(resolve, reject)
        }
      } catch (err) {
        reject(err)
      }
    })
  } catch {
    return Notification.permission
  }
}

export function getWebNotifyDismissed(): boolean {
  if (typeof window === 'undefined') return false
  try { return localStorage.getItem(STORAGE_KEYS.WEB_NOTIFICATION_DISMISSED) === '1' } catch { return false }
}

export function setWebNotifyDismissed(): void {
  if (typeof window === 'undefined') return
  try { localStorage.setItem(STORAGE_KEYS.WEB_NOTIFICATION_DISMISSED, '1') } catch { /* ignore */ }
}

export function shouldOfferWebNotifications(opts: {
  isElectron: boolean
  permission: 'unsupported' | NotificationPermission
  dismissed: boolean
}): boolean {
  if (opts.isElectron) return false
  if (opts.dismissed) return false
  return opts.permission === 'default'
}
