// spa/src/lib/agent-ws-dispatch.ts
// Pure dispatcher for agent.* WS events → useAgentStore actions.
// Kept out of useMultiHostEventWs.ts for testability and future extension
// (e.g. Codex statusLine may arrive via a different event.type or carry an
// agent_type discriminator inside value).
import type { HostEvent } from './host-events'
import { useAgentStore } from '../stores/useAgentStore'

export function dispatchAgentWsEvent(hostId: string, event: HostEvent): void {
  if (event.type === 'agent.status') {
    try {
      const parsed = JSON.parse(event.value) as Record<string, unknown>
      useAgentStore.getState().setCcStatus(hostId, event.session, parsed)
    } catch { /* ignore malformed payload */ }
    return
  }
  if (event.type === 'agent.status.cleared') {
    // Daemon currently always sends {"agent_type":"cc"}; the store wipes all
    // ccStatus entries for the host regardless, so we skip parsing value.
    useAgentStore.getState().clearHostAgentStatus(hostId)
    return
  }
}
