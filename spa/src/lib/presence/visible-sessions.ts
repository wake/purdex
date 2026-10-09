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

// The daemon refuses a whole report over one bad field (push spec §5.4): at most 200 sessions, a code of 1-64 and a name
// of at most 64 printable characters, a body under 16 KiB. A tmux session name can be anything, so what is sent is
// made to fit rather than left to be refused for good.
const MAX_SESSIONS = 200
const MAX_RUNES = 64
const MAX_BYTES = 12 * 1024 // of the sessions' JSON, leaving the rest of the 16 KiB to the envelope

const clean = (s: string): string => Array.from(s.replace(/\p{C}/gu, '')).slice(0, MAX_RUNES).join('')

/** The sessions as the daemon accepts them: control characters dropped, codes and names cut to 64 characters, a code
 *  that is empty skipped, and no more than 200 entries / 12 KiB (the first ones in layout order stay). */
export function boundSessions(list: readonly PresenceSession[]): PresenceSession[] {
  const out: PresenceSession[] = []
  let bytes = 2
  for (const s of list) {
    const code = clean(s.code)
    if (code === '') continue
    const item = { code, name: clean(s.name) }
    const size = new TextEncoder().encode(JSON.stringify(item)).length + 1
    if (out.length >= MAX_SESSIONS || bytes + size > MAX_BYTES) break
    out.push(item)
    bytes += size
  }
  return out
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
  for (const hostId of Object.keys(out)) out[hostId] = boundSessions(out[hostId])
  return out
}
