// spa/src/components/team/groupSegments.ts — splits the normal (unpinned) zone of the TabBar into plain tabs and team runs
// (spec §4.2, plan TI-2). A run starts at a team's LEAD tab and takes the member tabs of the same team right behind it
// (the tab lifecycle keeps them contiguous, in team order). A member with no lead before it — its lead is in another
// workspace, or pinned — stays an ordinary tab. A collapsed team keeps the lead and counts the members it hides.
import type { TeamTabMark } from './team-display'

export type Segment<T extends { id: string }> =
  | { kind: 'tab'; tab: T }
  | { kind: 'team'; mark: TeamTabMark; /** The lead first, then the visible members. */ tabs: T[]; /** Members the collapse hides. */ hidden: number }

export function groupSegments<T extends { id: string }>(
  tabs: readonly T[],
  tabMark: (tabId: string) => TeamTabMark | null,
  collapsed: Record<string, boolean>,
): Segment<T>[] {
  const out: Segment<T>[] = []
  for (let i = 0; i < tabs.length; i++) {
    const mark = tabMark(tabs[i].id)
    if (!mark || mark.role !== 'lead') {
      out.push({ kind: 'tab', tab: tabs[i] })
      continue
    }
    const run: T[] = [tabs[i]]
    while (i + 1 < tabs.length) {
      const next = tabMark(tabs[i + 1].id)
      if (!next || next.role !== 'member' || next.teamKey !== mark.teamKey) break
      run.push(tabs[++i])
    }
    const isCollapsed = collapsed[mark.teamKey] === true
    out.push({ kind: 'team', mark, tabs: isCollapsed ? run.slice(0, 1) : run, hidden: isCollapsed ? run.length - 1 : 0 })
  }
  return out
}
