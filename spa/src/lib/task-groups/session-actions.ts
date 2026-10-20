// spa/src/lib/task-groups/session-actions.ts — what the sidebar does with a SESSION (a tab that shows one): file it in a manual group, start a group from it, take it out (task groups spec §5 user writes, TG-2b). The
// group edits themselves are `manual-actions.ts`, which is given a root; this is the caller that gets it: `resolveRoot` answers from the root cache, and when the cache lacks it asks that session's host. `no-root`
// is what comes back only when the host cannot answer either (or the pane recorded no agent session id) — never a session's own id in its place: it would file the wrong thing the moment the session is relayed.
import { resolveRoot } from './root-cache'
import {
  createManualGroup, putMemberInGroup, takeOutOfGroups, type CreateResult, type ManualActionResult,
} from './manual-actions'

/** The session a row stands for: its host, the agent session id its pane recorded (null = none) and the tab the row is. */
export interface SessionRef {
  hostId: string
  agentSessionId: string | null
  tabId: string
}

const rootOf = (s: SessionRef): Promise<string | null> => (s.agentSessionId === null ? Promise.resolve(null) : resolveRoot(s.hostId, s.agentSessionId))

/** Puts the session in the group (out of whichever held it). */
export async function fileSession(s: SessionRef, groupId: string): Promise<ManualActionResult> {
  return putMemberInGroup({ host: s.hostId, root: await rootOf(s) }, groupId, s.tabId)
}

/** Starts a group named `name` with the session in it. */
export async function startGroupWithSession(s: SessionRef, name: string): Promise<CreateResult> {
  return createManualGroup({ name, member: { host: s.hostId, root: await rootOf(s) }, tabId: s.tabId })
}

/** 「移出」: the session goes out of its group. Without its root the member cannot be named, so nothing is touched. */
export async function takeSessionOut(s: SessionRef): Promise<ManualActionResult> {
  const root = await rootOf(s)
  if (root === null) return { ok: false, reason: 'no-root' }
  return takeOutOfGroups({ host: s.hostId, root, tabId: s.tabId })
}
