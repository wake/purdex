// spa/src/components/team/useSeatHostMain.ts — the colour the one-line cell paints the ring's model symbol in.
import { useMemo } from 'react'
import { useHostLook } from '../../lib/host-look'
import { useAgentStore } from '../../stores/useAgentStore'
import { compositeKey } from '../../lib/composite-key'
import { resolveHostColors } from '../../lib/host-color'

/**
 * The main colour of a seat's host (what the host icon lights up in), as CSS, or undefined: a host this Mac lacks
 * (hostId '') or a host with no colour. Resolved like KnownHostBadge does — the mode follows whether the seat is an agent.
 */
export function useSeatHostMain(hostId: string, sessionCode: string): string | undefined {
  const look = useHostLook(hostId === '' ? null : hostId)
  const agentType = useAgentStore((s) => s.agentTypes[compositeKey(hostId, sessionCode)])
  return useMemo(() => (hostId === '' ? undefined : resolveHostColors({ colors: look.colors, color: look.color }, agentType ? 'terminal' : 'console')?.main), [hostId, look, agentType])
}
