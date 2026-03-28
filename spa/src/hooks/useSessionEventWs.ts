import { useEffect } from 'react'
import { useStreamStore } from '../stores/useStreamStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'
import { connectSessionEvents } from '../lib/session-events'
import { fetchHistory } from '../lib/api'

export function useSessionEventWs(wsBase: string, daemonBase: string) {
  const fetchSessions = useSessionStore((s) => s.fetch)

  useEffect(() => {
    const conn = connectSessionEvents(
      `${wsBase}/ws/session-events`,
      (event) => {
        if (event.type === 'hook') {
          try {
            const hookData = JSON.parse(event.value)
            useAgentStore.getState().handleHookEvent(event.session, hookData)
          } catch { /* ignore parse errors */ }
        }
        if (event.type === 'relay') {
          useStreamStore.getState().setRelayStatus(event.session, event.value === 'connected')
        }
        if (event.type === 'handoff') {
          const store = useStreamStore.getState()
          if (event.value === 'connected') {
            store.setHandoffProgress(event.session, '')
            fetchSessions(daemonBase).then(() => {
              const sess = useSessionStore.getState().sessions.find((s) => s.code === event.session)
              if (sess && sess.mode !== 'term') {
                fetchHistory(daemonBase, sess.code).then((msgs) => {
                  useStreamStore.getState().loadHistory(event.session, msgs)
                }).catch((e) => { console.warn('history fetch failed:', e) })
              } else {
                useStreamStore.getState().clearSession(event.session)
              }
            }).catch((e) => { console.warn('fetchSessions failed:', e) })
          } else if (event.value.startsWith('failed')) {
            store.setHandoffProgress(event.session, '')
            fetchSessions(daemonBase)
          } else {
            store.setHandoffProgress(event.session, event.value)
          }
        }
      },
    )
    return () => conn.close()
  }, [fetchSessions, daemonBase, wsBase])
}
