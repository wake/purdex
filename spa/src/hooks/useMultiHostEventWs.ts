// spa/src/hooks/useMultiHostEventWs.ts — Multi-host event WS + connection state machine
import { useEffect } from 'react'
import { useHostStore, type HostRuntime } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useStreamStore } from '../stores/useStreamStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useTabStore } from '../stores/useTabStore'
import { connectHostEvents, type EventConnection } from '../lib/host-events'
import { scanPaneTree } from '../lib/pane-tree'
import { hostWsUrl, fetchWsTicket } from '../lib/host-api'
import { fetchHistory } from '../lib/api'
import { checkHealth, type HealthResult } from '../lib/host-connection'
import { ConnectionStateMachine } from '../lib/connection-state-machine'
import type { Session } from '../lib/api'

export function useMultiHostEventWs() {
  const hostOrderKey = useHostStore((s) => s.hostOrder.join(','))

  useEffect(() => {
    const { hosts, hostOrder } = useHostStore.getState()
    const connections = new Map<string, EventConnection>()
    const stateMachines = new Map<string, ConnectionStateMachine>()

    for (const hostId of hostOrder) {
      if (!hosts[hostId]) continue
      const wsUrl = hostWsUrl(hostId, '/ws/host-events')
      const baseUrl = useHostStore.getState().getDaemonBase(hostId)

      // --- Connection state machine (per host) ---
      const connRef: { current: EventConnection | undefined } = { current: undefined }

      const statusMap: Record<HealthResult['daemon'], HostRuntime['status']> = {
        connected: 'connected',
        unreachable: 'disconnected',
        refused: 'disconnected',
        'auth-error': 'auth-error',
      }

      const sm = new ConnectionStateMachine(
        () => checkHealth(baseUrl, () => useHostStore.getState().hosts[hostId]?.token),
        (result) => {
          useHostStore.getState().setRuntime(hostId, {
            status: statusMap[result.daemon],
            latency: result.latency ?? undefined,
            daemonState: result.daemon,
          })
          // On recovery → reconnect WS with pre-fetched ticket
          if (result.daemon === 'connected' && connRef.current) {
            if (result.ticket) {
              connRef.current.reconnectWithTicket(result.ticket)
            } else {
              connRef.current.reconnect()
            }
          }
        },
      )
      stateMachines.set(hostId, sm)
      useHostStore.getState().setRuntime(hostId, { manualRetry: () => sm.trigger() })

      // --- WS connection (per host) ---
      const conn = connectHostEvents(
        wsUrl,
        (event) => {
          if (event.type === 'sessions') {
            try {
              const data: Session[] = JSON.parse(event.value)
              useSessionStore.getState().replaceHost(hostId, data)
              for (const s of data) {
                useTabStore.getState().updateSessionCache(hostId, s.code, s.name)
              }

              // session-closed detection: collect unique closed codes, then mark once each
              const newCodes = new Set(data.map((s: Session) => s.code))
              const closedCodes = new Set<string>()
              const { tabs } = useTabStore.getState()
              for (const tab of Object.values(tabs)) {
                scanPaneTree(tab.layout, (pane) => {
                  const c = pane.content
                  if (c.kind === 'tmux-session' && c.hostId === hostId && !c.terminated && !newCodes.has(c.sessionCode)) {
                    closedCodes.add(c.sessionCode)
                  }
                })
              }
              for (const code of closedCodes) {
                useTabStore.getState().markTerminated(hostId, code, 'session-closed')
              }
            } catch { /* ignore */ }
            return
          }
          if (event.type === 'hook') {
            try {
              const hookData = JSON.parse(event.value)
              useAgentStore.getState().handleHookEvent(hostId, event.session, hookData)
            } catch { /* ignore */ }
          }
          if (event.type === 'relay') {
            useStreamStore.getState().setRelayStatus(hostId, event.session, event.value === 'connected')
          }
          if (event.type === 'tmux') {
            useHostStore.getState().setRuntime(hostId, {
              tmuxState: event.value === 'ok' ? 'ok' : 'unavailable',
            })
          }
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
        // onClose — trigger SM health check (no auto-reconnect)
        () => {
          useHostStore.getState().setRuntime(hostId, { status: 'reconnecting' })
          sm.trigger()
        },
        // onOpen
        () => {
          useHostStore.getState().setRuntime(hostId, {
            status: 'connected',
            daemonState: 'connected',
          })
          useAgentStore.getState().clearSubagentsForHost(hostId)
          const daemonBase = useHostStore.getState().getDaemonBase(hostId)
          useSessionStore.getState().fetchHost(hostId, daemonBase).catch(() => {})
        },
        () => fetchWsTicket(hostId),
        false, // autoReconnect disabled — SM manages reconnection
        true,  // lazy — waits for SM to trigger first connection
      )
      connRef.current = conn
      connections.set(hostId, conn)

      // Start negotiation — SM will trigger reconnectWithTicket on success
      sm.trigger()
    }

    return () => {
      connections.forEach((c) => c.close())
      stateMachines.forEach((sm) => sm.stop())
    }
  }, [hostOrderKey])
}
