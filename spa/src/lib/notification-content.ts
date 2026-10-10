// spa/src/lib/notification-content.ts
import { normalizeEventName } from './event-name'
import { NOTIFICATION_BODY_RUNES, NOTIFICATION_TITLE_RUNES, normaliseNotificationText } from './notification-normalise'

interface NotificationContent { title: string; body: string }

/** The text of an event as a lock-screen line (the phone push's rule, #2144), or the fallback when nothing is left of it. */
function lockScreenText(raw: unknown, fallback: string): string {
  return typeof raw === 'string' ? (normaliseNotificationText(raw, NOTIFICATION_BODY_RUNES) || fallback) : fallback
}

export function buildNotificationContent(
  eventName: string,
  rawEvent: Record<string, unknown>,
  sessionName: string,
  t?: (key: string, params?: Record<string, string | number>) => string,
): NotificationContent | null {
  let content: NotificationContent | null
  // W2 transition: cc broadcasts PdxXxx; the legacy switch arms live below.
  // Normalize once at entry so callers (dispatcher / settings UI / tests) can
  // pass either form.
  switch (normalizeEventName(eventName)) {
    case 'Notification': {
      const nt = rawEvent.notification_type as string | undefined
      let fallback: string
      if (nt === 'permission_prompt') {
        fallback = t?.('notification.permission_prompt') ?? 'Permission approval required'
      } else if (nt === 'elicitation_dialog') {
        fallback = t?.('notification.elicitation_dialog') ?? 'Input required (MCP)'
      } else {
        fallback = t?.('notification.fallback.new') ?? 'New notification'
      }
      content = { title: sessionName, body: lockScreenText(rawEvent.message, fallback) }
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
      content = { title: sessionName, body: lockScreenText(rawEvent.last_assistant_message, t?.('notification.fallback.stop') ?? 'Task completed') }
      break
    case 'StopFailure':
      content = { title: sessionName, body: lockScreenText(rawEvent.error_details, lockScreenText(rawEvent.error, t?.('notification.fallback.stopFailure') ?? 'Task stopped unexpectedly')) }
      break
    case 'WorkerTerminated':
      content = { title: sessionName, body: t?.('notification.worker_terminated') ?? 'Worker terminated' }
      break
    default:
      return null
  }
  // The same rule for every body (a tool name inside a template included) and for the title: a name written by a session
  // cannot carry direction marks or run past the lock screen either.
  content.body = normaliseNotificationText(content.body, NOTIFICATION_BODY_RUNES)
  content.title = normaliseNotificationText(content.title, NOTIFICATION_TITLE_RUNES) || content.title
  return content
}
