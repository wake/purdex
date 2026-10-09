// spa/src/lib/team/team-tab-lifecycle.test.ts — the group's tabs stay one contiguous run in team order (R5, R6) and the
// group closes with its lead's tab by any path (P2). Real stores throughout; `startTeamTabLifecycle` is the one subscriber.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import { renderHook } from '@testing-library/react'
import { startTeamTabLifecycle } from './team-tab-lifecycle'
import { useShortcuts } from '../../hooks/useShortcuts'
import { closeTab } from '../tab-lifecycle'
import { useTabStore } from '../../stores/useTabStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useHistoryStore } from '../../stores/useHistoryStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { KEY, resetTeamStores, seedScene, wsTabs } from './__tests__/team-fixture'

let stop: () => void
beforeEach(() => {
  resetTeamStores()
  useHistoryStore.setState({ closedTabs: [] })
  stop = startTeamTabLifecycle()
})
afterEach(() => {
  stop()
  delete (window as unknown as Record<string, unknown>).electronAPI
})

const members: Array<[string, string]> = [['A', 'a-tm'], ['B', 'b-tm']]
/** Changes an input of the subscriber without touching the order (so a pass runs), and counts the workspace writes. */
function workspaceWritesAfterANudge(): number {
  let writes = 0
  const off = useWorkspaceStore.subscribe((s, prev) => { if (s.workspaces !== prev.workspaces) writes++ })
  useSessionStore.setState({ sessions: { ...useSessionStore.getState().sessions } })
  off()
  return writes
}
const tabIds = () => Object.keys(useTabStore.getState().tabs).sort()

describe('order', () => {
  it('group tabs are made contiguous in team order at the lead\'s index', () => {
    seedScene({
      members,
      tabs: [['x', null], ['lead', 'lead-tm'], ['y', null], ['mb', 'b-tm'], ['z', null], ['ma', 'a-tm']],
      workspaces: [{ id: 'w1', tabs: ['x', 'lead', 'y', 'mb', 'z', 'ma'] }],
    })
    expect(wsTabs('w1')).toEqual(['x', 'lead', 'ma', 'mb', 'y', 'z'])
  })

  it('follows the team order the person arranged', () => {
    seedScene({ members, tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['mb', 'b-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'mb', 'x'] }] })
    useTeamUiStore.getState().setMemberOrder(KEY, ['B', 'A'])
    expect(wsTabs('w1')).toEqual(['lead', 'mb', 'ma', 'x'])
  })

  it('normalisation is a no-op when already in order (no write)', () => {
    seedScene({ members, tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['mb', 'b-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'mb', 'x'] }] })
    expect(workspaceWritesAfterANudge()).toBe(0)
  })

  it('a released member\'s tab ends up right after the group (R6)', () => {
    const scene = { tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['mb', 'b-tm'], ['x', null]] as Array<[string, string | null]>, workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'mb', 'x'] }] }
    seedScene({ members, ...scene })
    expect(wsTabs('w1')).toEqual(['lead', 'ma', 'mb', 'x'])
    seedScene({ members: [['B', 'b-tm']], ...scene }) // A is released: its tab is a normal tab now
    expect(wsTabs('w1')).toEqual(['lead', 'mb', 'ma', 'x'])
    expect(tabIds()).toEqual(['lead', 'ma', 'mb', 'x']) // never closed by this
  })

  it('a member tab in another workspace is not moved', () => {
    seedScene({
      members,
      tabs: [['lead', 'lead-tm'], ['x', null], ['ma', 'a-tm'], ['y', null]],
      workspaces: [{ id: 'w1', tabs: ['lead', 'x'] }, { id: 'w2', tabs: ['ma', 'y'] }],
    })
    expect(wsTabs('w1')).toEqual(['lead', 'x'])
    expect(wsTabs('w2')).toEqual(['ma', 'y'])
    expect(workspaceWritesAfterANudge()).toBe(0) // and it is left alone, not rewritten on every pass
  })

  it('a member that sits before its lead moves behind it', () => {
    seedScene({ members, tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['ma', 'x', 'lead'] }] })
    expect(wsTabs('w1')).toEqual(['x', 'lead', 'ma'])
  })
})

