import { describe, it, expect } from 'vitest'
import { createTab, createWorkspace, isStandaloneTab } from './tab'
import type { SidebarRegion, WorkspaceSidebarState } from './tab'

describe('createTab', () => {
  it('creates a tab with session content', () => {
    const tab = createTab({ kind: 'tmux-session', hostId: 'test-host', sessionCode: 'abc123', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    expect(tab.id).toMatch(/^[0-9a-z]{6}$/)
    expect(tab.pinned).toBe(false)
    expect(tab.locked).toBe(false)
    expect(typeof tab.createdAt).toBe('number')
    expect(tab.layout.type).toBe('leaf')
    if (tab.layout.type === 'leaf') {
      expect(tab.layout.pane.id).toMatch(/^[0-9a-z]{6}$/)
      expect(tab.layout.pane.content).toEqual({ kind: 'tmux-session', hostId: 'test-host', sessionCode: 'abc123', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    }
  })

  it('creates a tab with dashboard content', () => {
    const tab = createTab({ kind: 'dashboard' })
    expect(tab.id).toMatch(/^[0-9a-z]{6}$/)
    expect(tab.pinned).toBe(false)
    expect(tab.locked).toBe(false)
    expect(typeof tab.createdAt).toBe('number')
    expect(tab.layout.type).toBe('leaf')
    if (tab.layout.type === 'leaf') {
      expect(tab.layout.pane.id).toMatch(/^[0-9a-z]{6}$/)
      expect(tab.layout.pane.content).toEqual({ kind: 'dashboard' })
    }
  })

  it('creates a tab with pinned=true when opt is set', () => {
    const tab = createTab({ kind: 'new-tab' }, { pinned: true })
    expect(tab.pinned).toBe(true)
    expect(tab.locked).toBe(false)
  })
})

describe('createWorkspace', () => {
  it('creates a workspace with defaults', () => {
    const ws = createWorkspace('My Project')
    expect(ws.id).toMatch(/^[0-9a-z]{6}$/)
    expect(ws.name).toBe('My Project')
    expect(ws.tabs).toEqual([])
    expect(ws.activeTabId).toBeNull()
  })
})

describe('SidebarRegion type', () => {
  it('accepts valid region values', () => {
    const regions: SidebarRegion[] = [
      'primary-sidebar',
      'primary-panel',
      'secondary-panel',
      'secondary-sidebar',
    ]
    expect(regions).toHaveLength(4)
  })
})

describe('Workspace.sidebarState', () => {
  it('is optional on Workspace', () => {
    const ws = createWorkspace('test')
    expect(ws.sidebarState).toBeUndefined()
  })

  it('accepts a WorkspaceSidebarState value', () => {
    const state: WorkspaceSidebarState = {
      regions: {
        'primary-sidebar': { width: 240, mode: 'pinned' as const },
      },
    }
    const ws = createWorkspace('test')
    ws.sidebarState = state
    expect(ws.sidebarState).toBe(state)
  })
})

describe('isStandaloneTab', () => {
  it('returns true when tab is not in any workspace', () => {
    const tab = createTab({ kind: 'tmux-session', hostId: 'test-host', sessionCode: 'abc', mode: 'terminal', cachedName: '', tmuxInstance: '' })
    const workspaces = [createWorkspace('WS1')]
    expect(isStandaloneTab(tab.id, workspaces)).toBe(true)
  })

  it('returns false when tab is in a workspace', () => {
    const tab = createTab({ kind: 'tmux-session', hostId: 'test-host', sessionCode: 'xyz', mode: 'stream', cachedName: '', tmuxInstance: '' })
    const ws = createWorkspace('WS1')
    ws.tabs = [tab.id]
    expect(isStandaloneTab(tab.id, [ws])).toBe(false)
  })
})
