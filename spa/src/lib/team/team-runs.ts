// spa/src/lib/team/team-runs.ts — the ONE definition of a team run in a workspace's tab list (spec §4.2): a lead tab plus the
// member tabs of the same team right behind it. A member that is not in such a run (its lead is in another workspace,
// pinned, or not right before it) is an ordinary tab. The tab bar draws these runs (`groupSegments`) and stepping /
// ⌘1–8 skip the hidden members (`visibleTabIds`); both read this, so what is drawn is what can be reached.
export interface TabTeamHit {
  key: string
  role: 'lead' | 'member'
}

export interface TeamRun {
  teamKey: string
  /** Index in the list of the lead tab; the members follow at the next indices. */
  start: number
  /** Number of member tabs behind the lead. */
  members: number
}

export function teamRuns(tabIds: readonly string[], teamOfTab: (tabId: string) => TabTeamHit | null | undefined): TeamRun[] {
  const runs: TeamRun[] = []
  for (let i = 0; i < tabIds.length; i++) {
    const hit = teamOfTab(tabIds[i])
    if (!hit || hit.role !== 'lead') continue
    let n = 0
    for (let j = i + 1; j < tabIds.length; j++) {
      const next = teamOfTab(tabIds[j])
      if (!next || next.role !== 'member' || next.key !== hit.key) break
      n++
    }
    runs.push({ teamKey: hit.key, start: i, members: n })
    i += n
  }
  return runs
}

/**
 * Every member tab sitting in a run behind its lead in this list, whatever the collapse state: the sidebar folds exactly
 * these into the lead's beads. A member outside a run (lead in another workspace, or left open when the lead closed) is absent.
 */
export function runMemberIds(
  tabIds: readonly string[],
  teamOfTab: (tabId: string) => TabTeamHit | null | undefined,
): Set<string> {
  const members = new Set<string>()
  for (const run of teamRuns(tabIds, teamOfTab)) for (let k = 1; k <= run.members; k++) members.add(tabIds[run.start + k])
  return members
}

/** The member tabs a collapse hides: those in a run behind their lead, while that team is collapsed. */
export function hiddenMemberIds(
  tabIds: readonly string[],
  collapsed: Record<string, boolean>,
  teamOfTab: (tabId: string) => TabTeamHit | null | undefined,
): Set<string> {
  const hidden = new Set<string>()
  for (const run of teamRuns(tabIds, teamOfTab)) {
    if (collapsed[run.teamKey] !== true) continue
    for (let k = 1; k <= run.members; k++) hidden.add(tabIds[run.start + k])
  }
  return hidden
}
