import { describe, it, expect, beforeEach } from 'vitest'
import { useWorkspaceStore } from './store'
import type { Workspace } from '../../types/tab'

describe('useWorkspaceStore', () => {
  beforeEach(() => {
    useWorkspaceStore.getState().reset()
  })

  // === 全自由制基礎 ===

  it('initializes with empty workspaces and null activeWorkspaceId', () => {
    const state = useWorkspaceStore.getState()
    expect(state.workspaces).toEqual([])
    expect(state.activeWorkspaceId).toBeNull()
  })

  it('setActiveWorkspace accepts null', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws.id)
    useWorkspaceStore.getState().setActiveWorkspace(null)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })

  // === Workspace CRUD ===

  it('adds a workspace and auto-activates first', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('New WS')
    expect(ws.name).toBe('New WS')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(1)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws.id)
  })

  it('adds second workspace without changing active', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(2)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1.id)
    expect(ws2.name).toBe('WS2')
  })

  it('removes a workspace', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('To Remove')
    useWorkspaceStore.getState().removeWorkspace(ws2.id)
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(1)
    expect(useWorkspaceStore.getState().workspaces[0].id).toBe(ws1.id)
  })

  it('removes the last workspace and sets activeWorkspaceId to null', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Only')
    useWorkspaceStore.getState().removeWorkspace(ws.id)
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(0)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })

  it('removeWorkspace on nonexistent id does nothing', () => {
    useWorkspaceStore.getState().addWorkspace('WS')
    useWorkspaceStore.getState().removeWorkspace('nonexistent')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(1)
  })

  it('removeWorkspace on empty list does nothing', () => {
    useWorkspaceStore.getState().removeWorkspace('nonexistent')
    expect(useWorkspaceStore.getState().workspaces).toHaveLength(0)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBeNull()
  })

  it('switches active workspace', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().setActiveWorkspace(ws2.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws2.id)
    useWorkspaceStore.getState().setActiveWorkspace(ws1.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1.id)
  })

  it('switches activeWorkspaceId when removing active workspace', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().setActiveWorkspace(ws2.id)
    useWorkspaceStore.getState().removeWorkspace(ws2.id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1.id)
  })

  // === Workspace reorder ===

  it('reorderWorkspaces changes order', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    const ws3 = useWorkspaceStore.getState().addWorkspace('WS3')
    useWorkspaceStore.getState().reorderWorkspaces([ws3.id, ws1.id, ws2.id])
    const names = useWorkspaceStore.getState().workspaces.map((ws) => ws.name)
    expect(names).toEqual(['WS3', 'WS1', 'WS2'])
  })

  it('reorderWorkspaces preserves workspaces missing from orderedIds', () => {
    const ws1 = useWorkspaceStore.getState().addWorkspace('WS1')
    const ws2 = useWorkspaceStore.getState().addWorkspace('WS2')
    useWorkspaceStore.getState().addWorkspace('WS3')
    // Only pass ws2 and ws1 — ws3 should be appended at end
    useWorkspaceStore.getState().reorderWorkspaces([ws2.id, ws1.id])
    const names = useWorkspaceStore.getState().workspaces.map((ws) => ws.name)
    expect(names).toEqual(['WS2', 'WS1', 'WS3'])
  })

  // === addWorkspace options ===

  it('addWorkspace passes icon to createWorkspace', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('WithIcon', { icon: 'R' })
    expect(ws.icon).toBe('R')
  })

  // === Workspace settings ===

  it('renameWorkspace updates workspace name', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Old Name')
    useWorkspaceStore.getState().renameWorkspace(ws.id, 'New Name')
    expect(useWorkspaceStore.getState().workspaces[0].name).toBe('New Name')
  })

  it('setWorkspaceIcon updates workspace icon', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().setWorkspaceIcon(ws.id, 'R')
    expect(useWorkspaceStore.getState().workspaces[0].icon).toBe('R')
  })

  // === importWorkspace ===

  describe('importWorkspace', () => {
    it('imports workspace preserving original id and tabs', () => {
      const ws: Workspace = { id: 'imported-1', name: 'Imported', tabs: ['t1', 't2'], activeTabId: 't1' }
      useWorkspaceStore.getState().importWorkspace(ws)
      const result = useWorkspaceStore.getState().workspaces[0]
      expect(result.id).toBe('imported-1')
      expect(result.tabs).toEqual(['t1', 't2'])
      expect(result.activeTabId).toBe('t1')
    })

    it('does not change activeWorkspaceId', () => {
      const ws1 = useWorkspaceStore.getState().addWorkspace('Existing')
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1.id)
      const ws: Workspace = { id: 'imported-2', name: 'Imported', tabs: [], activeTabId: null }
      useWorkspaceStore.getState().importWorkspace(ws)
      expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(ws1.id) // 不變
    })

    it('imports with icon and iconWeight', () => {
      const ws: Workspace = { id: 'imported-3', name: 'Dev', icon: 'Code', iconWeight: 'bold', tabs: [], activeTabId: null }
      useWorkspaceStore.getState().importWorkspace(ws)
      const result = useWorkspaceStore.getState().workspaces[0]
      expect(result.icon).toBe('Code')
      expect(result.iconWeight).toBe('bold')
    })

    it('deduplicates by id — importing same id twice is a no-op', () => {
      const ws: Workspace = { id: 'dedup-1', name: 'First', tabs: [], activeTabId: null }
      useWorkspaceStore.getState().importWorkspace(ws)
      useWorkspaceStore.getState().importWorkspace({ ...ws, name: 'Duplicate' })
      expect(useWorkspaceStore.getState().workspaces).toHaveLength(1)
      expect(useWorkspaceStore.getState().workspaces[0].name).toBe('First')
    })
  })

  // === setModuleConfig ===

  // === reorderWorkspaceTabs stale guard ===

  describe('reorderWorkspaceTabs — stale guard', () => {
    it('preserves tabs missing from stale newOrder (concurrent insert safety)', () => {
      useWorkspaceStore.setState({
        workspaces: [
          { id: 'w1', name: 'W1', tabs: ['t1', 't2', 't3'], activeTabId: null },
        ],
        activeWorkspaceId: 'w1',
      })
      // Caller captures stale snapshot ['t1', 't2'] (t3 was inserted concurrently).
      useWorkspaceStore.getState().reorderWorkspaceTabs('w1', ['t2', 't1'])
      const ws = useWorkspaceStore.getState().workspaces[0]
      // Missing tabs appended at end; reordered subset at front.
      expect(ws.tabs).toEqual(['t2', 't1', 't3'])
    })

    it('drops phantom ids not present in current ws.tabs', () => {
      useWorkspaceStore.setState({
        workspaces: [
          { id: 'w1', name: 'W1', tabs: ['t1', 't2'], activeTabId: null },
        ],
        activeWorkspaceId: 'w1',
      })
      useWorkspaceStore.getState().reorderWorkspaceTabs('w1', ['t2', 'phantom', 't1'])
      expect(useWorkspaceStore.getState().workspaces[0].tabs).toEqual(['t2', 't1'])
    })

    it('deduplicates repeated ids without losing tabs', () => {
      useWorkspaceStore.setState({
        workspaces: [
          { id: 'w1', name: 'W1', tabs: ['t1', 't2'], activeTabId: null },
        ],
        activeWorkspaceId: 'w1',
      })
      useWorkspaceStore.getState().reorderWorkspaceTabs('w1', ['t1', 't1'])
      expect(useWorkspaceStore.getState().workspaces[0].tabs).toEqual(['t1', 't2'])
    })
  })

  describe('setModuleConfig', () => {
    it('sets a module config value on a workspace', () => {
      const { workspaces } = useWorkspaceStore.getState()
      const wsId = workspaces[0]?.id
      if (!wsId) {
        useWorkspaceStore.getState().addWorkspace('test-ws')
      }
      const ws0 = useWorkspaceStore.getState().workspaces[0]
      useWorkspaceStore.getState().setModuleConfig(ws0.id, 'files', 'projectPath', '/home/user/project')
      const updated = useWorkspaceStore.getState().workspaces.find((w) => w.id === ws0.id)!
      expect(updated.moduleConfig?.files?.projectPath).toBe('/home/user/project')
    })

    it('preserves existing config when setting a new key', () => {
      const { workspaces } = useWorkspaceStore.getState()
      if (!workspaces[0]) {
        useWorkspaceStore.getState().addWorkspace('test-ws')
      }
      const ws0 = useWorkspaceStore.getState().workspaces[0]
      useWorkspaceStore.getState().setModuleConfig(ws0.id, 'files', 'projectPath', '/path1')
      useWorkspaceStore.getState().setModuleConfig(ws0.id, 'files', 'showHidden', true)
      const updated = useWorkspaceStore.getState().workspaces.find((w) => w.id === ws0.id)!
      expect(updated.moduleConfig?.files?.projectPath).toBe('/path1')
      expect(updated.moduleConfig?.files?.showHidden).toBe(true)
    })
  })
})
