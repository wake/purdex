import { describe, it, expect } from 'vitest'
import { buildNotificationContent } from './notification-content'

describe('buildNotificationContent', () => {
  it('Notification event → uses raw_event.message', () => {
    const result = buildNotificationContent('Notification', { message: 'Claude needs your permission' }, 'my-session')
    expect(result).toEqual({ title: 'my-session', body: 'Claude needs your permission' })
  })
  it('PermissionRequest → shows tool_name', () => {
    const result = buildNotificationContent('PermissionRequest', { tool_name: 'Bash' }, 'my-session')
    expect(result).toEqual({ title: 'my-session', body: 'Permission required: Bash' })
  })
  it('Stop → uses last_assistant_message', () => {
    const result = buildNotificationContent('Stop', { last_assistant_message: 'Done.' }, 'my-session')
    expect(result).toEqual({ title: 'my-session', body: 'Done.' })
  })
  it('Stop without message → fallback', () => {
    const result = buildNotificationContent('Stop', {}, 'my-session')
    expect(result).toEqual({ title: 'my-session', body: 'Task completed' })
  })
  it('StopFailure → uses error_details', () => {
    const result = buildNotificationContent('StopFailure', { error: 'rate_limit', error_details: '429 Too Many Requests' }, 'my-session')
    expect(result).toEqual({ title: 'my-session', body: '429 Too Many Requests' })
  })
  it('StopFailure without error_details → uses error', () => {
    const result = buildNotificationContent('StopFailure', { error: 'rate_limit' }, 'my-session')
    expect(result).toEqual({ title: 'my-session', body: 'rate_limit' })
  })
  it('StopFailure without any fields → fallback', () => {
    const result = buildNotificationContent('StopFailure', {}, 'my-session')
    expect(result).toEqual({ title: 'my-session', body: 'Task stopped unexpectedly' })
  })
  it('unknown event → null', () => {
    expect(buildNotificationContent('SessionStart', {}, 'x')).toBeNull()
  })
  it('Stop without message → uses t() when provided', () => {
    const t = (key: string) => key === 'notification.fallback.stop' ? 'Aufgabe abgeschlossen' : key
    const result = buildNotificationContent('Stop', {}, 'my-session', t)
    expect(result).toEqual({ title: 'my-session', body: 'Aufgabe abgeschlossen' })
  })
  it('Notification without message → uses t() fallback', () => {
    const t = (key: string) => key === 'notification.fallback.new' ? '新通知' : key
    const result = buildNotificationContent('Notification', {}, 'my-session', t)
    expect(result).toEqual({ title: 'my-session', body: '新通知' })
  })
  it('collapses consecutive newlines in body', () => {
    const result = buildNotificationContent('Stop', { last_assistant_message: 'Line 1\n\n\nLine 2\n\nLine 3' }, 'my-session')
    expect(result).toEqual({ title: 'my-session', body: 'Line 1\nLine 2\nLine 3' })
  })
})
