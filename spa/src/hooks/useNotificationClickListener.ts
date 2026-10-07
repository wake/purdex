// spa/src/hooks/useNotificationClickListener.ts — routes Electron's notification clicks to `handleNotificationClick`;
// split from useNotificationDispatcher (#1690).
import { useEffect } from 'react'
import { handleNotificationClick } from '../lib/notification-click'

export function useNotificationClickListener(): void {
  // Electron notification click listener
  useEffect(() => {
    if (!window.electronAPI?.onNotificationClicked) return
    return window.electronAPI.onNotificationClicked((payload) => {
      // Electron main always forwards `action` (carrying hostId). A payload
      // without it has no host to route to, so it is ignored rather than
      // guessed — session codes are not unique across hosts.
      if (!payload.action) return
      if (payload.action.kind === 'open-host') {
        handleNotificationClick({ kind: 'open-host', hostId: payload.action.hostId })
      } else if (payload.action.kind === 'open-approval') {
        const { requestId } = payload.action
        handleNotificationClick({ kind: 'open-approval', hostId: payload.action.hostId, ...(typeof requestId === 'string' ? { requestId } : {}) })
      } else {
        handleNotificationClick({
          kind: 'open-session',
          hostId: payload.action.hostId,
          sessionCode: payload.action.sessionCode ?? payload.sessionCode,
        })
      }
    })
  }, [])
}
