// spa/src/lib/team/team-runs.test.ts — `runMemberIds` (the sidebar's non-rows) agrees with the tab bar's runs and with the
// stepping filter on the same inputs (plan TI-3 fix: one definition of "a member in a run behind its lead").
import { describe, it, expect } from 'vitest'
import { groupSegments } from '../../components/team/groupSegments'
import type { TeamTabMark } from '../../components/team/team-display'
import { hiddenMemberIds, runMemberIds, type TabTeamHit } from './team-runs'
import { visibleTabIds } from './team-actions'

const mark = (teamKey: string, role: 'lead' | 'member'): TeamTabMark => ({
  teamKey, color: '#a78bfa', role,
  first: false, last: false, collapsed: false, hidden: false, seatState: 'active', hostAlias: '',
})
const marks: Record<string, TeamTabMark> = {
  lead: mark('T', 'lead'), a: mark('T', 'member'), b: mark('T', 'member'),
  lead2: mark('U', 'lead'), c: mark('U', 'member'), stray: mark('T', 'member'), orphan: mark('V', 'member'),
}
const hit = (id: string): TabTeamHit | null => (marks[id] ? { key: marks[id].teamKey, role: marks[id].role } : null)

describe('runMemberIds', () => {
  const lists = [
    ['x', 'lead', 'a', 'b', 'y'],
    ['lead', 'x', 'a'], // a member not right behind its lead is an ordinary tab
    ['orphan', 'x', 'stray'], // lead is elsewhere
    ['lead', 'a', 'lead2', 'c', 'orphan'],
    [],
  ]
  it.each(lists)('agrees with groupSegments and visibleTabIds: %j', (...ids) => {
    const members = runMemberIds(ids, hit)
    const segs = groupSegments(ids.map((id) => ({ id })), (id) => marks[id] ?? null, {})
    const inRuns = new Set(segs.flatMap((s) => (s.kind === 'team' ? s.tabs.slice(1).map((t) => t.id) : [])))
    expect(members).toEqual(inRuns)
    // collapse-independent: collapsing every team hides exactly these.
    expect(hiddenMemberIds(ids, { T: true, U: true, V: true }, hit)).toEqual(members)
    expect(new Set(ids.filter((id) => !members.has(id)))).toEqual(new Set(visibleTabIds(ids, { T: true, U: true, V: true }, hit)))
  })
})
