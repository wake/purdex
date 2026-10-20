// spa/src/lib/task-groups/session-actions.ts — what the sidebar does with a SESSION (a tab that shows one): file it in a manual group, start a group from it, take it out (task groups spec §5 user writes, TG-2b). The
// group edits themselves are `manual-actions.ts`, which is given a root; this is the caller that gets it: `resolveRoot` answers from the root cache, and when the cache lacks it asks that session's host. `no-root`
// is what comes back only when the host cannot answer either (or the pane recorded no agent session id) — never a session's own id in its place: it would file the wrong thing the moment the session is relayed.
// Getting the root can take a daemon read, and the tab may be closed or its pane rebound meanwhile: before anything is written the tab is read again, and if it no longer stands for the session the root was got for,
// the action is refused (`tab-changed`) and neither the group store nor the tab is touched. The same goes for the host: a root got (from the cache or the daemon) for a host that was re-pointed meanwhile is the old
// daemon's, and is never written.
import { collectLeaves } from '../pane-tree'
import type { Tab } from '../../types/tab'
import { hostIdentityNow } from '../team/quota-host'
import { useTabStore } from '../../stores/useTabStore'
import { resolveRoot } from './root-cache'
import { personalTargetOf } from './personal'
import {
  createManualGroup, putMemberInGroup, takeOutOfGroups, type ManualActionResult,
} from './manual-actions'

/** The session a row stands for: its host, the agent session id its pane recorded (null = none) and the tab the row is. */
export interface SessionRef {
  hostId: string
  agentSessionId: string | null
  tabId: string
}

export type SessionActionError = Extract<ManualActionResult, { ok: false }>['reason'] | 'tab-changed'
export type SessionActionResult = { ok: true } | { ok: false; reason: SessionActionError }
export type SessionCreateResult = { ok: true; id: string } | { ok: false; reason: SessionActionError }

/** The session a tab stands for (spec §3): its agent session — the first pane in tree order that recorded an agent session id, live or ended — else the first live tmux session (which recorded none). The one rule the sidebar's rows, the personal panel and `stillThere` share. */
export function sessionOfTab(tab: Pick<Tab, 'id' | 'layout'>): { hostId: string; sessionCode: string; cachedName: string; agentSessionId: string | null } | null {
  const agent = personalTargetOf(tab)
  if (agent !== null) {
    const pane = collectLeaves(tab.layout).map((p) => p.content).find((x) => x.kind === 'tmux-session' && x.hostId === agent.hostId && x.sessionCode === agent.sessionCode)
    return { hostId: agent.hostId, sessionCode: agent.sessionCode, cachedName: pane?.kind === 'tmux-session' ? pane.cachedName : '', agentSessionId: agent.agentSessionId }
  }
  const c = collectLeaves(tab.layout).map((p) => p.content).find((x) => x.kind === 'tmux-session' && !x.terminated)
  return c && c.kind === 'tmux-session' ? { hostId: c.hostId, sessionCode: c.sessionCode, cachedName: c.cachedName, agentSessionId: null } : null
}

/** The session the tab stands for now, or null when the tab is gone or shows none. A session that ended still has its tab, so its group can still be changed. */
function currentSessionOf(tabId: string): { hostId: string; agentSessionId: string | null } | null {
  const tab = useTabStore.getState().tabs[tabId]
  const s = tab ? sessionOfTab(tab) : null
  return s === null ? null : { hostId: s.hostId, agentSessionId: s.agentSessionId }
}

const stillThere = (s: SessionRef): boolean => {
  const now = currentSessionOf(s.tabId)
  return now !== null && now.hostId === s.hostId && now.agentSessionId === s.agentSessionId
}

/** The root of the session, and whether it can still be written: the tab still stands for the session and the host was not re-pointed while it was got. */
async function rootFor(s: SessionRef): Promise<{ root: string | null; current: boolean }> {
  const identity = hostIdentityNow(s.hostId)
  const root = s.agentSessionId === null ? null : await resolveRoot(s.hostId, s.agentSessionId)
  return { root, current: identity === hostIdentityNow(s.hostId) && stillThere(s) }
}

/** Puts the session in the group (out of whichever held it). */
export async function fileSession(s: SessionRef, groupId: string): Promise<SessionActionResult> {
  const { root, current } = await rootFor(s)
  if (!current) return { ok: false, reason: 'tab-changed' }
  return putMemberInGroup({ host: s.hostId, root }, groupId, s.tabId)
}

/** Starts a group named `name` with the session in it. A function is asked for the name at the moment the group is created — after the root is known — so a name chosen as "first unused" cannot have been taken meanwhile. */
export async function startGroupWithSession(s: SessionRef, name: string | (() => string)): Promise<SessionCreateResult> {
  const { root, current } = await rootFor(s)
  if (!current) return { ok: false, reason: 'tab-changed' }
  return createManualGroup({ name: typeof name === 'function' ? name() : name, member: { host: s.hostId, root }, tabId: s.tabId })
}

/** 「移出」: the session goes out of its group. Without its root the member cannot be named, so nothing is touched. */
export async function takeSessionOut(s: SessionRef): Promise<SessionActionResult> {
  const { root, current } = await rootFor(s)
  if (!current) return { ok: false, reason: 'tab-changed' }
  if (root === null) return { ok: false, reason: 'no-root' }
  return takeOutOfGroups({ host: s.hostId, root, tabId: s.tabId })
}
