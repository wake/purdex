// spa/src/lib/notification-content.ts

interface NotificationContent { title: string; body: string }

/** Collapse consecutive newlines into a single newline. */
function collapseNewlines(s: string): string {
  return s.replace(/\n{2,}/g, '\n')
}

export function buildNotificationContent(
  eventName: string,
  rawEvent: Record<string, unknown>,
  sessionName: string,
  t?: (key: string, params?: Record<string, string | number>) => string,
): NotificationContent | null {
  let content: NotificationContent | null
  switch (eventName) {
    case 'Notification': {
      const nt = rawEvent.notification_type as string | undefined
      let body: string
      if (rawEvent.message) {
        body = rawEvent.message as string
      } else if (nt === 'permission_prompt') {
        body = t?.('notification.permission_prompt') ?? 'Permission approval required'
      } else if (nt === 'elicitation_dialog') {
        body = t?.('notification.elicitation_dialog') ?? 'Input required (MCP)'
      } else {
        body = t?.('notification.fallback.new') ?? 'New notification'
      }
      content = { title: sessionName, body }
      break
    }
    case 'PermissionRequest': {
      const toolName = rawEvent.tool_name as string | undefined
      const body = toolName
        ? (t?.('notification.permission_request', { tool: toolName }) ?? `Permission required: ${toolName}`)
        : (t?.('notification.fallback.permission') ?? 'Permission required: unknown tool')
      content = { title: sessionName, body }
      break
    }
    case 'Stop':
      content = { title: sessionName, body: (rawEvent.last_assistant_message as string) || (t?.('notification.fallback.stop') ?? 'Task completed') }
      break
    case 'StopFailure':
      content = { title: sessionName, body: (rawEvent.error_details as string) || (rawEvent.error as string) || (t?.('notification.fallback.stopFailure') ?? 'Task stopped unexpectedly') }
      break
    default:
      return null
  }
  content.body = collapseNewlines(content.body)
  return content
}
