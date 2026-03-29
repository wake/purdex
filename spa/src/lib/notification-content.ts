// spa/src/lib/notification-content.ts

interface NotificationContent { title: string; body: string }

export function buildNotificationContent(
  eventName: string,
  rawEvent: Record<string, unknown>,
  sessionName: string,
  t?: (key: string) => string,
): NotificationContent | null {
  switch (eventName) {
    case 'Notification':
      return { title: sessionName, body: (rawEvent.message as string) || (t?.('notification.fallback.new') ?? 'New notification') }
    case 'PermissionRequest':
      return { title: sessionName, body: (rawEvent.tool_name as string) ? `Permission required: ${rawEvent.tool_name}` : (t?.('notification.fallback.permission') ?? 'Permission required: unknown tool') }
    case 'Stop':
      return { title: sessionName, body: (rawEvent.last_assistant_message as string) || (t?.('notification.fallback.stop') ?? 'Task completed') }
    case 'StopFailure':
      return { title: sessionName, body: (rawEvent.error_details as string) || (rawEvent.error as string) || (t?.('notification.fallback.stopFailure') ?? 'Task stopped unexpectedly') }
    default:
      return null
  }
}
