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
})
