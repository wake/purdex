import { useEffect } from 'react'
import { useStreamStore } from '../stores/useStreamStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'
import { connectSessionEvents } from '../lib/session-events'
import { fetchHistory } from '../lib/api'

/**
 * Legacy single-host session event WS hook.
 * Will be replaced by useMultiHostEventWs in multi-host phase.
 */
export function useSessionEventWs(hostId: string, wsBase: string, daemonBase: string) {
  const fetchHost = useSessionStore((s) => s.fetchHost)

  useEffect(() => {
    const conn = connectSessionEvents(
      `${wsBase}/ws/session-events`,
      (event) => {
        if (event.type === 'hook') {
          try {
            const hookData = JSON.parse(event.value)
            useAgentStore.getState().handleHookEvent(hostId, event.session, hookData)
          } catch { /* ignore parse errors */ }
        }
        if (event.type === 'relay') {
          useStreamStore.getState().setRelayStatus(hostId, event.session, event.value === 'connected')
        }
        if (event.type === 'handoff') {
          const store = useStreamStore.getState()
          if (event.value === 'connected') {
            store.setHandoffProgress(hostId, event.session, '')
            fetchHost(hostId, daemonBase).then(() => {
              const hostSessions = useSessionStore.getState().sessions[hostId] ?? []
              const sess = hostSessions.find((s) => s.code === event.session)
              if (sess && sess.mode !== 'term') {
                fetchHistory(daemonBase, sess.code).then((msgs) => {
                  useStreamStore.getState().loadHistory(hostId, event.session, msgs)
                }).catch((e) => { console.warn('history fetch failed:', e) })
              } else {
                useStreamStore.getState().clearSession(hostId, event.session)
              }
            }).catch((e) => { console.warn('fetchHost failed:', e) })
          } else if (event.value.startsWith('failed')) {
            store.setHandoffProgress(hostId, event.session, '')
            fetchHost(hostId, daemonBase)
          } else {
            store.setHandoffProgress(hostId, event.session, event.value)
          }
        }
      },
      undefined, // onClose
      // onOpen (initial + reconnect): clear ephemeral subagent tracking —
      // DB snapshot won't contain SubagentStart/Stop events.
      () => { useAgentStore.getState().clearAllSubagents() },
    )
    return () => conn.close()
  }, [hostId, fetchHost, daemonBase, wsBase])
}
