// spa/src/lib/open-session-tab.test.ts — the two tab paths the session list, the notification click and the approval
// switch (U22, lead-team plan v3 P9b-1) share: open a tab on a tmux session the way Hosts › Sessions does, and activate
// a pane that already shows it the way the notification click does. Real stores throughout.
import { describe, it, expect, beforeEach } from 'vitest'
import { activateTabPane, openSessionTab } from './open-session-tab'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { useHostStore } from '../stores/useHostStore'
import { usePaneFocusStore } from '../stores/usePaneFocusStore'
import type { Session } from './host-api'
import type { PaneContent, PaneLayout, Tab } from '../types/tab'

const HOST = 'h1'
const session = (over: Partial<Session> = {}): Session => ({ code: 'abc', name: 'dev', cwd: '/tmp', mode: 'terminal', tmux_instance: '222:2000', ...over })

const leaf = (id: string, content: PaneContent): PaneLayout => ({ type: 'leaf', pane: { id, content } })
const tab = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 0, layout })
const blankTab = (id: string): Tab => tab(id, leaf(`p-${id}`, { kind: 'new-tab' }))
const openTabs = (list: Tab[], activeTabId: string | null) =>
  useTabStore.setState({ tabs: Object.fromEntries(list.map((t) => [t.id, t])), tabOrder: list.map((t) => t.id), activeTabId })

beforeEach(() => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.getState().reset()
  useHostStore.setState({ hostOrder: [HOST] })
  useShownHostsStore.setState({ ids: [HOST] })
  usePaneFocusStore.setState({ recent: {}, focusRequest: null })
})

describe('openSessionTab (Hosts › Sessions "open", moved)', () => {
  it('adds one tmux-session tab with the session\'s code, name and tmux_instance, inserts it into the active workspace and activates it', () => {
    openTabs([blankTab('tO')], 'tO')
    const ws = useWorkspaceStore.getState().addWorkspace('W')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, 'tO')
    useWorkspaceStore.getState().setActiveWorkspace(ws.id)

    const tabId = openSessionTab(HOST, session())

    expect(tabId).not.toBeNull()
    const { tabs, tabOrder, activeTabId } = useTabStore.getState()
    expect(tabOrder).toEqual(['tO', tabId])
    expect(activeTabId).toBe(tabId)
    expect(tabs[tabId!].layout).toMatchObject({
      type: 'leaf',
      pane: { content: { kind: 'tmux-session', hostId: HOST, sessionCode: 'abc', mode: 'terminal', cachedName: 'dev', tmuxInstance: '222:2000' } },
    })
    const after = useWorkspaceStore.getState().workspaces.find((w) => w.id === ws.id)
    expect(after?.tabs).toEqual(['tO', tabId])
  })

  it('a session with no tmux_instance opens with an empty generation (unknown, never a match)', () => {
    const tabId = openSessionTab(HOST, session({ tmux_instance: undefined }))
    const pane = useTabStore.getState().tabs[tabId!].layout
    expect(pane).toMatchObject({ type: 'leaf', pane: { content: { tmuxInstance: '' } } })
  })

  it('opening the same session twice adds two tabs (tmux-session is never a singleton)', () => {
    const first = openSessionTab(HOST, session())
    const second = openSessionTab(HOST, session())
    expect(first).not.toBeNull()
    expect(second).not.toBeNull()
    expect(second).not.toBe(first)
    expect(useTabStore.getState().tabOrder).toEqual([first, second])
    expect(useTabStore.getState().activeTabId).toBe(second)
  })

  it('a host hidden in this workbench: null, no tab, no workspace touched', () => {
    useShownHostsStore.setState({ ids: [] })
    openTabs([blankTab('tO')], 'tO')
    const before = useWorkspaceStore.getState().workspaces

    expect(openSessionTab(HOST, session())).toBeNull()

    expect(useTabStore.getState().tabOrder).toEqual(['tO'])
    expect(useTabStore.getState().activeTabId).toBe('tO')
    expect(useWorkspaceStore.getState().workspaces).toBe(before)
  })
})

describe('activateTabPane (the notification click\'s existing-tab block, moved)', () => {
  const tmux: PaneContent = { kind: 'tmux-session', hostId: HOST, sessionCode: 'abc', mode: 'terminal', cachedName: 'dev', tmuxInstance: '' }

  it('activates the tab, makes the pane its focus target with a one-shot request, and shows the tab\'s workspace', () => {
    openTabs([tab('tS', leaf('pS', tmux)), blankTab('tO')], 'tO')
    const wsS = useWorkspaceStore.getState().addWorkspace('S')
    const wsO = useWorkspaceStore.getState().addWorkspace('O')
    useWorkspaceStore.getState().addTabToWorkspace(wsS.id, 'tS')
    useWorkspaceStore.getState().addTabToWorkspace(wsO.id, 'tO')
    useWorkspaceStore.getState().setActiveWorkspace(wsO.id)

    activateTabPane('tS', 'pS')

    expect(useTabStore.getState().activeTabId).toBe('tS')
    expect(usePaneFocusStore.getState().recent.tS?.[0]).toBe('pS')
    expect(usePaneFocusStore.getState().focusRequest).toMatchObject({ tabId: 'tS', paneId: 'pS', taken: false })
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(wsS.id)
    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === wsS.id)?.activeTabId).toBe('tS')
    expect(useTabStore.getState().tabOrder).toEqual(['tS', 'tO'])
  })

  it('a tab no workspace holds yet: the tab is activated, the workspace on screen stays', () => {
    openTabs([tab('tS', leaf('pS', tmux)), blankTab('tO')], 'tO')
    const wsO = useWorkspaceStore.getState().addWorkspace('O')
    useWorkspaceStore.getState().addTabToWorkspace(wsO.id, 'tO')
    useWorkspaceStore.getState().setActiveWorkspace(wsO.id)

    activateTabPane('tS', 'pS')

    expect(useTabStore.getState().activeTabId).toBe('tS')
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(wsO.id)
    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === wsO.id)?.activeTabId).not.toBe('tS')
  })
})
