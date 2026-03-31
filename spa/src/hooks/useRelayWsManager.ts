// spa/src/hooks/useRelayWsManager.ts
import { useEffect, useRef } from 'react'
import { useHostStore } from '../stores/useHostStore'
import { useStreamStore } from '../stores/useStreamStore'
import { connectStream, type StreamMessage, type ControlRequest } from '../lib/stream-ws'

/**
 * Manages stream WS connections driven by relayStatus changes.
 * Creates a WS when relay connects, destroys it when relay disconnects.
 * Derives wsBase per-host from hostStore — no params needed.
 */
export function useRelayWsManager() {
  const prevRelay = useRef<Record<string, boolean>>({})

  useEffect(() => {
    const activeConns = new Map<string, { close: () => void }>()

    const unsub = useStreamStore.subscribe(
      (s) => s.relayStatus,
      (relayStatus) => {
        for (const [ck, connected] of Object.entries(relayStatus)) {
          const wasConnected = prevRelay.current[ck] ?? false

          // Decompose composite key to hostId + sessionCode
          const colonIdx = ck.indexOf(':')
          const hostId = colonIdx >= 0 ? ck.slice(0, colonIdx) : ''
          const sessionCode = colonIdx >= 0 ? ck.slice(colonIdx + 1) : ck

          if (connected && !wasConnected) {
            // Derive wsBase for this specific host
            const wsBase = useHostStore.getState().getWsBase(hostId)
            // Relay just connected — create stream WS
            const conn = connectStream(
              `${wsBase}/ws/cli-bridge-sub/${encodeURIComponent(sessionCode)}`,
              (msg: StreamMessage) => {
                const store = useStreamStore.getState()
                if (msg.type === 'assistant' || msg.type === 'user') {
                  store.addMessage(hostId, sessionCode, msg)
                }
                if (msg.type === 'result' && 'total_cost_usd' in msg) {
                  store.addCost(hostId, sessionCode, (msg as { total_cost_usd?: number }).total_cost_usd || 0)
                  store.setStreaming(hostId, sessionCode, false)
                }
                if (msg.type === 'control_request') {
                  store.addControlRequest(hostId, sessionCode, msg as ControlRequest)
                }
                if (msg.type === 'system') {
                  const sys = msg as { subtype?: string; session_id?: string; model?: string }
                  if (sys.subtype === 'init') {
                    store.setSessionInfo(hostId, sessionCode, sys.session_id ?? '', sys.model ?? '')
                  }
                }
              },
              () => {
                // WS closed — clear conn (relay:disconnected event will handle UI state)
                useStreamStore.getState().setConn(hostId, sessionCode, null)
                activeConns.delete(ck)
              },
            )
            useStreamStore.getState().setConn(hostId, sessionCode, conn)
            activeConns.set(ck, conn)
          }

          if (!connected && wasConnected) {
            // Relay disconnected — close stream WS
            const existing = activeConns.get(ck)
            existing?.close()
            useStreamStore.getState().setConn(hostId, sessionCode, null)
            activeConns.delete(ck)
          }
        }

        // Clean up sessions removed from relayStatus (e.g., session deleted)
        for (const prevCk of Object.keys(prevRelay.current)) {
          if (!(prevCk in relayStatus)) {
            const colonIdx = prevCk.indexOf(':')
            const hostId = colonIdx >= 0 ? prevCk.slice(0, colonIdx) : ''
            const sessionCode = colonIdx >= 0 ? prevCk.slice(colonIdx + 1) : prevCk
            const existing = activeConns.get(prevCk)
            existing?.close()
            useStreamStore.getState().setConn(hostId, sessionCode, null)
            activeConns.delete(prevCk)
          }
        }

        prevRelay.current = { ...relayStatus }
      },
    )

    return () => {
      unsub()
      activeConns.forEach((conn) => conn.close())
      activeConns.clear()
    }
  }, [])
}
