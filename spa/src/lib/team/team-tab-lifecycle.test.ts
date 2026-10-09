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
import { useEditorStore } from '../../stores/useEditorStore'
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
async function workspaceWritesAfterANudge(): Promise<number> {
  let writes = 0
  const off = useWorkspaceStore.subscribe((s, prev) => { if (s.workspaces !== prev.workspaces) writes++ })
  useSessionStore.setState({ sessions: { ...useSessionStore.getState().sessions } })
  await settle()
  off()
  return writes
}
/** The subscriber looks at the stores on a microtask (after the action that changed them has finished). */
const settle = async () => { for (let i = 0; i < 4; i++) await Promise.resolve() }
const tabIds = () => Object.keys(useTabStore.getState().tabs).sort()

describe('order', () => {
  it('group tabs are made contiguous in team order at the lead\'s index', async () => {
    seedScene({
      members,
      tabs: [['x', null], ['lead', 'lead-tm'], ['y', null], ['mb', 'b-tm'], ['z', null], ['ma', 'a-tm']],
      workspaces: [{ id: 'w1', tabs: ['x', 'lead', 'y', 'mb', 'z', 'ma'] }],
    })
    await settle()
    expect(wsTabs('w1')).toEqual(['x', 'lead', 'ma', 'mb', 'y', 'z'])
  })

  it('follows the team order the person arranged', async () => {
    seedScene({ members, tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['mb', 'b-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'mb', 'x'] }] })
    await settle()
    useTeamUiStore.getState().setMemberOrder(KEY, ['B', 'A'])
    await settle()
    expect(wsTabs('w1')).toEqual(['lead', 'mb', 'ma', 'x'])
  })

  it('normalisation is a no-op when already in order (no write)', async () => {
    seedScene({ members, tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['mb', 'b-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'mb', 'x'] }] })
    await settle()
    await settle()
    expect(await workspaceWritesAfterANudge()).toBe(0)
  })

  it('a released member\'s tab ends up right after the group (R6)', async () => {
    const scene = { tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['mb', 'b-tm'], ['x', null]] as Array<[string, string | null]>, workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'mb', 'x'] }] }
    seedScene({ members, ...scene })
    await settle()
    await settle()
    expect(wsTabs('w1')).toEqual(['lead', 'ma', 'mb', 'x'])
    seedScene({ members: [['B', 'b-tm']], ...scene }) // A is released: its tab is a normal tab now
    await settle()
    expect(wsTabs('w1')).toEqual(['lead', 'mb', 'ma', 'x'])
    expect(tabIds()).toEqual(['lead', 'ma', 'mb', 'x']) // never closed by this
  })

  it('a member tab in another workspace is not moved', async () => {
    seedScene({
      members,
      tabs: [['lead', 'lead-tm'], ['x', null], ['ma', 'a-tm'], ['y', null]],
      workspaces: [{ id: 'w1', tabs: ['lead', 'x'] }, { id: 'w2', tabs: ['ma', 'y'] }],
    })
    await settle()
    expect(wsTabs('w1')).toEqual(['lead', 'x'])
    expect(wsTabs('w2')).toEqual(['ma', 'y'])
    await settle()
    expect(await workspaceWritesAfterANudge()).toBe(0) // and it is left alone, not rewritten on every pass
  })

  it('a member that sits before its lead moves behind it', async () => {
    seedScene({ members, tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['ma', 'x', 'lead'] }] })
    await settle()
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

  it('closing the lead by ✕ closes the group', async () => {
    scene()
    await settle()
    closeTab('lead')
    await settle()
    expect(tabIds()).toEqual(['x', 'z'])
    expect(wsTabs('w1')).toEqual(['x', 'z'])
  })

  it('… by ⌘W (useShortcuts) closes the group', async () => {
    scene()
    await settle()
    let fire: (a: string) => void = () => {}
    ;(window as unknown as Record<string, unknown>).electronAPI = {
      onShortcut: (f: (p: { action: string }) => void) => { fire = (action) => f({ action }); return vi.fn() },
      signalReady: () => {},
    }
    renderHook(() => useShortcuts())
    fire('close-tab')
    await settle()
    expect(tabIds()).toEqual(['x', 'z'])
  })

  it('… by the tab store\'s workspace close (the context menu path) closes the group', async () => {
    scene()
    await settle()
    useWorkspaceStore.getState().closeTabInWorkspace('lead')
    await settle()
    expect(tabIds()).toEqual(['x', 'z'])
  })

  it('… by a TerminatedPane / WorkerEndedPane close (lib/tab-lifecycle closeTab) closes the group', async () => {
    scene()
    await settle()
    useTabStore.getState().setActiveTab('ma')
    closeTab('lead', { skipHistory: true })
    await settle()
    expect(tabIds()).toEqual(['x', 'z'])
    expect(useTabStore.getState().activeTabId).not.toBe('ma') // the member that was on screen went too
  })

  it('… by removing the workspace leaves nothing behind', async () => {
    scene()
    await settle()
    for (const id of [...wsTabs('w1')]) closeTab(id) // WorkspaceSettingsPage closes the tabs, then removes the workspace
    useWorkspaceStore.getState().removeWorkspace('w1')
    await settle()
    expect(tabIds()).toEqual([])
    expect(useTeamUiStore.getState().ghostWorkspace[KEY]).toBe('w1')
  })

  it('a locked member tab survives and rejoins when the lead reopens', async () => {
    scene()
    await settle()
    useTabStore.getState().toggleLock('ma')
    closeTab('lead')
    await settle()
    expect(tabIds()).toEqual(['ma', 'x', 'z'])
    expect(useTabStore.getState().tabs.mb).toBeUndefined()
    // the lead's tab is back: the group is one run again, the member rejoining
    seedScene({
      members,
      tabs: [['x', null], ['z', null], ['ma', 'a-tm'], ['lead2', 'lead-tm']],
      workspaces: [{ id: 'w1', tabs: ['x', 'z', 'ma', 'lead2'] }],
    })
    await settle()
    expect(wsTabs('w1')).toEqual(['x', 'z', 'lead2', 'ma'])
  })

  it('members\' closes add no history', async () => {
    scene()
    await settle()
    closeTab('lead')
    await settle()
    const closed = useHistoryStore.getState().closedTabs
    expect(closed.map((r) => r.tab.id)).toEqual(['lead']) // the lead's own close is the caller's; the members add none
  })

  it('ghost workspace recorded', async () => {
    scene()
    await settle()
    closeTab('lead')
    await settle()
    expect(useTeamUiStore.getState().ghostWorkspace[KEY]).toBe('w1')
  })

  it('moving the lead tab to another workspace does not close the group', async () => {
    scene()
    await settle()
    useWorkspaceStore.getState().removeTabFromWorkspace('w1', 'lead')
    await settle() // the lead is in no workspace for a moment: still not gone
    useWorkspaceStore.getState().addTabToWorkspace('w2', 'lead')
    await settle()
    await settle()
    expect(tabIds()).toEqual(['lead', 'ma', 'mb', 'x', 'z'])
    expect(useTeamUiStore.getState().ghostWorkspace[KEY]).toBeUndefined()
  })

  it('closing the active lead leaves a valid active tab and workspace pointer (the cascade waits for the close)', async () => {
    scene()
    useWorkspaceStore.getState().setWorkspaceActiveTab('w1', 'lead')
    await settle()
    closeTab('lead')
    await settle()
    const active = useTabStore.getState().activeTabId
    expect(active !== null && useTabStore.getState().tabs[active]).toBeTruthy()
    const wsActive = useWorkspaceStore.getState().workspaces.find((w) => w.id === 'w1')!.activeTabId
    expect(wsActive !== null && useTabStore.getState().tabs[wsActive]).toBeTruthy()
  })

  it('a member whose close the person declined (unsaved editor) stays, and the rest still close', async () => {
    scene()
    const mb = useTabStore.getState().tabs.mb
    const tmux = (mb.layout as { pane: unknown }).pane
    useTabStore.setState({
      tabs: { ...useTabStore.getState().tabs, mb: { ...mb, layout: { type: 'split', id: 's', direction: 'h', sizes: [50, 50], children: [
        { type: 'leaf', pane: tmux }, { type: 'leaf', pane: { id: 'pe', content: { kind: 'editor', source: { type: 'local' }, filePath: '/f.txt' } } },
      ] } } as never },
    })
    useEditorStore.setState({ buffers: { 'local:/f.txt': { isDirty: true } as never } })
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    await settle()
    closeTab('lead')
    await settle()
    expect(confirm).toHaveBeenCalledTimes(1) // asked once, not again on every pass
    expect(tabIds()).toEqual(['mb', 'x', 'z']) // ma went; mb was kept by the person
    confirm.mockRestore()
    useEditorStore.setState({ buffers: {} })
  })

  it('closing a member only closes that tab (P8)', async () => {
    scene()
    await settle()
    closeTab('ma')
    await settle()
    expect(tabIds()).toEqual(['lead', 'mb', 'x', 'z'])
  })

  it('a lead tab that was never grouped (no team) closes alone', async () => {
    seedScene({ tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']], workspaces: [{ id: 'w1', tabs: ['lead', 'ma'] }] })
    await settle()
    useTeamUiStore.setState({ ghostWorkspace: {} })
    // no roster members: `ma` is not a member; a team of one lead only
    closeTab('lead')
    await settle()
    expect(tabIds()).toEqual(['ma'])
  })
})
