// spa/src/lib/agent-ws-dispatch.ts
// Pure dispatcher for agent.* WS events → useAgentStore actions.
// Kept out of useMultiHostEventWs.ts for testability and future extension
// (e.g. Codex statusLine may arrive via a different event.type or carry an
// agent_type discriminator inside value).
import type { HostEvent } from './host-events'
import { useAgentStore } from '../stores/useAgentStore'
import { statuslineTestBus } from './statusline-test-bus'

const TEST_NONCE_PREFIX = '__pdx_test_'

export function dispatchAgentWsEvent(hostId: string, event: HostEvent): void {
  if (event.type === 'agent.status') {
    try {
      // Daemon broadcasts {"agent_type":"cc","status":<raw CC statusLine JSON>}
      // (see internal/module/agent/handler.go statusSnapshot). We must unwrap
      // and pass the inner `status` to the store — otherwise `raw.session_name`
      // (read by setCcStatus) lives one level too deep and oscTitles never
      // populate in real environments.
      const parsed = JSON.parse(event.value)
      if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return
      const wire = parsed as Record<string, unknown>
      // SPA only supports cc today; future Codex payloads should route elsewhere.
      if (wire.agent_type !== 'cc') return
      const status = wire.status
      if (!status || typeof status !== 'object' || Array.isArray(status)) return
      const rawStatus = status as Record<string, unknown>
      useAgentStore.getState().setCcStatus(hostId, event.session, rawStatus)
      if (event.session.startsWith(TEST_NONCE_PREFIX)) {
        statuslineTestBus.emit({ nonce: event.session, hostId, raw: rawStatus })
      }
    } catch { /* ignore malformed payload */ }
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
