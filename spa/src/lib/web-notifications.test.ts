import { describe, it, expect, beforeEach, vi } from 'vitest'
import { shouldOfferWebNotifications, requestWebNotificationPermission, getWebNotifyDismissed, setWebNotifyDismissed } from './web-notifications'

function stubNotification(impl: Partial<{ permission: NotificationPermission; requestPermission: unknown }>) {
  Object.defineProperty(window, 'Notification', {
    configurable: true, writable: true,
    value: Object.assign(function () {}, { permission: 'default' as NotificationPermission, ...impl }),
  })
}

describe('shouldOfferWebNotifications', () => {
  const base = { isElectron: false, permission: 'default' as const, dismissed: false }
  it('web + default + 未關閉 → true', () => { expect(shouldOfferWebNotifications(base)).toBe(true) })
  it('granted → false', () => { expect(shouldOfferWebNotifications({ ...base, permission: 'granted' })).toBe(false) })
  it('denied → false', () => { expect(shouldOfferWebNotifications({ ...base, permission: 'denied' })).toBe(false) })
  it('dismissed → false', () => { expect(shouldOfferWebNotifications({ ...base, dismissed: true })).toBe(false) })
  it('Electron → false', () => { expect(shouldOfferWebNotifications({ ...base, isElectron: true })).toBe(false) })
  it('unsupported → false', () => { expect(shouldOfferWebNotifications({ ...base, permission: 'unsupported' })).toBe(false) })
})

describe('requestWebNotificationPermission', () => {
  it('Promise 版 → resolved 值', async () => {
    stubNotification({ requestPermission: vi.fn().mockResolvedValue('granted') })
    expect(await requestWebNotificationPermission()).toBe('granted')
  })
  it('callback 版 → 正規化為 permission', async () => {
    stubNotification({ requestPermission: (cb: (p: NotificationPermission) => void) => cb('granted') })
    expect(await requestWebNotificationPermission()).toBe('granted')
  })
  it('無 Notification → unsupported', async () => {
    // @ts-expect-error 移除
    delete window.Notification
    expect(await requestWebNotificationPermission()).toBe('unsupported')
  })
})

describe('dismissal 安全存取', () => {
  beforeEach(() => localStorage.clear())
  it('set → get true', () => {
    expect(getWebNotifyDismissed()).toBe(false)
    setWebNotifyDismissed()
    expect(getWebNotifyDismissed()).toBe(true)
  })
})
