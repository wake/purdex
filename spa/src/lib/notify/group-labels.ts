// spa/src/lib/notify/group-labels.ts — which group name stands for which lineage root on one host (notify title spec §3.2; plan N-3 #1). Pure: the stores are read by the caller.
//   (a) every member of a manual group whose host is `hostId` → the group's name, whether or not a tab shows it (the store is synced, so each device computes the same);
//   (b) an agent tab on `hostId` that carries `tab.group`, whose root (a) did not cover → the manual group's name, or for a team group key the team's display name (`panelName`: the team's name, else its lead's label).
//       A `tab.group` naming a group that no longer exists is no placement and writes nothing.
// A session that holds a team seat is not written: the daemon names the team itself (layer 1). A root that is not known yet is left out and listed in `wanted` so the roots asker reads it; nothing is guessed.
// The same root claimed twice: (a) beats (b), a manual group earlier in `order` beats a later one, and among tabs the lower tab id wins — so two devices agree.
import { parseGroupKey } from '../task-groups/group-key'
import { sessionOfTab } from '../task-groups/session-actions'
import { panelName } from '../team/team-names'
import type { TeamView } from '../team/team-views'
import type { ManualGroups } from '../../stores/useManualGroupsStore'
import type { Tab } from '../../types/tab'
import type { GroupLabel } from './types'

export interface ProjectionInput {
  hostId: string
  manual: ManualGroups
  tabs: Record<string, Tab>
  views: readonly TeamView[]
  /** The cached root of an agent session on a host, or null. */
  rootOf: (hostId: string, sessionId: string) => string | null
}

export interface Projection {
  /** Sorted by root, so equal projections serialise equal. */
  labels: GroupLabel[]
  /** The (b) sessions on this host: the roots asker reads those whose root is not cached. */
  wanted: { hostId: string; agentSessionId: string }[]
}

export function projectGroupLabels({ hostId, manual, tabs, views, rootOf }: ProjectionInput): Projection {
  const byRoot = new Map<string, string>()
  for (const id of manual.order) {
    const group = manual.groups[id]
    if (!group) continue
    for (const m of group.members) if (m.host === hostId && !byRoot.has(m.root)) byRoot.set(m.root, group.name)
  }
  const seated = new Set<string>()
  for (const v of views) { seated.add(v.lead.session.session_id); for (const m of v.members) seated.add(m.session.session_id) }
  const wanted: Projection['wanted'] = []
  const claimed = new Set<string>()
  for (const tabId of Object.keys(tabs).sort()) {
    const tab = tabs[tabId]
    if (!tab.group) continue
    const key = parseGroupKey(tab.group)
    let name: string | null = null
    if (key?.kind === 'manual') name = manual.groups[key.id]?.name ?? null
    else if (key?.kind === 'team') { const v = views.find((x) => x.key === key.teamKey); name = v ? panelName(v).text : null }
    if (name === null || name === '') continue
    const s = sessionOfTab(tab)
    if (s === null || s.hostId !== hostId || s.agentSessionId === null || seated.has(s.agentSessionId)) continue
    wanted.push({ hostId, agentSessionId: s.agentSessionId })
    const root = rootOf(hostId, s.agentSessionId)
    if (root === null || byRoot.has(root) || claimed.has(root)) continue
    claimed.add(root)
    byRoot.set(root, name)
  }
  const labels = [...byRoot].map(([root_session_id, group]) => ({ root_session_id, group })).sort((a, b) => (a.root_session_id < b.root_session_id ? -1 : 1))
  return { labels, wanted }
}
