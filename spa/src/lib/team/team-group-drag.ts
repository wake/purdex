// spa/src/lib/team/team-group-drag.ts — the order a drag on the tab bar's normal zone writes (TG-1, spec §4.2 Drag).
// The unit of a move is a team run (the lead plus every member tab behind it, collapsed ones included — they are in `tabIds`
// even when not drawn) or a single plain tab. Whichever unit the drag started on is moved to where the target unit sits, as
// dnd-kit's arrayMove would; a drop on any tab of a group therefore lands before or after the whole group, never inside it.
import { teamRuns, type TabTeamHit } from './team-runs'

/**
 * The new order of `tabIds` for dragging `activeId` onto `overId`, or null when nothing changes: the active tab is a run's
 * member (that is a reorder inside the group, not a move of it), a tab is unknown, or both are in the same unit.
 */
export function dragReorder(
  tabIds: readonly string[],
  activeId: string,
  overId: string,
  teamOfTab: (tabId: string) => TabTeamHit | null | undefined,
): string[] | null {
  const runs = teamRuns(tabIds, teamOfTab)
  const units: string[][] = []
  const unitAt = new Map<string, number>()
  let nextRun = 0
  for (let i = 0; i < tabIds.length; ) {
    const r = runs[nextRun]
    const len = r && r.start === i ? 1 + r.members : 1
    if (len > 1 || (r && r.start === i)) nextRun++
    const unit = tabIds.slice(i, i + len)
    for (const id of unit) unitAt.set(id, units.length)
    units.push(unit)
    i += len
  }
  const from = unitAt.get(activeId)
  const to = unitAt.get(overId)
  if (from === undefined || to === undefined || from === to) return null
  // A member inside a run is not the unit's head: moving it alone would split the run.
  if (units[from][0] !== activeId) return null
  const next = units.slice()
  const [moved] = next.splice(from, 1)
  next.splice(to, 0, moved)
  return next.flat()
}
