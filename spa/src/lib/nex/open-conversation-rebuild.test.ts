// spa/src/lib/nex/open-conversation-rebuild.test.ts — 重建… on an 已退出 row opens the closed-terminal
// rebuild pane for the conversation (conversation entity spec §13.4, R-4-3): seeded content, the
// generated name and the host's generation, de-duplication over every leaf, and the workspace it lands in.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useTabStore } from '../../stores/useTabStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { emptyHostConfigEntry, useHostConfigStore } from '../../stores/useHostConfigStore'
import { useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { createTab, type PaneContent, type Tab } from '../../types/tab'
import type { Session } from '../host-api'
import type { HostProject } from '../host-config-api'
import type { ConversationRow } from './conversations-api'
import { hostHomeFor } from './handoff'
import { conversationRebuildContent, openConversationRebuild } from './open-conversation-rebuild'

vi.mock('./handoff', async (o) => ({ ...(await o<typeof import('./handoff')>()), hostHomeFor: vi.fn() }))

const H = 'host-a'
const S = 'aaaaaaaa-1111-2222-3333-444444444444'
const S2 = 'bbbbbbbb-1111-2222-3333-444444444444'
const NOW = 1_800_000_000_000
const GEN = '111:1000'
const PROJECTS: HostProject[] = [{ id: 'p1', name: 'Proj', slug: 'proj', path: '~/proj' }]

const row = (over: Partial<ConversationRow> = {}): ConversationRow => ({
  session_id: S, title: 'Fix the login bug', title_source: 'ai', cwd: '/home/u/proj/sub', cwd_exists: true,
  last_activity_at: NOW - 30_000, last_in: 'terminal', ...over,
})
const live = (code: string, name: string, tmux_instance = GEN): Session => ({ code, name, cwd: '', mode: 'terminal', tmux_instance })

function settleWorld(): void {
  useTabStore.setState({ worldId: 'master', worldEpoch: 0 })
  useWorkspaceStore.setState({ worldId: 'master', worldEpoch: 0 })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0 })
}

function addTab(tab: Tab, wsId?: string): void {
  useTabStore.getState().addTab(tab)
  if (wsId) useWorkspaceStore.getState().addTabToWorkspace(wsId, tab.id)
}

function conversationTabs(): Tab[] {
  return Object.values(useTabStore.getState().tabs).filter((t) => t.layout.type === 'leaf'
    && t.layout.pane.content.kind === 'tmux-session' && t.layout.pane.content.terminated === 'conversation-ended')
}

function leafContent(tabId: string): PaneContent {
  const layout = useTabStore.getState().tabs[tabId].layout
  if (layout.type !== 'leaf') throw new Error('fixture: a leaf')
  return layout.pane.content
}

beforeEach(() => {
  localStorage.clear()
  vi.spyOn(Date, 'now').mockReturnValue(NOW)
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useWorkspaceStore.getState().reset()
  settleWorld()
  useSessionStore.setState({ sessions: { [H]: [live('c1', 'proj-1'), live('c2', 'other')] } })
  useHostConfigStore.setState({ byHost: { [H]: { ...emptyHostConfigEntry('ready'), projects: PROJECTS } } })
  vi.mocked(hostHomeFor).mockReset().mockResolvedValue('/home/u')
})

afterEach(() => {
  settleWorld()
  vi.restoreAllMocks()
})

