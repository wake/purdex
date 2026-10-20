// spa/src/components/team/panel-view.test.ts — what the panel area shows for the active tab (team spec §4.4 Round 3, first match wins; task groups spec §7).
import { describe, it, expect } from 'vitest'
import { panelView } from './panel-view'
import type { TeamPanelTeam } from './team-display'
import type { PersonalTarget } from '../../lib/task-groups/personal'

const team = { teamKey: 'h\u0000t', name: 'T' } as TeamPanelTeam
const drill = { 'h\u0000t': { hostId: 'h', sessionId: 's' } }
const personal: PersonalTarget = { tabId: 't1', hostId: 'h1', sessionCode: 'c1', agentSessionId: 'cc-1', agentType: 'cc', ended: false }

describe('panelView', () => {
  it('a team tab without a drill shows the team, even when it has an agent session of its own', () => {
    expect(panelView({ panelTeam: team, teamDrill: {}, personal })).toEqual({ kind: 'team', team })
  })
  it('a team tab with a drill shows the drilled workbook', () => {
    expect(panelView({ panelTeam: team, teamDrill: drill, personal: null })).toEqual({ kind: 'workbook', from: 'team', team })
  })
  it('a tab of no team with an agent session shows the personal panel', () => {
    expect(panelView({ panelTeam: null, teamDrill: drill, personal })).toEqual({ kind: 'personal', target: personal })
  })
  it('a tab of no team and no agent session shows nothing', () => {
    expect(panelView({ panelTeam: null, teamDrill: drill, personal: null })).toBeNull()
  })
  it('on a journal.v1 host a persisted drill is ignored, and the personal panel still shows', () => {
    expect(panelView({ panelTeam: team, teamDrill: drill, personal: null, journalHost: true })).toEqual({ kind: 'team', team })
    expect(panelView({ panelTeam: null, teamDrill: {}, personal, journalHost: true })).toEqual({ kind: 'personal', target: personal })
  })
  it('is pure: the same input gives the same view after another tab was asked about', () => {
    const first = panelView({ panelTeam: null, teamDrill: {}, personal })
    panelView({ panelTeam: team, teamDrill: {}, personal: null })
    expect(panelView({ panelTeam: null, teamDrill: {}, personal })).toEqual(first)
  })
})
