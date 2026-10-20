// spa/src/lib/task-groups/personal.ts — what the personal panel is about (task groups spec §7, TG-5b): the active tab's agent session when it is not a team seat, and the group that session is in, for the capsule.
// Pure: callers hand in the tab, the placement inputs and the team views.
import type { Tab } from '../../types/tab'
import { collectLeaves } from '../pane-tree'
import { isWatchTabId } from '../watch/ids'
import type { TeamView } from '../team/team-views'
import { teamColor } from '../../components/team/team-display'
import { groupOfTab, type PlacementCtx } from './placement'
import { parseGroupKey } from './group-key'

/** The session a personal panel stands for: the first pane (pre-order) that recorded an agent session id, live or ended (spec §3 "a tab's agent session"). */
export interface PersonalTarget {
  tabId: string
  hostId: string
  /** The tmux session code of that pane: the agent light and the context reading are keyed by (hostId, sessionCode). */
  sessionCode: string
  agentSessionId: string
  /** The pane's agent type (`cc`, `codex`, …), when it recorded one. */
  agentType: string | undefined
  /** The pane's session ended (the agent exited); the panel stays. */
  ended: boolean
}

export function personalTargetOf(tab: Pick<Tab, 'id' | 'layout'>): PersonalTarget | null {
  if (isWatchTabId(tab.id)) return null
  for (const pane of collectLeaves(tab.layout)) {
    const c = pane.content
    if (c.kind !== 'tmux-session') continue
    const agent = c.rebuild?.agent
    if (agent?.sessionId) {
      return { tabId: tab.id, hostId: c.hostId, sessionCode: c.sessionCode, agentSessionId: agent.sessionId, agentType: agent.type, ended: c.terminated !== undefined }
    }
  }
  return null
}

/** The neutral grey of 「未分組」 and of a group with no colour (TaskGroupView.tsx's COLOR_DEFAULT). Not a swatch colour. */
export const UNGROUPED_GREY = '#6b7280'

export type PersonalGroup =
  | { kind: 'ungrouped' }
  | { kind: 'manual'; id: string; name: string; color: string | undefined; count: number }
  | { kind: 'team'; teamKey: string; name: string; color: string }

/**
 * The group the personal panel's capsule names. A session whose root is not known yet is 「未分組」 (R23: nothing is guessed), which is also what `groupOfTab` answers then.
 * `count`: the tabs of the group that show a session (R12's 「· N 個 session」), the active one included.
 */
export function personalGroupOf(
  tab: Pick<Tab, 'id' | 'layout' | 'group'>,
  ctx: PlacementCtx,
  views: readonly Pick<TeamView, 'key' | 'name' | 'label' | 'colorIndex'>[],
  allTabs: Readonly<Record<string, Pick<Tab, 'id' | 'layout' | 'group'>>>,
): PersonalGroup {
  const key = groupOfTab(tab, ctx)
  const parsed = key === null ? null : parseGroupKey(key)
  if (parsed?.kind === 'manual') {
    const group = ctx.manual.groups[parsed.id]
    if (group === undefined) return { kind: 'ungrouped' }
    const count = Object.values(allTabs).filter((t) => personalTargetOf(t) !== null && groupOfTab(t, ctx) === key).length
    return { kind: 'manual', id: parsed.id, name: group.name, color: group.color, count }
  }
  if (parsed?.kind === 'team') {
    const view = views.find((v) => v.key === parsed.teamKey)
    if (view !== undefined) return { kind: 'team', teamKey: parsed.teamKey, name: view.name !== '' ? view.name : view.label, color: teamColor(view.colorIndex) }
  }
  return { kind: 'ungrouped' }
}
