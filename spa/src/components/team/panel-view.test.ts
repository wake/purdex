// spa/src/components/team/panel-view.test.ts — what the panel area shows for the active tab (team spec §4.4 Round 3, first match wins).
import { describe, it, expect } from 'vitest'
import { panelView } from './panel-view'
import type { TeamPanelTeam } from './team-display'

const team = { teamKey: 'h\u0000t', name: 'T' } as TeamPanelTeam
const drill = { 'h\u0000t': { hostId: 'h', sessionId: 's' } }
const own = { hostId: 'h1', sessionId: 'cc-1' }

describe('panelView', () => {
  it('a team tab without a drill shows the team, even when its own conversation has a workbook', () => {
    expect(panelView({ panelTeam: team, teamDrill: {}, own })).toEqual({ kind: 'team', team })
  })
  it('a team tab with a drill shows the drilled workbook', () => {
    expect(panelView({ panelTeam: team, teamDrill: drill, own: null })).toEqual({ kind: 'workbook', from: 'team', team })
  })
  it('a tab of no team shows its own conversation workbook when it has one', () => {
    expect(panelView({ panelTeam: null, teamDrill: drill, own })).toEqual({ kind: 'workbook', from: 'own', hostId: 'h1', sessionId: 'cc-1' })
  })
  it('a tab of no team without a workbook shows nothing', () => {
    expect(panelView({ panelTeam: null, teamDrill: drill, own: null })).toBeNull()
  })
  it('is pure: the same input gives the same view after another tab was asked about', () => {
    const first = panelView({ panelTeam: null, teamDrill: {}, own })
    panelView({ panelTeam: team, teamDrill: {}, own: null })
    expect(panelView({ panelTeam: null, teamDrill: {}, own })).toEqual(first)
  })
})
