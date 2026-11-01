// spa/src/stores/useRecapViewStore.test.ts — which session's recap history the panel area shows (plan T14b): one value, never persisted;
// opening it brings the panel out of the title bar / one-line form into the full one.
import { describe, it, expect, beforeEach } from 'vitest'
import { useRecapViewStore, openRecapView, isRecapOpen, type RecapTarget } from './useRecapViewStore'
import { useTeamUiStore } from './useTeamUiStore'
import { useTabStore } from './useTabStore'

const team: RecapTarget = { scope: 'h1\u0000t1', teamKey: 'h1\u0000t1', hostId: 'h1', sessionId: 'A', title: 'a', address: '' }
const solo: RecapTarget = { scope: 'tab-9', teamKey: null, hostId: 'h1', sessionId: 'S', title: 's', address: '' }

beforeEach(() => {
  useRecapViewStore.getState().close()
  useTeamUiStore.setState({ panelMode: {}, panelLast: {}, sharedPanelMode: 'titlebar', sharedPanelLast: 'full' })
})

describe('useRecapViewStore', () => {
  it('holds one open target and close() clears it', () => {
    expect(useRecapViewStore.getState().open).toBeNull()
    useRecapViewStore.getState().show(team)
    expect(useRecapViewStore.getState().open).toEqual(team)
    useRecapViewStore.getState().close()
    expect(useRecapViewStore.getState().open).toBeNull()
  })

  it('isRecapOpen needs the same scope and session', () => {
    useRecapViewStore.getState().show(team)
    expect(isRecapOpen(useRecapViewStore.getState().open, team.scope, 'A')).toBe(true)
    expect(isRecapOpen(useRecapViewStore.getState().open, team.scope, 'B')).toBe(false)
    expect(isRecapOpen(useRecapViewStore.getState().open, 'other', 'A')).toBe(false)
    expect(isRecapOpen(null, team.scope, 'A')).toBe(false)
  })

  it('openRecapView on a team in the title bar moves its panel to the full form', () => {
    useTeamUiStore.getState().setPanelMode(team.teamKey!, 'titlebar')
    expect(useTeamUiStore.getState().panelMode[team.teamKey!]).toBe('titlebar')
    openRecapView(team)
    expect(useTeamUiStore.getState().panelMode[team.teamKey!]).toBeUndefined() // `full` is the absence
    expect(useRecapViewStore.getState().open).toEqual(team)
  })

  it('openRecapView on a team in the one-line form also goes to full', () => {
    useTeamUiStore.getState().setPanelMode(team.teamKey!, 'line')
    expect(useTeamUiStore.getState().panelMode[team.teamKey!]).toBe('line')
    openRecapView(team)
    expect(useTeamUiStore.getState().panelMode[team.teamKey!]).toBeUndefined()
  })

  it('openRecapView for a personal session moves the shared value to full', () => {
    openRecapView(solo)
    expect(useTeamUiStore.getState().sharedPanelMode).toBe('full')
    expect(useRecapViewStore.getState().open).toEqual(solo)
  })
})

describe('the view closes with its panel (prototype: teamUp / teamBtn / perUp / perBtn / switchTab)', () => {
  const open = () => useRecapViewStore.getState().open

  it('a team panel folded into the title bar closes the view; putting it back shows the normal panel', () => {
    openRecapView(team)
    expect(open()).toEqual(team)
    useTeamUiStore.getState().setPanelMode(team.teamKey!, 'titlebar')
    expect(open()).toBeNull()
    useTeamUiStore.getState().setPanelMode(team.teamKey!, 'full')
    expect(open()).toBeNull()
  })

  it('another team going to the title bar leaves it open', () => {
    openRecapView(team)
    useTeamUiStore.getState().setPanelMode('h1\u0000other', 'titlebar')
    expect(open()).toEqual(team)
  })

  it('the one-line form keeps the view (the prototype keeps it too)', () => {
    openRecapView(team)
    useTeamUiStore.getState().setPanelMode(team.teamKey!, 'line')
    expect(open()).toEqual(team)
  })

  it('a personal panel folded into the title bar closes the view', () => {
    useTabStore.setState({ activeTabId: solo.scope })
    openRecapView(solo)
    expect(open()).toEqual(solo)
    useTeamUiStore.getState().setSharedPanelMode('titlebar')
    expect(open()).toBeNull()
  })

  it('switching tabs closes a personal view (not a team one); coming back shows the card', () => {
    useTabStore.setState({ activeTabId: solo.scope })
    openRecapView(solo)
    useTabStore.setState({ activeTabId: 'tab-other' })
    expect(open()).toBeNull()
    useTabStore.setState({ activeTabId: solo.scope })
    expect(open()).toBeNull()
    openRecapView(team)
    useTabStore.setState({ activeTabId: 'tab-x' })
    expect(open()).toEqual(team)
  })
})