describe('openConversationRebuild — a new tab', () => {
  it('opens a closed-terminal pane seeded for S, named as take-to-terminal names one, on the host generation', async () => {
    const w1 = useWorkspaceStore.getState().addWorkspace('W1')
    useWorkspaceStore.getState().setActiveWorkspace(w1.id)

    const id = await openConversationRebuild(H, row())

    expect(leafContent(id)).toEqual({
      kind: 'tmux-session', hostId: H, sessionCode: '', mode: 'terminal', cachedName: 'proj-2', tmuxInstance: GEN,
      terminated: 'conversation-ended',
      rebuild: {
        sessionName: 'proj-2', tmuxInstance: GEN, cwd: '/home/u/proj/sub', cwdSource: 'user',
        agent: { type: 'cc', sessionId: S, updatedAt: NOW - 30_000 },
        capturedAt: NOW,
      },
      conversation: { sessionId: S, title: 'Fix the login bug', lastIn: 'terminal', lastWriteAt: NOW - 30_000 },
    })
    // The `~/proj` project only resolves through the host's home: the take-to-terminal helper was asked.
    expect(hostHomeFor).toHaveBeenCalledWith(H, PROJECTS)
    expect(useTabStore.getState().activeTabId).toBe(id)
    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === w1.id)!.tabs).toEqual([id])
    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === w1.id)!.activeTabId).toBe(id)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(w1.id)
  })

  it('carries the generation as unknown when the SPA has no sessions payload for the host', async () => {
    useSessionStore.setState({ sessions: {} })
    const id = await openConversationRebuild(H, row())
    const c = leafContent(id)
    if (c.kind !== 'tmux-session') throw new Error('a terminal pane')
    expect(c.tmuxInstance).toBe('')
    expect(c.rebuild?.tmuxInstance).toBe('')
    expect(c.cachedName).toBe('proj-1')
  })

  it('a payload whose sessions disagree on the generation is no evidence: unknown', async () => {
    useSessionStore.setState({ sessions: { [H]: [live('c1', 'a', '111:1000'), live('c2', 'b', '222:2000')] } })
    const c = leafContent(await openConversationRebuild(H, row()))
    expect(c.kind === 'tmux-session' && c.tmuxInstance).toBe('')
  })

  it('preserves the last mode and lowercases the session id', async () => {
    const c = leafContent(await openConversationRebuild(H, row({ session_id: S.toUpperCase(), last_in: 'worker' })))
    if (c.kind !== 'tmux-session') throw new Error('a terminal pane')
    expect(c.conversation).toEqual({ sessionId: S, title: 'Fix the login bug', lastIn: 'worker', lastWriteAt: NOW - 30_000 })
    expect(c.rebuild?.agent?.sessionId).toBe(S)
  })
})

