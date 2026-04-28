// W2 transition: cc broadcasts `PdxXxx` raw_event_name; SPA notification
// surfaces (shouldNotify informational suppression, NotificationSettings.events
// lookup, buildNotificationContent switch) still key on the legacy literals.
// normalizeEventName collapses the 4 user-facing notification events back to
// their legacy form so callees stay agnostic to the transition.
const PURDEX_TO_LEGACY: Record<string, string> = {
  PdxNotification: 'Notification',
  PdxPermissionRequest: 'PermissionRequest',
  PdxStop: 'Stop',
  PdxStopFailure: 'StopFailure',
}

export function normalizeEventName(raw: string): string {
  return PURDEX_TO_LEGACY[raw] ?? raw
}
