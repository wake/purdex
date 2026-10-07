// spa/src/hooks/useNotificationDispatcher.ts — the desktop-notification effects, mounted once by App in their original
// order, and the re-exports its importers use; each part lives in its own module (#1690).
import { useAgentNotifications } from './useAgentNotifications'
import { useNotificationClickListener } from './useNotificationClickListener'
import { useConnectionNotifications } from './useConnectionNotifications'

export { buildDebounceKey, __resetDebounceStateForTests, __purgeDebounceForHostForTests, shouldNotify } from '../lib/notification-gate'
export { shouldDispatch, shouldDispatchRequest, clearSeenTs } from '../lib/notification-dedup'
export { handleNotificationClick } from '../lib/notification-click'
export type { NotificationAction } from '../lib/notification-click'

export function useNotificationDispatcher(): void {
  useAgentNotifications()
  useNotificationClickListener()
  useConnectionNotifications()
}