describe('openConversationRebuild — one tab per (host, S)', () => {
  it('a second open selects the first tab, brings its workspace on screen, and adds none', async () => {
    const store = useWorkspaceStore.getState()
    const w1 = store.addWorkspace('W1')
    const w2 = store.addWorkspace('W2')
    store.setActiveWorkspace(w2.id)
    const first = await openConversationRebuild(H, row())
    const other = createTab({ kind: 'new-tab' })
    addTab(other, w1.id)
    useTabStore.getState().setActiveTab(other.id)
    useWorkspaceStore.getState().setActiveWorkspace(w1.id)

    const second = await openConversationRebuild(H, row({ session_id: S.toUpperCase() }))

    expect(second).toBe(first)
    expect(conversationTabs()).toHaveLength(1)
    expect(useTabStore.getState().activeTabId).toBe(first)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(w2.id)
    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === w2.id)!.tabs).toEqual([first])
    // Selected where it is: not asked for a name again.
    expect(hostHomeFor).toHaveBeenCalledTimes(1)
  })

  it('finds the pane when it is a split leaf, not the primary pane', async () => {
    const w1 = useWorkspaceStore.getState().addWorkspace('W1')
    const pane = conversationRebuildContent(H, row(), 'proj-2', GEN, 1)
    const split: Tab = {
      id: 'split-tab', pinned: false, locked: false, createdAt: 1,
      layout: {
        type: 'split', id: 's1', direction: 'h', sizes: [50, 50],
        children: [
          { type: 'leaf', pane: { id: 'primary', content: { kind: 'new-tab' } } },
          { type: 'leaf', pane: { id: 'conv', content: pane } },
        ],
      },
    }
    addTab(split, w1.id)
    useTabStore.getState().setActiveTab(null)

    const id = await openConversationRebuild(H, row())

    expect(id).toBe('split-tab')
    expect(Object.keys(useTabStore.getState().tabs)).toEqual(['split-tab'])
    expect(useTabStore.getState().activeTabId).toBe('split-tab')
  })

  it('a different S opens a second tab', async () => {
    const a = await openConversationRebuild(H, row())
    const b = await openConversationRebuild(H, row({ session_id: S2 }))
    expect(b).not.toBe(a)
    expect(conversationTabs()).toHaveLength(2)
  })

  it('the same S on another host opens a second tab', async () => {
    const a = await openConversationRebuild(H, row())
    const b = await openConversationRebuild('host-b', row())
    expect(b).not.toBe(a)
    expect(conversationTabs()).toHaveLength(2)
  })

  it('a pane for S that is not a conversation-ended pane does not count', async () => {
    const closed = createTab({
      kind: 'tmux-session', hostId: H, sessionCode: 'c9', mode: 'terminal', cachedName: 'x', tmuxInstance: GEN,
      terminated: 'session-closed',
      rebuild: { sessionName: 'x', tmuxInstance: GEN, cwd: '/w', agent: { type: 'cc', sessionId: S, updatedAt: 1 }, capturedAt: 1 },
    })
    addTab(closed)
    const id = await openConversationRebuild(H, row())
    expect(id).not.toBe(closed.id)
    expect(conversationTabs()).toHaveLength(1)
  })

  // R-4-18: the notice warns about the row as it is when the user acts, so a reopen re-reads it.
  it('a reopen refreshes the pane conversation snapshot from the row, and keeps every other field', async () => {
    const id = await openConversationRebuild(H, row())
    const before = leafContent(id)
    const other = createTab({ kind: 'new-tab' })
    addTab(other)
    useTabStore.getState().setActiveTab(other.id)

    const again = await openConversationRebuild(H, row({ last_activity_at: NOW - 20_000, title: 'Renamed', last_in: 'worker' }))

    expect(again).toBe(id)
    expect(useTabStore.getState().activeTabId).toBe(id)
    expect(leafContent(id)).toEqual({
      ...before,
      conversation: { sessionId: S, title: 'Renamed', lastIn: 'worker', lastWriteAt: NOW - 20_000 },
    })
  })

  it('the refresh lands on the split leaf that holds the pane, nowhere else', async () => {
    const pane = conversationRebuildContent(H, row(), 'proj-2', GEN, 1)
    const split: Tab = {
      id: 'split-tab', pinned: false, locked: false, createdAt: 1,
      layout: {
        type: 'split', id: 's1', direction: 'h', sizes: [50, 50],
        children: [
          { type: 'leaf', pane: { id: 'primary', content: { kind: 'new-tab' } } },
          { type: 'leaf', pane: { id: 'conv', content: pane } },
        ],
      },
    }
    addTab(split)

    await openConversationRebuild(H, row({ last_activity_at: NOW - 5_000 }))

    const layout = useTabStore.getState().tabs['split-tab'].layout
    if (layout.type !== 'split') throw new Error('fixture: a split')
    expect(layout.children[0]).toEqual(split.layout.type === 'split' ? split.layout.children[0] : null)
    const conv = layout.children[1]
    expect(conv.type === 'leaf' && conv.pane.content).toEqual({ ...pane, conversation: { ...pane.conversation, lastWriteAt: NOW - 5_000 } })
  })

  it('two clicks while the name is still being worked out make one tab', async () => {
    let release: (home: string) => void = () => {}
    vi.mocked(hostHomeFor).mockReturnValue(new Promise((r) => { release = r }))
    const a = openConversationRebuild(H, row())
    const b = openConversationRebuild(H, row())
    release('/home/u')
    const [ida, idb] = await Promise.all([a, b])
    expect(idb).toBe(ida)
    expect(conversationTabs()).toHaveLength(1)
  })
})
