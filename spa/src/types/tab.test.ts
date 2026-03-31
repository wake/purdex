import { describe, it, expect } from 'vitest'
import { createTab, createWorkspace, isStandaloneTab } from './tab'

describe('createTab', () => {
  it('creates a tab with session content', () => {
    const tab = createTab({ kind: 'session', hostId: 'test-host', sessionCode: 'abc123', mode: 'terminal' })
    expect(tab.id).toMatch(/^[0-9a-z]{6}$/)
    expect(tab.pinned).toBe(false)
    expect(tab.locked).toBe(false)
    expect(typeof tab.createdAt).toBe('number')
    expect(tab.layout.type).toBe('leaf')
    if (tab.layout.type === 'leaf') {
      expect(tab.layout.pane.id).toMatch(/^[0-9a-z]{6}$/)
      expect(tab.layout.pane.content).toEqual({ kind: 'session', hostId: 'test-host', sessionCode: 'abc123', mode: 'terminal' })
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
    expect(ws.color).toBeTruthy()
    expect(ws.tabs).toEqual([])
    expect(ws.activeTabId).toBeNull()
  })

  it('creates a workspace with a custom color', () => {
    const ws = createWorkspace('Custom', '#ff0000')
    expect(ws.color).toBe('#ff0000')
  })
})

describe('isStandaloneTab', () => {
  it('returns true when tab is not in any workspace', () => {
    const tab = createTab({ kind: 'session', hostId: 'test-host', sessionCode: 'abc', mode: 'terminal' })
    const workspaces = [createWorkspace('WS1')]
    expect(isStandaloneTab(tab.id, workspaces)).toBe(true)
  })

  it('returns false when tab is in a workspace', () => {
    const tab = createTab({ kind: 'session', hostId: 'test-host', sessionCode: 'xyz', mode: 'stream' })
    const ws = createWorkspace('WS1')
    ws.tabs = [tab.id]
    expect(isStandaloneTab(tab.id, [ws])).toBe(false)
  })
})
