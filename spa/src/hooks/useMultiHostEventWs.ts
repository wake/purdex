// spa/src/hooks/useMultiHostEventWs.ts — Multi-host event WS + connection state machine
import { useEffect, useRef } from 'react'
import { useHostStore, type HostRuntime } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useRebuildStore, type OperationLockGrant } from '../stores/useRebuildStore'
import { connectHostEvents, type EventConnection } from '../lib/host-events'
import { dispatchAgentWsEvent, isAgentWsEvent } from '../lib/agent-ws'
import { dispatchBackupWsEvent } from '../lib/storage-backup/backup-ws-dispatch'
import { dispatchProfileWsEvent } from '../lib/profile/profile-ws-dispatch'
import { debugStatuslineTest } from '../lib/statusline-test-debug'
import { closeAttachGate } from '../lib/rebuild/attach-gate'
import { provenanceBindings } from '../lib/rebuild/reconcile-host'
import { connectionClosed, connectionOpened, forgetHost } from '../lib/rebuild/session-version'
import { cancelSessionRefresh, currentLockGen, operationLockAcquired, reconcileAfterLockRelease } from '../lib/rebuild/refresh-sessions'
import { endSessionsBarrier, handleSessionsFrame } from '../lib/rebuild/ws-sessions'
import { probeSessionProvenance } from '../lib/rebuild/provenance-probe'
import { hostWsUrl, fetchWsTicket } from '../lib/host-api'
import { checkHealth, type HealthResult } from '../lib/host-connection'
import { ConnectionStateMachine } from '../lib/connection-state-machine'

/**
 * The operation lock's observer (#1309 + #1310 spec §3.1): every tree rewriter —
 * a switch, a profile apply, a rebuild, a batch — writes under the lock and
 * releases it after its write, so a RELEASE is when the panes on screen are
 * reconciled from a list read after that write (`reconcileAfterLockRelease`,
 * which also runs the revive pass a rebuild in flight held back, and then ends
 * each host's barrier); an ACQUIRE moves the lock generation that fences a
 * release's refresh, and puts the versioned, live hosts in barrier — no WS
 * verdict on the panes the holder is about to write (ws-sessions.ts).
 *
 * Transitions are read off the store as it is NOW, by grant identity, not off
 * the listener's `(state, prev)` pair: a listener that takes the lock inside a
 * release notification makes zustand notify the acquire (nested) BEFORE the
 * remaining listeners hear of the release, so the pair can describe a past
 * that is already gone. Seen that way, a holder can also change without a free
 * moment in between (X → Y); that is a release of X followed by an acquire of Y,
 * in that order — the release's refresh then meets Y's lock and ends, and Y's
 * own release starts the next one.
 *
 * Exported so integration tests wire the very subscription the hook does.
 */
export function createOperationLockObserver(): () => void {
  let seen: OperationLockGrant | null = useRebuildStore.getState().lockGrant
  return () => {
    const now = useRebuildStore.getState().lockGrant
    if (now === seen) return
    const was = seen
    seen = now
    if (was !== null) {
      const gen = currentLockGen()
      void reconcileAfterLockRelease((hostId) => endSessionsBarrier(hostId, gen))
    }
    if (now !== null) operationLockAcquired()
  }
}

interface HostEntry {
  conn: EventConnection
  sm: ConnectionStateMachine
  configKey: string // "ip:port"
}

