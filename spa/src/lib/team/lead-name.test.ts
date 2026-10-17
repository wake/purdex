import { describe, expect, it } from 'vitest'
import { leadNameBySession, leadNameByTeam } from './lead-name'
import type { TeamRoster } from './roster'

const roster = (name: string, leadTitle?: string): Record<string, TeamRoster[]> => ({
  h: [{
    id: 't1', host_id: 'd', created_at: 1, team_name: name, team_label: '',
    lead: { session_id: 'L', ref: '_l', address: 'm/l', live: true, ...(leadTitle !== undefined ? { title: leadTitle } : {}) }, members: [],
  }],
})

describe('lead-name', () => {
  it('a caller title that is empty or blank counts as not passed: the roster lead title is used', () => {
    expect(leadNameBySession(roster('Team', 'roster-title'), 'L', '')).toEqual({ name: 'Team', title: 'roster-title' })
    expect(leadNameBySession(roster('Team', 'roster-title'), 'L', '   ')).toEqual({ name: 'Team', title: 'roster-title' })
    expect(leadNameByTeam(roster('Team', 'roster-title'), 't1', '')).toEqual({ name: 'Team', title: 'roster-title' })
  })

  it('a caller title wins when it is not blank; an empty team name names nothing', () => {
    expect(leadNameBySession(roster('Team', 'roster-title'), 'L', 'mine')).toEqual({ name: 'Team', title: 'mine' })
    expect(leadNameBySession(roster('', 'roster-title'), 'L', 'mine')).toBeUndefined()
  })
})
