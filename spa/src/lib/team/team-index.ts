// spa/src/lib/team/team-index.ts — the lookups the team surfaces read (plan review #6–#8). `teamOfTab` answers one tab and
// walks every seat each call; a tab bar of 40 tabs calling it per tab is O(tabs × seats) on every render. `buildTeamIndex`
// answers all of them in one pass over the seats and the tabs, with the same rule (`seatLookup` + `shownSessions` are the
// functions `teamOfTab` uses), and the surfaces read the maps in O(1). Pure; the provider builds it once per input change.
import type { Tab } from '../../types/tab'
import { seatLookup, shownSessions, type SeatHit, type TeamView, type TeamViewsInput } from './team-views'

export interface TeamIndex {
  /** Tab id → the team and role the tab is drawn as (absent for a tab that shows no team session, and for a pinned tab:
   *  a pinned tab is never grouped). Equals `teamOfTab` for every unpinned tab. */
  byTabId: Map<string, SeatHit>
  /** The INCLUSIVE seat map (task groups spec §3): tab id → the seat of the first pane, in pre-order, whose session is a live team seat's session — pinned tabs included, and a team whose lead tab is pinned included.
   *  Only the readers that mean "this tab belongs to that team" read it (the panel routing, `panelTeam`); everything that DRAWS a team group (marks, beads, the member collapse, the tab lifecycle, the run drawing)
   *  and the pin guard stay on `byTabId`, which leaves a pinned tab out. */
  seatByTabId: Map<string, SeatHit>
  byKey: Map<string, TeamView>
  /** `<hostId>\0<tmux session name>` → the seat that session is. */
  bySession: Map<string, SeatHit>
}

export function buildTeamIndex(
  views: readonly TeamView[],
  tabsById: Record<string, Pick<Tab, 'layout'> & { pinned?: boolean }>,
  sessionsByHost: TeamViewsInput['sessionsByHost'],
): TeamIndex {
  const bySession = seatLookup(views)
  const byTabId = new Map<string, SeatHit>()
  const seatByTabId = new Map<string, SeatHit>()
  // A group starts at its lead (spec §4.2) and a pinned tab is never grouped: a team whose lead tab is pinned has no
  // group at all (its members would be a group with no lead to carry the label).
  const ungrouped = new Set(views.filter((v) => v.lead.tabId !== null && tabsById[v.lead.tabId]?.pinned === true).map((v) => v.key))
  if (bySession.size > 0) {
    for (const tabId of Object.keys(tabsById)) {
      // the inclusive map: the first seat among the tab's panes, pinned or not
      for (const { key } of shownSessions(tabsById[tabId].layout, sessionsByHost)) {
        const first = bySession.get(key)
        if (first) { seatByTabId.set(tabId, first); break }
      }
      if (tabsById[tabId].pinned === true) continue // a pinned tab is never grouped (spec §4.2)
      for (const { key } of shownSessions(tabsById[tabId].layout, sessionsByHost)) {
        const hit = bySession.get(key)
        if (hit && ungrouped.has(hit.key)) break
        if (hit) {
          byTabId.set(tabId, hit)
          break
        }
      }
    }
  }
  return { byTabId, seatByTabId, byKey: new Map(views.map((v) => [v.key, v])), bySession }
}
