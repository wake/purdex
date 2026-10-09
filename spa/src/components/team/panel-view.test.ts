// spa/src/components/team/panel-view.test.ts — what the panel area shows for the active tab (team spec §4.4, first match wins).
import { describe, it, expect } from 'vitest'
import { panelView } from './panel-view'
import type { TeamPanelTeam } from './team-display'

const team = { teamKey: 'h\u0000t', name: 'T' } as TeamPanelTeam
const drill = { 'h\u0000t': { hostId: 'h', sessionId: 's' } }

describe('panelView', () => {
  it('a tab whose workbook toggle is on shows its workbook, even on a team tab', () => {
    expect(panelView('a', { workbookTabs: { a: true }, panelTeam: team, teamDrill: {} })).toEqual({ kind: 'workbook', from: 'tab' })
    expect(panelView('a', { workbookTabs: { a: true }, panelTeam: team, teamDrill: drill })).toEqual({ kind: 'workbook', from: 'tab' })
    expect(panelView('a', { workbookTabs: { a: true }, panelTeam: null, teamDrill: {} })).toEqual({ kind: 'workbook', from: 'tab' })
  })
  it('a team tab without a drill shows the team', () => {
    expect(panelView('a', { workbookTabs: {}, panelTeam: team, teamDrill: {} })).toEqual({ kind: 'team', team })
  })
  it('a team tab with a drill shows the drilled workbook', () => {
    expect(panelView('a', { workbookTabs: {}, panelTeam: team, teamDrill: drill })).toEqual({ kind: 'workbook', from: 'team' })
  })
  it('a toggle on another tab does not matter', () => {
    expect(panelView('a', { workbookTabs: { b: true }, panelTeam: team, teamDrill: {} })).toEqual({ kind: 'team', team })
  })
  it('a tab of no team (toggle off) shows nothing, also with no active tab', () => {
    expect(panelView('a', { workbookTabs: {}, panelTeam: null, teamDrill: drill })).toBeNull()
    expect(panelView(null, { workbookTabs: {}, panelTeam: null, teamDrill: {} })).toBeNull()
  })
  it('switching away and back gives the same view (no memory is cleared)', () => {
    const memory = { workbookTabs: { a: true } as Record<string, true>, teamDrill: drill }
    const first = panelView('a', { ...memory, panelTeam: team })
    panelView('b', { ...memory, panelTeam: null })
    expect(panelView('a', { ...memory, panelTeam: team })).toEqual(first)
  })
})
