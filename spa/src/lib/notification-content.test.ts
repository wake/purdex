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
  it('PermissionRequest with tool_name uses t() for i18n (#105)', () => {
    const t = (key: string, params?: Record<string, string | number>) =>
      key === 'notification.permission_request' ? `需要授權：${params?.tool}` : key
    const result = buildNotificationContent('PermissionRequest', { tool_name: 'Bash' }, 'my-session', t)
    expect(result).toEqual({ title: 'my-session', body: '需要授權：Bash' })
  })

  it('Notification(permission_prompt) shows specific body (#110)', () => {
    const t = (key: string) =>
      key === 'notification.permission_prompt' ? '需要授權核准' : key
    const result = buildNotificationContent('Notification', { notification_type: 'permission_prompt' }, 'my-session', t)
    expect(result).toEqual({ title: 'my-session', body: '需要授權核准' })
  })

  it('Notification(elicitation_dialog) shows specific body (#110)', () => {
    const t = (key: string) =>
      key === 'notification.elicitation_dialog' ? '需要輸入資訊（MCP）' : key
    const result = buildNotificationContent('Notification', { notification_type: 'elicitation_dialog' }, 'my-session', t)
    expect(result).toEqual({ title: 'my-session', body: '需要輸入資訊（MCP）' })
  })

  it('Notification(permission_prompt) with message prefers message', () => {
    const result = buildNotificationContent('Notification', { notification_type: 'permission_prompt', message: 'Claude wants to run Bash' }, 'my-session')
    expect(result).toEqual({ title: 'my-session', body: 'Claude wants to run Bash' })
  })

  // #2144: the body is the phone push's lock-screen line (lib/notification-normalise.ts), not the raw Markdown
  it('puts a multi-line body on one line', () => {
    const result = buildNotificationContent('Stop', { last_assistant_message: 'Line 1\n\n\nLine 2\n\nLine 3' }, 'my-session')
    expect(result).toEqual({ title: 'my-session', body: 'Line 1 Line 2 Line 3' })
  })

  it('drops Markdown syntax from a Stop body', () => {
    const msg = '## Result\n\n- **fast**: `go test` passes\n- see [the docs](https://example.com)\n\n> note'
    expect(buildNotificationContent('Stop', { last_assistant_message: msg }, 's')?.body).toBe('Result fast: go test passes see the docs note')
  })

  it('cuts a long body at 240 runes with an ellipsis, like the push', () => {
    const body = buildNotificationContent('Stop', { last_assistant_message: 'x'.repeat(1000) }, 's')!.body
    expect(Array.from(body)).toHaveLength(241)
    expect(body.endsWith('…')).toBe(true)
  })

  it('a message that is only Markdown falls back to the default text', () => {
    expect(buildNotificationContent('Stop', { last_assistant_message: '```\n```\n**' }, 's')?.body).toBe('Task completed')
    expect(buildNotificationContent('Notification', { message: '> ' }, 's')?.body).toBe('New notification')
  })

  it('a Notification message and a StopFailure error are cleaned the same way', () => {
    expect(buildNotificationContent('Notification', { message: '**Claude** needs\nyour `OK`' }, 's')?.body).toBe('Claude needs your OK')
    expect(buildNotificationContent('StopFailure', { error_details: '**rate** limit\n(429)' }, 's')?.body).toBe('rate limit (429)')
    expect(buildNotificationContent('StopFailure', { error_details: '', error: 'boom' }, 's')?.body).toBe('boom')
    expect(buildNotificationContent('StopFailure', { error_details: '```', error: '' }, 's')?.body).toBe('Task stopped unexpectedly')
  })

  it('the title is cleaned and cut like the push title (direction marks dropped, 120 runes)', () => {
    expect(buildNotificationContent('Stop', { last_assistant_message: 'ok' }, 'a‮b​c')?.title).toBe('abc')
    const title = buildNotificationContent('Stop', { last_assistant_message: 'ok' }, 't'.repeat(300))!.title
    expect(Array.from(title)).toHaveLength(121)
  })

  // The phone push cuts a title and nothing else (internal/push/agent_content.go): a name is not Markdown.
  it.each(['my__session__x', '1. first session', '> quoted name', '`tick` **bold** name', 'snake_case'])('the title %j keeps its name', (name) => {
    expect(buildNotificationContent('Stop', { last_assistant_message: 'ok' }, name)?.title).toBe(name)
  })

  it('a title made only of format characters is empty, never the raw string with its direction marks', () => {
    expect(buildNotificationContent('Stop', { last_assistant_message: 'ok' }, '‮‮')?.title).toBe('')
  })

  it('the body is cleaned once, like the push: what removing the markers exposes is not cleaned again', () => {
    expect(buildNotificationContent('Stop', { last_assistant_message: '`- x' }, 's')?.body).toBe('- x')
    expect(buildNotificationContent('Notification', { message: '**- x' }, 's')?.body).toBe('- x')
  })

  it('a tool name keeps its underscores (an MCP tool is mcp__server__tool) and loses control / format characters', () => {
    expect(buildNotificationContent('PermissionRequest', { tool_name: 'mcp__srv__tool' }, 's')?.body).toBe('Permission required: mcp__srv__tool')
    expect(buildNotificationContent('PermissionRequest', { tool_name: 'Ba‮sh' }, 's')?.body).toBe('Permission required: Bash')
    expect(buildNotificationContent('PermissionRequest', { tool_name: '‮' }, 's')?.body).toBe('Permission required: unknown tool')
  })

  // W2 transition: cc broadcasts PdxXxx; buildNotificationContent normalizes
  // at entry so PdxXxx and legacy literals produce identical output.
  it('PdxNotification → identical to Notification (W2)', () => {
    const result = buildNotificationContent('PdxNotification', { message: 'Claude needs your permission' }, 'my-session')
    expect(result).toEqual({ title: 'my-session', body: 'Claude needs your permission' })
  })
  it('PdxPermissionRequest → identical to PermissionRequest (W2)', () => {
    const result = buildNotificationContent('PdxPermissionRequest', { tool_name: 'Bash' }, 'my-session')
    expect(result).toEqual({ title: 'my-session', body: 'Permission required: Bash' })
  })
  it('PdxStop → identical to Stop (W2)', () => {
    const result = buildNotificationContent('PdxStop', { last_assistant_message: 'Done.' }, 'my-session')
    expect(result).toEqual({ title: 'my-session', body: 'Done.' })
  })
  it('PdxStopFailure → identical to StopFailure (W2)', () => {
    const result = buildNotificationContent('PdxStopFailure', { error_details: '429 Too Many Requests' }, 'my-session')
    expect(result).toEqual({ title: 'my-session', body: '429 Too Many Requests' })
  })
})
