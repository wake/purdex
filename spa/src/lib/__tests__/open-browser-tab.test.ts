import { describe, it, expect, beforeEach } from 'vitest'
import { openBrowserTab } from '../open-browser-tab'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { createTab } from '../../types/tab'

function addTerminalTab(id?: string) {
  const tab = createTab({ kind: 'tmux-session', hostId: 'h', sessionCode: 's', mode: 'terminal', cachedName: '', tmuxInstance: '' })
  if (id) (tab as { id: string }).id = id
  useTabStore.getState().addTab(tab)
  return tab
}

function addBrowserTab(id?: string, url = 'https://example.com') {
  const tab = createTab({ kind: 'browser', url })
  if (id) (tab as { id: string }).id = id
  useTabStore.getState().addTab(tab)
  return tab
}

describe('openBrowserTab — workspace tab insertion order', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.getState().reset()
  })

  it('inserts after nearest browser tab to the right in workspace', () => {
    // Setup: [T1*] [T2] [B1] [T3] in workspace
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    const t1 = addTerminalTab('t1')
    const t2 = addTerminalTab('t2')
    const b1 = addBrowserTab('b1')
    const t3 = addTerminalTab('t3')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, t1.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, t2.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, b1.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, t3.id)
    useTabStore.getState().setActiveTab(t1.id)

    openBrowserTab('https://new.com')

    const wsState = useWorkspaceStore.getState().workspaces[0]
    const newTabId = wsState.tabs.find((id) => id !== t1.id && id !== t2.id && id !== b1.id && id !== t3.id)!
    const b1Idx = wsState.tabs.indexOf(b1.id)
    const newIdx = wsState.tabs.indexOf(newTabId)
    // New tab should be right after B1
    expect(newIdx).toBe(b1Idx + 1)
  })

  it('inserts after active tab when no browser tab to the right', () => {
    // Setup: [B1] [T1*] [T2] in workspace
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    const b1 = addBrowserTab('b1')
    const t1 = addTerminalTab('t1')
    const t2 = addTerminalTab('t2')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, b1.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, t1.id)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, t2.id)
    useTabStore.getState().setActiveTab(t1.id)

    openBrowserTab('https://new.com')

    const wsState = useWorkspaceStore.getState().workspaces[0]
    const newTabId = wsState.tabs.find((id) => id !== b1.id && id !== t1.id && id !== t2.id)!
    const t1Idx = wsState.tabs.indexOf(t1.id)
    const newIdx = wsState.tabs.indexOf(newTabId)
    expect(newIdx).toBe(t1Idx + 1)
  })

  it('appends when no active tab', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    const t1 = addTerminalTab('t1')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, t1.id)
    // activeTabId is null

    openBrowserTab('https://new.com')

    const wsState = useWorkspaceStore.getState().workspaces[0]
    expect(wsState.tabs).toHaveLength(2)
    // New tab should be at the end
    expect(wsState.tabs[1]).not.toBe(t1.id)
  })

  it('sets new tab as workspace active tab', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    const t1 = addTerminalTab('t1')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, t1.id)
    useTabStore.getState().setActiveTab(t1.id)

    openBrowserTab('https://new.com')

    const wsState = useWorkspaceStore.getState().workspaces[0]
    const newTabId = wsState.tabs.find((id) => id !== t1.id)!
    expect(wsState.activeTabId).toBe(newTabId)
  })
})

describe('openBrowserTab — global tabOrder insertion (no workspace)', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.getState().reset()
  })

  it('inserts after nearest browser tab to the right in tabOrder', () => {
    // Setup: [T1*] [T2] [B1] [T3], no workspace
    const t1 = addTerminalTab('t1')
    const t2 = addTerminalTab('t2')
    const b1 = addBrowserTab('b1')
    const t3 = addTerminalTab('t3')
    useTabStore.getState().setActiveTab(t1.id)

    openBrowserTab('https://new.com')

    const tabOrder = useTabStore.getState().tabOrder
    const newTabId = tabOrder.find((id) => id !== t1.id && id !== t2.id && id !== b1.id && id !== t3.id)!
    const b1Idx = tabOrder.indexOf(b1.id)
    const newIdx = tabOrder.indexOf(newTabId)
    expect(newIdx).toBe(b1Idx + 1)
  })
})