describe('the lead\'s tab goes', () => {
  const scene = () => seedScene({
    members,
    tabs: [['x', null], ['lead', 'lead-tm'], ['ma', 'a-tm'], ['mb', 'b-tm'], ['z', null]],
    workspaces: [{ id: 'w1', tabs: ['x', 'lead', 'ma', 'mb', 'z'] }, { id: 'w2', tabs: [] }],
    activeTabId: 'lead',
  })

  it('closing the lead by ✕ closes the group', () => {
    scene()
    closeTab('lead')
    expect(tabIds()).toEqual(['x', 'z'])
    expect(wsTabs('w1')).toEqual(['x', 'z'])
  })

  it('… by ⌘W (useShortcuts) closes the group', () => {
    scene()
    let fire: (a: string) => void = () => {}
    ;(window as unknown as Record<string, unknown>).electronAPI = {
      onShortcut: (f: (p: { action: string }) => void) => { fire = (action) => f({ action }); return vi.fn() },
      signalReady: () => {},
    }
    renderHook(() => useShortcuts())
    fire('close-tab')
    expect(tabIds()).toEqual(['x', 'z'])
  })

  it('… by the tab store\'s workspace close (the context menu path) closes the group', () => {
    scene()
    useWorkspaceStore.getState().closeTabInWorkspace('lead')
    expect(tabIds()).toEqual(['x', 'z'])
  })

  it('… by a TerminatedPane / WorkerEndedPane close (lib/tab-lifecycle closeTab) closes the group', () => {
    scene()
    useTabStore.getState().setActiveTab('ma')
    closeTab('lead', { skipHistory: true })
    expect(tabIds()).toEqual(['x', 'z'])
    expect(useTabStore.getState().activeTabId).not.toBe('ma') // the member that was on screen went too
  })

  it('… by removing the workspace leaves nothing behind', () => {
    scene()
    for (const id of [...wsTabs('w1')]) closeTab(id) // WorkspaceSettingsPage closes the tabs, then removes the workspace
    useWorkspaceStore.getState().removeWorkspace('w1')
    expect(tabIds()).toEqual([])
    expect(useTeamUiStore.getState().ghostWorkspace[KEY]).toBe('w1')
  })

  it('a locked member tab survives and rejoins when the lead reopens', () => {
    scene()
    useTabStore.getState().toggleLock('ma')
    closeTab('lead')
    expect(tabIds()).toEqual(['ma', 'x', 'z'])
    expect(useTabStore.getState().tabs.mb).toBeUndefined()
    // the lead's tab is back: the group is one run again, the member rejoining
    seedScene({
      members,
      tabs: [['x', null], ['z', null], ['ma', 'a-tm'], ['lead2', 'lead-tm']],
      workspaces: [{ id: 'w1', tabs: ['x', 'z', 'ma', 'lead2'] }],
    })
    expect(wsTabs('w1')).toEqual(['x', 'z', 'lead2', 'ma'])
  })

  it('members\' closes add no history', () => {
    scene()
    closeTab('lead')
    const closed = useHistoryStore.getState().closedTabs
    expect(closed.map((r) => r.tab.id)).toEqual(['lead']) // the lead's own close is the caller's; the members add none
  })

  it('ghost workspace recorded', () => {
    scene()
    closeTab('lead')
    expect(useTeamUiStore.getState().ghostWorkspace[KEY]).toBe('w1')
  })

  it('moving the lead tab to another workspace does not close the group', () => {
    scene()
    useWorkspaceStore.getState().removeTabFromWorkspace('w1', 'lead')
    useWorkspaceStore.getState().addTabToWorkspace('w2', 'lead')
    expect(tabIds()).toEqual(['lead', 'ma', 'mb', 'x', 'z'])
    expect(useTeamUiStore.getState().ghostWorkspace[KEY]).toBeUndefined()
  })

  it('closing a member only closes that tab (P8)', () => {
    scene()
    closeTab('ma')
    expect(tabIds()).toEqual(['lead', 'mb', 'x', 'z'])
  })

  it('a lead tab that was never grouped (no team) closes alone', () => {
    seedScene({ tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']], workspaces: [{ id: 'w1', tabs: ['lead', 'ma'] }] })
    useTeamUiStore.setState({ ghostWorkspace: {} })
    // no roster members: `ma` is not a member; a team of one lead only
    closeTab('lead')
    expect(tabIds()).toEqual(['ma'])
  })
})
