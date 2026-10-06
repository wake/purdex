// spa/src/lib/nex/open-conversation-rebuild.ts — 重建… on an 已退出 row (conversation entity spec §13.4, R-4-3).
//
// The rebuild tab of an ended conversation is a closed terminal pane: `tmux-session` with
// `terminated: 'conversation-ended'`, which `TerminatedPane` renders as §7's screen (mode choice,
// `RebuildActionSet`, session picker). It never had a tmux session of its own, so its `sessionCode` is `''`;
// what it knows is a rebuild record for S — the transcript's cwd, `agent {type:'cc', sessionId:S}`, a session
// name generated the way take-to-terminal generates one, and the host's current tmux generation — plus the
// `conversation` the screen reads its title, preselection (R-4-5) and recently-written notice (R-4-4) from.
// From then on it is an ordinary closed terminal pane: the host reconciler skips it (it is terminated), and
// Rebuild all takes it when it carries a generation (`batch.ts` `groupForBatch`) — as a group of its own, since an
// empty session code is no binding to share with another pane.
//
// One tab per (host, S): `openSingletonTab` never matches `tmux-session` content and looks only at primary
// panes, so the pane is looked for here, in every leaf of every tab.
import { useTabStore } from '../../stores/useTabStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { focusTabInWorkspace } from '../../features/workspace/lib/open-worker-tab'
import { collectLeaves } from '../pane-tree'
import { nextProjectSessionName } from '../launch-session-name'
import { slugForCwd } from './session-slug'
import { hostHomeFor } from './handoff'
import type { ConversationRow } from './conversations-api'
import type { TmuxSessionContent } from '../../types/tab'

/**
 * The host's current tmux generation: the `tmux_instance` stamped on the sessions payload the SPA last received
 * for it — the same field the session picker hands a pane as its `tmuxInstance`. `''` (unknown) when there is no
 * payload, no stamp, or stamps that disagree: no evidence, no generation.
 */
export function hostTmuxGeneration(hostId: string): string {
  const stamps = new Set<string>()
  for (const s of useSessionStore.getState().sessions[hostId] ?? []) {
    if (typeof s.tmux_instance === 'string' && s.tmux_instance !== '') stamps.add(s.tmux_instance)
  }
  return stamps.size === 1 ? [...stamps][0] : ''
}

/** The tab whose layout holds — in any leaf — the conversation-ended pane for (hostId, S); S is case-insensitive. */
export function findConversationRebuildTab(hostId: string, sessionId: string): string | null {
  const want = sessionId.toLowerCase()
  for (const [tabId, tab] of Object.entries(useTabStore.getState().tabs)) {
    for (const leaf of collectLeaves(tab.layout)) {
      const c = leaf.content
      if (c.kind === 'tmux-session' && c.hostId === hostId && c.terminated === 'conversation-ended'
        && c.conversation?.sessionId.toLowerCase() === want) return tabId
    }
  }
  return null
}

/** The pane content for a conversation's rebuild tab (R-4-3). Values from the row as it is now. */
export function conversationRebuildContent(
  hostId: string,
  row: ConversationRow,
  sessionName: string,
  tmuxInstance: string,
  capturedAt: number = Date.now(),
): TmuxSessionContent {
  const sessionId = row.session_id.toLowerCase()
  return {
    kind: 'tmux-session',
    hostId,
    sessionCode: '',
    mode: 'terminal',
    cachedName: sessionName,
    tmuxInstance,
    terminated: 'conversation-ended',
    rebuild: {
      sessionName,
      tmuxInstance,
      cwd: row.cwd,
      cwdSource: 'user',
      agent: { type: 'cc', sessionId, updatedAt: row.last_activity_at },
      // No `agentExited`: an exited agent is not resumed by default (the panel's resume row starts unticked,
      // `planForRecord` skips it), and this pane exists to resume S. Absent reads "running when last seen".
      capturedAt,
    },
    conversation: { sessionId, title: row.title, lastIn: row.last_in, lastWriteAt: row.last_activity_at },
  }
}

/**
 * Opens (or selects, when one exists for this host + session id) a tab whose pane is the closed-terminal rebuild
 * screen for the conversation (R-4-3), and brings it on screen in its workspace.
 */
export async function openConversationRebuild(hostId: string, row: ConversationRow): Promise<string> {
  const existing = findConversationRebuildTab(hostId, row.session_id)
  if (existing) {
    focusTabInWorkspace(existing)
    return existing
  }
  // The name, as take-to-terminal names its session (handoff.ts `takeToTerminal`): the host config carries the
  // projects the slug is looked up from, and a `~/…` project needs the host's home to compare against the cwd.
  await useHostConfigStore.getState().ensureLoaded(hostId)
  const projects = useHostConfigStore.getState().byHost[hostId]?.projects ?? []
  const home = await hostHomeFor(hostId, projects)
  // Looked for again after the waits: a second click that arrived meanwhile selects the tab the first one made.
  const opened = findConversationRebuildTab(hostId, row.session_id)
  if (opened) {
    focusTabInWorkspace(opened)
    return opened
  }
  const liveNames = (useSessionStore.getState().sessions[hostId] ?? []).map((s) => s.name)
  const name = nextProjectSessionName(slugForCwd(row.cwd ?? '', projects, home), liveNames)
  // `tmux-session` content is never a singleton match, so this always makes a new tab.
  const tabId = useTabStore.getState().openSingletonTab(conversationRebuildContent(hostId, row, name, hostTmuxGeneration(hostId)))
  focusTabInWorkspace(tabId)
  return tabId
}
