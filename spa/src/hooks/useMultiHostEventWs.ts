// spa/src/hooks/useMultiHostEventWs.ts — Multi-host event WebSocket
import { useEffect } from 'react'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useStreamStore } from '../stores/useStreamStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useTabStore } from '../stores/useTabStore'
import { connectHostEvents } from '../lib/host-events'
import { hostWsUrl, fetchWsTicket } from '../lib/host-api'
import { fetchHistory } from '../lib/api'
import type { Session } from '../lib/api'

export function useMultiHostEventWs() {
  // Stable dep: only re-run when the list of host IDs changes (add/remove host)
  const hostOrderKey = useHostStore((s) => s.hostOrder.join(','))

  useEffect(() => {
    const { hosts, hostOrder } = useHostStore.getState()
    const connections = new Map<string, { close: () => void }>()

    for (const hostId of hostOrder) {
      if (!hosts[hostId]) continue
      const wsUrl = hostWsUrl(hostId, '/ws/host-events')

      const conn = connectHostEvents(
        wsUrl,
        (event) => {
          // Handle 'sessions' event (from session watcher)
          if (event.type === 'sessions') {
            try {
              const data: Session[] = JSON.parse(event.value)
              useSessionStore.getState().replaceHost(hostId, data)
              // Sync cachedName for existing tabs
              for (const s of data) {
                useTabStore.getState().updateSessionCache(hostId, s.code, s.name)
              }
            } catch { /* ignore */ }
            return
          }
          // Handle 'hook' event
          if (event.type === 'hook') {
            try {
              const hookData = JSON.parse(event.value)
              useAgentStore.getState().handleHookEvent(hostId, event.session, hookData)
            } catch { /* ignore */ }
          }
          // Handle 'relay' event
          if (event.type === 'relay') {
            useStreamStore.getState().setRelayStatus(hostId, event.session, event.value === 'connected')
          }
          // Handle 'tmux' event
          if (event.type === 'tmux') {
            useHostStore.getState().setRuntime(hostId, {
              tmuxState: event.value === 'ok' ? 'ok' : 'unavailable',
            })
          }
          // Handle 'handoff' event
          if (event.type === 'handoff') {
            const store = useStreamStore.getState()
            const daemonBase = useHostStore.getState().getDaemonBase(hostId)
            if (event.value === 'connected') {
              store.setHandoffProgress(hostId, event.session, '')
              useSessionStore.getState().fetchHost(hostId, daemonBase).then(() => {
                const sess = (useSessionStore.getState().sessions[hostId] ?? [])
                  .find((s) => s.code === event.session)
                if (sess && sess.mode !== 'terminal') {
                  fetchHistory(daemonBase, sess.code).then((msgs) => {
                    useStreamStore.getState().loadHistory(hostId, event.session, msgs)
                  }).catch(() => {})
                } else {
                  useStreamStore.getState().clearSession(hostId, event.session)
                }
              }).catch(() => {})
            } else if (event.value.startsWith('failed')) {
              store.setHandoffProgress(hostId, event.session, '')
              useSessionStore.getState().fetchHost(hostId, daemonBase).catch(() => {})
            } else {
              store.setHandoffProgress(hostId, event.session, event.value)
            }
          }
        },
        // onClose
        () => { useHostStore.getState().setRuntime(hostId, { status: 'reconnecting' }) },
        // onOpen
        () => {
          useHostStore.getState().setRuntime(hostId, { status: 'connected' })
          useAgentStore.getState().clearSubagentsForHost(hostId)
          const daemonBase = useHostStore.getState().getDaemonBase(hostId)
          useSessionStore.getState().fetchHost(hostId, daemonBase).catch(() => {})
        },
        // getTicket — fetch one-time WS ticket for this host
        () => fetchWsTicket(hostId),
      )
      connections.set(hostId, conn)
    }
    return () => { connections.forEach((c) => c.close()) }
  }, [hostOrderKey])
}
