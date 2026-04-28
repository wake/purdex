// spa/src/lib/agent-ws/status-dispatch.ts
// agent.status / agent.status.cleared dispatcher → useAgentStore actions.
// Split out of the WS hook for testability; the agent-ws router (./index.ts)
// is the single entry point that dispatches to this and other agent.* handlers.
import type { HostEvent } from '../host-events'
import { useAgentStore } from '../../stores/useAgentStore'
import { statuslineTestBus } from '../statusline-test-bus'
import { debugStatuslineTest } from '../statusline-test-debug'

const TEST_NONCE_PREFIX = '__pdx_test_'

export function handleStatusEvent(hostId: string, event: HostEvent): void {
  if (event.type === 'agent.status') {
    const isTestNonce = event.session.startsWith(TEST_NONCE_PREFIX)
    if (isTestNonce) {
      debugStatuslineTest('dispatch.entry', {
        hostId,
        session: event.session,
        valueLen: event.value.length,
      })
    }
    try {
      // Daemon broadcasts {"agent_type":"cc","status":<raw CC statusLine JSON>}
      // (see internal/module/agent/handler.go statusSnapshot). We must unwrap
      // and pass the inner `status` to the store — otherwise `raw.session_name`
      // (read by setCcStatus) lives one level too deep and oscTitles never
      // populate in real environments.
      const parsed = JSON.parse(event.value)
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
        if (isTestNonce) debugStatuslineTest('dispatch.reject.parsed-not-object', { parsed })
        return
      }
      const wire = parsed as Record<string, unknown>
      if (isTestNonce) {
        debugStatuslineTest('dispatch.parsed', {
          agent_type: wire.agent_type,
          statusKeys: wire.status && typeof wire.status === 'object'
            ? Object.keys(wire.status as Record<string, unknown>)
            : null,
        })
      }
      // SPA only supports cc today; future Codex payloads should route elsewhere.
      if (wire.agent_type !== 'cc') {
        if (isTestNonce) debugStatuslineTest('dispatch.reject.wrong-agent-type', { agent_type: wire.agent_type })
        return
      }
      const status = wire.status
      if (!status || typeof status !== 'object' || Array.isArray(status)) {
        if (isTestNonce) debugStatuslineTest('dispatch.reject.status-not-object', { status })
        return
      }
      const rawStatus = status as Record<string, unknown>
      useAgentStore.getState().setCcStatus(hostId, event.session, rawStatus)
      if (isTestNonce) {
        debugStatuslineTest('dispatch.emit-bus', { nonce: event.session })
        statuslineTestBus.emit({ nonce: event.session, hostId, raw: rawStatus })
      }
    } catch (err) {
      if (isTestNonce) debugStatuslineTest('dispatch.reject.exception', { err: String(err) })
    }
    return
  }
  if (event.type === 'agent.status.cleared') {
    // Scoped clear (targeted session) vs global clear (empty session) — the
    // daemon emits scoped events for the self-test nonce cleanup and emits
    // unscoped events when a real statusLine is uninstalled.
    if (event.session) {
      useAgentStore.getState().clearSession(hostId, event.session)
    } else {
      useAgentStore.getState().clearHostAgentStatus(hostId)
    }
    return
  }
}