export function useMultiHostEventWs() {
  const hostConfigKey = useHostStore((s) =>
    s.hostOrder.map((id) => {
      const h = s.hosts[id]
      return h ? `${id}:${h.ip}:${h.port}` : id
    }).join(',')
  )

  const entriesRef = useRef(new Map<string, HostEntry>())

  useEffect(() => {
    const { hosts, hostOrder } = useHostStore.getState()
    const entries = entriesRef.current
    const currentIds = new Set(hostOrder)

    // 1. Remove hosts no longer in hostOrder
    for (const [hostId, entry] of entries) {
      if (!currentIds.has(hostId)) {
        entry.conn.close()
        entry.sm.stop()
        forgetHost(hostId)
        cancelSessionRefresh(hostId)
        entries.delete(hostId)
      }
    }

    // 2. For each host, check if configKey changed; skip if same
    for (const hostId of hostOrder) {
      const host = hosts[hostId]
      if (!host) continue

      const configKey = `${host.ip}:${host.port}`
      const existing = entries.get(hostId)

      if (existing && existing.configKey === configKey) {
        continue // no change — keep existing connection
      }

      // Teardown old entry if config changed
      if (existing) {
        existing.conn.close()
        existing.sm.stop()
        forgetHost(hostId) // another endpoint may be another daemon: nothing held carries over
        cancelSessionRefresh(hostId)
      }

      // Create new SM + WS for this host
      const wsUrl = hostWsUrl(hostId, '/ws/host-events')
      const baseUrl = useHostStore.getState().getDaemonBase(hostId)

      const connRef: { current: EventConnection | undefined } = { current: undefined }

      const statusMap: Record<HealthResult['daemon'], HostRuntime['status']> = {
        connected: 'connected',
        unreachable: 'disconnected',
        refused: 'disconnected',
        'auth-error': 'auth-error',
      }

      const sm = new ConnectionStateMachine(
        () => checkHealth(baseUrl, () => useHostStore.getState().hosts[hostId]?.token ?? undefined),
        (result) => {
          useHostStore.getState().setRuntime(hostId, {
            status: statusMap[result.daemon],
            latency: result.latency ?? undefined,
            daemonState: result.daemon,
          })
          // On recovery → reconnect WS with pre-fetched ticket
          if (result.daemon === 'connected' && connRef.current) {
            // A new connection starts here; nothing it will say has arrived
            // yet, so the pane's binding is unverified again (spec §4.6).
            // The old socket is retired without an `onClose`: move `conn`
            // first, then close the gate (#1255 SPA spec §3.3).
            connectionClosed(hostId)
            closeAttachGate(hostId)
            if (result.ticket) {
              connRef.current.reconnectWithTicket(result.ticket)
            } else {
              connRef.current.reconnect()
            }
          }
        },
      )
      useHostStore.getState().setRuntime(hostId, { manualRetry: () => sm.trigger() })

      // --- WS connection (per host) ---
      const conn = connectHostEvents(
        wsUrl,
        (event) => {
          if (event.type === 'sessions') {
            // Ordered by the list's version when the daemon sends one (#1255).
            handleSessionsFrame(hostId, event)
            return
          }
          if (event.type === 'hook') {
            try {
              const hookData = JSON.parse(event.value)
              useAgentStore.getState().handleNormalizedEvent(hostId, event.session, hookData)
              // The second provenance trigger (spec §5.4), and the one v2
              // lacked: the first probe of a pre-deploy session runs before any
              // event has filled the frame's `session_id`, gets `found: false`,
              // and nothing else would ever ask again — the session list has
              // not changed and the pane is not re-attached. The hook stream is
              // exactly the signal that the daemon now knows more than it did.
              //
              // AFTER `handleNormalizedEvent`, not before: a broadcast that
              // itself writes the record leaves the pane ineligible, so it
              // costs no request.
              for (const { sessionCode, tmuxInstance } of provenanceBindings(hostId, event.session)) {
                probeSessionProvenance(hostId, sessionCode, tmuxInstance)
              }
            } catch { /* ignore */ }
          }
          if (event.type === 'tmux') {
            useHostStore.getState().setRuntime(hostId, {
              tmuxState: event.value === 'ok' ? 'ok' : 'unavailable',
            })
          }
          if (event.type === 'backup:done') {
            // Cross-device backup refresh (spec §4.6). The dispatch helper
            // ignores this device's own posts (already reflected by backupNow).
            dispatchBackupWsEvent(hostId, event)
            return
          }
          if (event.type === 'profile') {
            // Profile section change (spec §4.6). Own writes are NOT filtered
            // here or in the helper — the sync driver decides what "own" means.
            dispatchProfileWsEvent(hostId, event)
            return
          }
          // `handoff` / `relay` events: the daemon stopped emitting them in
          // P-D.2 and the SPA has no Stream state since P-D.3. One from an
          // older peer daemon falls through here untouched.
          // Whitelist sourced from agent-ws/index.ts — single source so adding
          // a new agent.* handler only requires updating AGENT_WS_EVENT_TYPES
          // (R2-F2). No broad startsWith filter (defender review #9).
          if (isAgentWsEvent(event.type)) {
            if (event.session.startsWith('__pdx_test_')) {
              debugStatuslineTest('ws.entry', {
                hostId,
                type: event.type,
                session: event.session,
                valueLen: event.value.length,
              })
            }
            dispatchAgentWsEvent(hostId, event)
            return
          }
        },
        // onClose — trigger SM health check (no auto-reconnect)
        () => {
          // `conn` moves BEFORE the gate closes, in this same synchronous
          // callback (#1255 SPA spec §3.3, codex #5): whoever sees the gate
          // closed also sees a connection generation no fetch was sent on.
          connectionClosed(hostId)
          // Immediately, not on the next open: from here on nothing confirms
          // the panes' bindings (spec §4.6).
          useHostStore.getState().setRuntime(hostId, { status: 'reconnecting', attachReady: false })
          sm.trigger()
        },
        // onOpen
        () => {
          connectionOpened(hostId)
          useHostStore.getState().setRuntime(hostId, {
            status: 'connected',
            daemonState: 'connected',
          })
          useSessionStore.getState().fetchHost(hostId).catch(() => {})
        },
        () => fetchWsTicket(hostId),
        false, // autoReconnect disabled — SM manages reconnection
        true,  // lazy — waits for SM to trigger first connection
      )
      connRef.current = conn

      entries.set(hostId, { conn, sm, configKey })

      // A brand-new connection: the gate starts closed and only this
      // connection's own `sessions` payload may open it.
      closeAttachGate(hostId)

      // Start negotiation — SM will trigger reconnectWithTicket on success
      sm.trigger()
    }
    // No cleanup here — diff logic handles teardown of changed/removed hosts.
    // Unmount cleanup is handled by the separate effect below.
  }, [hostConfigKey])

  // Unmount-only cleanup: close all entries when the component unmounts
  useEffect(() => {
    const entries = entriesRef.current
    return () => {
      entries.forEach((entry, hostId) => {
        entry.conn.close()
        entry.sm.stop()
        forgetHost(hostId)
        cancelSessionRefresh(hostId)
      })
      entries.clear()
    }
  }, [])

  // The operation lock's acquire / release (see `createOperationLockObserver`).
  // The release is also the second revive trigger: a rebuild in flight holds
  // the lock and the pass skips everything under it, so the release is what
  // turns "skipped" into "revived" — from a list read after the rebuild.
  useEffect(() => useRebuildStore.subscribe(createOperationLockObserver()), [])
}
