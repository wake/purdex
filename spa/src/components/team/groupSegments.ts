// spa/src/components/team/groupSegments.ts — splits the normal (unpinned) zone of the TabBar into plain tabs and team runs
// (spec §4.2, plan TI-2). The run definition is `teamRuns` (lib/team/team-runs.ts), shared with stepping / ⌘N so what is
// drawn is what can be reached: a lead plus the member tabs of its team right behind it. A member outside such a run stays
// an ordinary tab. A collapsed team keeps the lead and counts the members it hides.
import { teamRuns } from '../../lib/team/team-runs'
import type { TeamTabMark } from './team-display'

export type Segment<T extends { id: string }> =
  | { kind: 'tab'; tab: T }
  | { kind: 'team'; mark: TeamTabMark; /** The lead first, then the visible members. */ tabs: T[]; /** Members the collapse hides. */ hidden: number }

export function groupSegments<T extends { id: string }>(
  tabs: readonly T[],
  tabMark: (tabId: string) => TeamTabMark | null,
  collapsed: Record<string, boolean>,
): Segment<T>[] {
  const runs = teamRuns(tabs.map((t) => t.id), (id) => {
    const m = tabMark(id)
    return m ? { key: m.teamKey, role: m.role } : null
  })
  const out: Segment<T>[] = []
  let at = 0
  for (const run of runs) {
    for (; at < run.start; at++) out.push({ kind: 'tab', tab: tabs[at] })
    const all = tabs.slice(run.start, run.start + 1 + run.members)
    const isCollapsed = collapsed[run.teamKey] === true
    out.push({ kind: 'team', mark: tabMark(all[0].id)!, tabs: isCollapsed ? all.slice(0, 1) : all, hidden: isCollapsed ? all.length - 1 : 0 })
    at = run.start + all.length
  }
  for (; at < tabs.length; at++) out.push({ kind: 'tab', tab: tabs[at] })
  return out
}
