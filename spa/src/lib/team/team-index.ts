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
  if (bySession.size > 0) {
    for (const tabId of Object.keys(tabsById)) {
      if (tabsById[tabId].pinned === true) continue // a pinned tab is never grouped (spec §4.2)
      for (const { key } of shownSessions(tabsById[tabId].layout, sessionsByHost)) {
        const hit = bySession.get(key)
        if (hit) {
          byTabId.set(tabId, hit)
          break
        }
      }
    }
  }
  return { byTabId, byKey: new Map(views.map((v) => [v.key, v])), bySession }
}
