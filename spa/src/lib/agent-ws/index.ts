// spa/src/lib/agent-ws/index.ts
// Router for agent.* WS events. New agent.* event types must be added
// explicitly here (defender review #9 — no broad startsWith filter).
import type { HostEvent } from '../host-events'
import { handleStatusEvent } from './status-dispatch'
import { handlePathHintEvent } from './path-hint-dispatch'

export function dispatchAgentWsEvent(hostId: string, event: HostEvent): void {
  if (event.type === 'agent.status' || event.type === 'agent.status.cleared') {
    handleStatusEvent(hostId, event)
    return
  }
  if (event.type === 'agent.path_hint') {
    handlePathHintEvent(hostId, event)
    return
  }
}

export { handleStatusEvent, handlePathHintEvent }
