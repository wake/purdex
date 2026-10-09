// spa/src/lib/presence/visible-sessions.ts — the tmux sessions this window shows, per host (push spec §5.4, plan PU-4
// Task 2): every pane of the ACTIVE tab (a split shows all its leaves at once, as `isAgentVisibleInActiveTab` counts
// them), each with its session code and its tmux session name. Workers (exec-*) are not tmux sessions and are out of
// push v1, so they are left out.
import { getActiveTabAgents } from '../active-session'
import { executionIdOfAgentCode } from '../nex/worker-agent-status'
import { useSessionStore } from '../../stores/useSessionStore'

export interface PresenceSession {
  code: string
  name: string
}

/** `{}` when no tab is active or the active tab shows no tmux session. A host with no visible session is absent. */
export function visibleSessionsByHost(): Record<string, PresenceSession[]> {
  const known = useSessionStore.getState().sessions
  const out: Record<string, PresenceSession[]> = {}
  for (const { hostId, sessionCode } of getActiveTabAgents()) {
    if (executionIdOfAgentCode(sessionCode) !== null) continue
    const name = known[hostId]?.find((s) => s.code === sessionCode)?.name ?? ''
    ;(out[hostId] ??= []).push({ code: sessionCode, name })
  }
  return out
}
