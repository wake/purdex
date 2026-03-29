// spa/src/lib/notification-content.ts

interface NotificationContent { title: string; body: string }

export function buildNotificationContent(
  eventName: string,
  rawEvent: Record<string, unknown>,
  sessionName: string,
): NotificationContent | null {
  switch (eventName) {
    case 'Notification':
      return { title: sessionName, body: (rawEvent.message as string) || 'New notification' }
    case 'PermissionRequest':
      return { title: sessionName, body: `Permission required: ${(rawEvent.tool_name as string) || 'unknown tool'}` }
    case 'Stop':
      return { title: sessionName, body: (rawEvent.last_assistant_message as string) || 'Task completed' }
    default:
      return null
  }
}
