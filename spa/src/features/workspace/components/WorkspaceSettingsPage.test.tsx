vi.mock('../lib/icon-path-cache', () => ({
  getIconPath: () => 'M0,0',
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceSettingsPage } from './WorkspaceSettingsPage'
import { UNSORTED_WORKSPACE_ID, useWorkspaceStore } from '../store'
import { useTabStore } from '../../../stores/useTabStore'
import { useHistoryStore } from '../../../stores/useHistoryStore'
import { createTab, type Tab } from '../../../types/tab'

describe('WorkspaceSettingsPage', () => {
  let wsId: string

  beforeEach(() => {
    cleanup()
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
    useWorkspaceStore.getState().reset()
    const ws = useWorkspaceStore.getState().addWorkspace('Test WS')
    wsId = ws.id
  })

  it('renders workspace name in editable input', () => {
    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    const input = screen.getByDisplayValue('Test WS')
    expect(input).toBeInTheDocument()
  })

  it('updates workspace name on input change + blur', () => {
    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    const input = screen.getByDisplayValue('Test WS')
    fireEvent.change(input, { target: { value: 'Renamed' } })
    fireEvent.blur(input)
    expect(useWorkspaceStore.getState().workspaces[0].name).toBe('Renamed')
  })

  it('has maxLength on name input to prevent excessively long names', () => {
    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    const input = screen.getByDisplayValue('Test WS') as HTMLInputElement
    expect(input.maxLength).toBe(64)
  })

  it('renders delete button and shows confirm dialog', () => {
    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    fireEvent.click(screen.getByTestId('delete-workspace-btn'))
    // WorkspaceDeleteDialog renders a delete confirm dialog with the workspace name
    expect(screen.getByText(/Delete Test WS/i)).toBeInTheDocument()
  })

  // Every tab belongs to exactly one workspace (Profile Sync spec §4.3): a tab the user keeps when deleting its
  // workspace no longer "goes Home" — it moves to the next workspace (the previous one for the last; a new
  // Unsorted when none is left).
  describe('deleting a workspace while keeping its tabs', () => {
    const tabIn = (id: string, content: Parameters<typeof createTab>[0] = { kind: 'dashboard' }): Tab => {
      const tab = createTab(content)
      useTabStore.getState().addTab(tab)
      useWorkspaceStore.getState().insertTab(tab.id, id)
      return tab
    }
    const ownersOf = (tabId: string) => useWorkspaceStore.getState().workspaces.filter((w) => w.tabs.includes(tabId)).map((w) => w.id)
    /** Opens the dialog, un-ticks the first `keep` tabs (ticked = close), confirms. */
    const deleteKeeping = (id: string, keep: number) => {
      render(<WorkspaceSettingsPage workspaceId={id} />)
      fireEvent.click(screen.getByTestId('delete-workspace-btn'))
      screen.queryAllByRole('checkbox').slice(0, keep).forEach((box) => fireEvent.click(box))
      fireEvent.click(screen.getByRole('button', { name: 'Delete' }))
    }

    it('the FIRST of three → its kept tabs move to the next workspace, after the tabs already there; focus follows', () => {
      const [kept, closed] = [tabIn(wsId), tabIn(wsId)]
      const b = useWorkspaceStore.getState().addWorkspace('B')
      const c = useWorkspaceStore.getState().addWorkspace('C')
      const inB = tabIn(b.id)
      useWorkspaceStore.getState().setActiveWorkspace(wsId)
      useTabStore.getState().setActiveTab(kept.id)

      deleteKeeping(wsId, 1)

      const state = useWorkspaceStore.getState()
      expect(state.workspaces.map((w) => w.id)).toEqual([b.id, c.id])
      expect(state.workspaces[0].tabs).toEqual([inB.id, kept.id])
      expect(useTabStore.getState().tabs[closed.id]).toBeUndefined()
      expect(state.activeWorkspaceId).toBe(b.id)
      expect(useTabStore.getState().activeTabId).toBe(kept.id)
    })

    it('the MIDDLE of three → the next workspace, not the first', () => {
      const b = useWorkspaceStore.getState().addWorkspace('B')
      const c = useWorkspaceStore.getState().addWorkspace('C')
      const kept = tabIn(b.id)
      useWorkspaceStore.getState().setActiveWorkspace(wsId) // not the one being deleted: focus still follows the kept tabs
      deleteKeeping(b.id, 1)
      expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual([wsId, c.id])
      expect(ownersOf(kept.id)).toEqual([c.id])
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(c.id)
      expect(useTabStore.getState().activeTabId).toBe(kept.id)
    })

    it('the LAST of three → the previous workspace', () => {
      const b = useWorkspaceStore.getState().addWorkspace('B')
      const c = useWorkspaceStore.getState().addWorkspace('C')
      const kept = tabIn(c.id)
      deleteKeeping(c.id, 1)
      expect(useWorkspaceStore.getState().workspaces.map((w) => w.id)).toEqual([wsId, b.id])
      expect(ownersOf(kept.id)).toEqual([b.id])
    })

    it('the ONLY workspace → a new Unsorted gets the kept tabs and becomes active', () => {
      const [k1, k2] = [tabIn(wsId), tabIn(wsId)]
      deleteKeeping(wsId, 2)
      const state = useWorkspaceStore.getState()
      expect(state.workspaces.map((w) => w.id)).toEqual([UNSORTED_WORKSPACE_ID])
      expect(state.workspaces[0]).toMatchObject({ name: 'Unsorted', tabs: [k1.id, k2.id] })
      expect(state.activeWorkspaceId).toBe(UNSORTED_WORKSPACE_ID)
      expect(useTabStore.getState().activeTabId).toBe(k1.id)
    })

    it('a tab the dialog could not close (locked) is kept, too', () => {
      const b = useWorkspaceStore.getState().addWorkspace('B')
      const locked = tabIn(wsId)
      useTabStore.getState().toggleLock(locked.id)
      deleteKeeping(wsId, 0) // everything ticked
      expect(useTabStore.getState().tabs[locked.id]).toBeDefined()
      expect(ownersOf(locked.id)).toEqual([b.id])
    })

    it("the workspace's own settings tab (not in the dialog) is closed with it: deleting the only workspace leaves nothing behind", () => {
      const own = tabIn(wsId, { kind: 'settings', scope: { workspaceId: wsId } })
      const closed = tabIn(wsId)
      deleteKeeping(wsId, 0)
      expect(useTabStore.getState().tabs[own.id]).toBeUndefined()
      expect(useTabStore.getState().tabs[closed.id]).toBeUndefined()
      expect(useWorkspaceStore.getState().workspaces).toEqual([])
    })

    // Review F4: `closeTab` returns early for a locked tab, so a locked settings tab survived and was moved along.
    it("a LOCKED settings tab of the deleted workspace is removed all the same; another workspace's settings tab is not touched", () => {
      const b = useWorkspaceStore.getState().addWorkspace('B')
      const own = tabIn(wsId, { kind: 'settings', scope: { workspaceId: wsId } })
      useTabStore.getState().toggleLock(own.id)
      const ownElsewhere = tabIn(b.id, { kind: 'settings', scope: { workspaceId: wsId } }) // dragged into B
      const others = tabIn(wsId, { kind: 'settings', scope: { workspaceId: b.id } }) // B's settings, opened in here
      const global = tabIn(wsId, { kind: 'settings', scope: 'global' })

      deleteKeeping(wsId, 0)

      const { tabs, tabOrder } = useTabStore.getState()
      expect(tabs[own.id]).toBeUndefined()
      expect(tabs[ownElsewhere.id]).toBeUndefined()
      expect(tabOrder).toEqual([others.id, global.id])
      expect(useWorkspaceStore.getState().workspaces.map((w) => w.tabs)).toEqual([[others.id, global.id]])
      expect(useHistoryStore.getState().closedTabs.map((c) => c.tab.id)).toEqual([]) // nothing to reopen it into
    })

    it('closing every tab still focuses the workspace that takes over', () => {
      tabIn(wsId)
      const b = useWorkspaceStore.getState().addWorkspace('B')
      const inB = tabIn(b.id)
      useWorkspaceStore.getState().setActiveWorkspace(wsId)
      deleteKeeping(wsId, 0)
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(b.id)
      expect(useTabStore.getState().activeTabId).toBe(inB.id)
    })
  })

  it('shows "not found" when workspace does not exist', () => {
    render(<WorkspaceSettingsPage workspaceId="nonexistent" />)
    expect(screen.getByText(/not found/i)).toBeInTheDocument()
  })
})
