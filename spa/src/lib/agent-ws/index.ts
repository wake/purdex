// spa/src/lib/agent-ws/index.ts
// Router for agent.* WS events. New agent.* event types must be added to
// AGENT_WS_EVENT_TYPES below — used by both this router AND the WS hook
// (useMultiHostEventWs) so a single source decides which events qualify
// for dispatch (R2-F2).
import type { HostEvent } from '../host-events'
import { handleStatusEvent } from './status-dispatch'
import { handlePathHintEvent } from './path-hint-dispatch'

export const AGENT_WS_EVENT_TYPES = [
  'agent.status',
  'agent.status.cleared',
  'agent.path_hint',
] as const satisfies readonly HostEvent['type'][]

type AgentWsEventType = (typeof AGENT_WS_EVENT_TYPES)[number]

export function isAgentWsEvent(type: HostEvent['type']): type is AgentWsEventType {
  return (AGENT_WS_EVENT_TYPES as readonly string[]).includes(type)
}

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
