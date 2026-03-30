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
  t?: (key: string) => string,
): NotificationContent | null {
  let content: NotificationContent | null
  switch (eventName) {
    case 'Notification':
      content = { title: sessionName, body: (rawEvent.message as string) || (t?.('notification.fallback.new') ?? 'New notification') }
      break
    case 'PermissionRequest':
      content = { title: sessionName, body: (rawEvent.tool_name as string) ? `Permission required: ${rawEvent.tool_name}` : (t?.('notification.fallback.permission') ?? 'Permission required: unknown tool') }
      break
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
