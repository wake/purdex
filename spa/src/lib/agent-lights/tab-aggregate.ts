// spa/src/lib/agent-lights/tab-aggregate.ts — a tab's agent light over ALL its panes (spec N5, U1-3 ruling 6).
//
// The tab light is the highest-priority status of every agent pane of the layout (error > waiting > running > idle);
// unread and "awaiting approval" (the hand) are OR over panes; the subagent dots come from the representative pane
// (the highest-priority one; a tie goes to the primary pane, then the leaf order); the corner symbol is the highest
// background across panes. The tab's icon and title stay the primary pane's (identity, not light) — callers read
// those from the primary pane themselves.
import type { PaneLayout } from '../../types/tab'
import type { AgentStatus, BackgroundKind, NormalizedEvent } from '../../stores/useAgentStore'
import { backgroundOf } from '../../stores/useAgentStore'
import { collectLeaves, getPrimaryPane, paneAgentKey } from '../pane-tree'
import { compositeKey } from '../composite-key'
import { statusRank } from './status-rank'

export interface TabAgentPane {
  /** compositeKey(hostId, sessionCode) — the agent store's key. */
  key: string
  hostId: string
  /** Set for an execution (worker) pane. */
  executionId?: string
}

/**
 * Every pane of the layout that shows an agent (a live tmux pane, or an execution pane), primary pane first, then the
 * leaf order, de-duplicated by key (two panes on one session code count once).
 */
export function tabAgentPanes(layout: PaneLayout): TabAgentPane[] {
  const out: TabAgentPane[] = []
  const seen = new Set<string>()
  for (const pane of [getPrimaryPane(layout), ...collectLeaves(layout)]) {
    const a = paneAgentKey(pane.content)
    if (!a) continue
    const key = compositeKey(a.hostId, a.sessionCode)
    if (seen.has(key)) continue
    seen.add(key)
    out.push(pane.content.kind === 'execution'
      ? { key, hostId: a.hostId, executionId: pane.content.executionId }
      : { key, hostId: a.hostId })
  }
  return out
}

export interface TabAgentData {
  statuses: Record<string, AgentStatus>
  unread: Record<string, boolean>
  agentTypes: Record<string, string>
  lastEvents: Record<string, NormalizedEvent>
}

export interface TabAgentAggregate {
  status: AgentStatus | undefined
  isUnread: boolean
  isAwaitingApproval: boolean
  /** The pane the dots (and the agent type) come from; undefined when the tab shows no agent pane. */
  repKey: string | undefined
  background: BackgroundKind | undefined
}

const BACKGROUND_RANK: Record<BackgroundKind, number> = { workflow: 3, monitor: 2, schedule: 1 }

const NONE: TabAgentAggregate = {
  status: undefined, isUnread: false, isAwaitingApproval: false, repKey: undefined, background: undefined,
}

/**
 * The aggregate of `panes` (from `tabAgentPanes`). `awaiting` holds the keys of execution panes waiting on a
 * permission request (the hand): such a pane counts as `waiting` at least, whatever its stored status says.
 */
export function aggregateTabAgents(
  panes: TabAgentPane[], data: TabAgentData, awaiting: ReadonlySet<string> = new Set(),
): TabAgentAggregate {
  if (panes.length === 0) return NONE
  let status: AgentStatus | undefined
  let rank = 0
  let repKey: string | undefined
  let isUnread = false
  let background: BackgroundKind | undefined
  for (const { key } of panes) {
    let s = data.statuses[key]
    if (awaiting.has(key) && statusRank(s) < statusRank('waiting')) s = 'waiting'
    const r = statusRank(s)
    if (r > rank) { rank = r; status = s; repKey = key }
    if (data.unread[key]) isUnread = true
    const b = backgroundOf(data.lastEvents[key])
    if (b && (!background || BACKGROUND_RANK[b] > BACKGROUND_RANK[background])) background = b
  }
  if (repKey === undefined) {
    // no pane has a status: the first pane that has an agent type represents the tab, else the primary pane
    repKey = panes.find((p) => data.agentTypes[p.key])?.key ?? panes[0].key
  }
  return { status, isUnread, isAwaitingApproval: awaiting.size > 0, repKey, background }
}
