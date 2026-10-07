// spa/src/hooks/useConnectionNotifications.ts — host-store L2/L3 connection notifications (daemon refused, tmux down);
// split from useNotificationDispatcher (#1690).
import { useEffect } from 'react'
import { useI18nStore } from '../stores/useI18nStore'
import { useHostStore } from '../stores/useHostStore'
import { hostLabel, hostLookOf } from '../lib/host-look'
import type { NotificationAction } from '../lib/notification-click'

export function useConnectionNotifications(): void {
  // L2/L3 connection notifications (daemon refused, tmux down)
  useEffect(() => {
    const prevState: Record<string, { daemon?: string; tmux?: string }> = {}

    const unsubscribe = useHostStore.subscribe((state) => {
      const t = useI18nStore.getState().t

      for (const hostId of state.hostOrder) {
        const rt = state.runtime[hostId]
        const prev = prevState[hostId]
        const hostName = hostLabel(hostId, hostLookOf(hostId, state.hosts))

        // L2: daemon refused (was connected, now refused)
        if (prev?.daemon === 'connected' && rt?.daemonState === 'refused') {
          sendConnectionNotification(
            t('notification.daemon_refused', { name: hostName }),
            { kind: 'open-host', hostId },
          )
        }
        // L3: tmux down (was ok, now unavailable)
        if (prev?.tmux === 'ok' && rt?.tmuxState === 'unavailable') {
          sendConnectionNotification(
            t('notification.tmux_down', { name: hostName }),
            { kind: 'open-host', hostId },
          )
        }

        prevState[hostId] = { daemon: rt?.daemonState, tmux: rt?.tmuxState }
      }
    })
    return unsubscribe
  }, [])
}

function sendConnectionNotification(message: string, action: NotificationAction): void {
  if (window.electronAPI?.showNotification) {
    window.electronAPI.showNotification({
      title: message,
      body: '',
      sessionCode: '',
      eventName: 'ConnectionStatus',
      broadcastTs: Date.now(),
      action: action.kind === 'open-session'
        ? { kind: 'open-session', hostId: action.hostId, sessionCode: action.sessionCode }
        : { kind: action.kind, hostId: action.hostId },
    })
  }
}
