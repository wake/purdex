// spa/src/lib/host-lifecycle.test.ts — Tests for host delete cascade and session-closed detection
import { describe, it, expect, beforeEach } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore, type NormalizedEvent } from '../stores/useAgentStore'
import { useStreamStore } from '../stores/useStreamStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { useUndoToast } from '../stores/useUndoToast'
import { createTab } from '../types/tab'
import { getPrimaryPane, scanPaneTree } from './pane-tree'
import { deleteHostCascade } from './host-lifecycle'
import type { Tab } from '../types/tab'
import type { StreamMessage } from './stream-ws'
import type { Session } from './host-api'

function makeSession(code: string, name: string = code): Session {
  return { code, name, mode: 'terminal', cwd: '~', cc_session_id: '', cc_model: '', has_relay: false }
}

const HOST_A = 'host-a'
const HOST_B = 'host-b'

function makeSessionTab(hostId: string, code: string, mode: 'terminal' | 'stream' = 'terminal'): Tab {
  return createTab({ kind: 'tmux-session', hostId, sessionCode: code, mode, cachedName: '', tmuxInstance: '' })
}

function resetAllStores() {
  useHostStore.setState({
    hosts: {
      [HOST_A]: { id: HOST_A, name: 'Host A', ip: '1.2.3.4', port: 7860, order: 0 },
      [HOST_B]: { id: HOST_B, name: 'Host B', ip: '5.6.7.8', port: 7860, order: 1 },
    },
    hostOrder: [HOST_A, HOST_B],
    activeHostId: HOST_A,
    runtime: {},
  })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  useAgentStore.setState({ lastEvents: {}, statuses: {}, unread: {}, subagents: {}, agentTypes: {}, models: {} })
  useStreamStore.setState({ sessions: {}, relayStatus: {}, handoffProgress: {} })
  useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
  useWorkspaceStore.getState().reset()
  useUndoToast.setState({ toast: null })
}

describe('host delete cascade', () => {
  beforeEach(resetAllStores)

  it('closeTabs=true closes matching tabs', () => {
    const tab1 = makeSessionTab(HOST_A, 'dev001')
    const tab2 = makeSessionTab(HOST_B, 'dev002')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)

    deleteHostCascade(HOST_A, true)

    expect(useTabStore.getState().tabs[tab1.id]).toBeUndefined()
    expect(useTabStore.getState().tabs[tab2.id]).toBeDefined()
  })

  it('closeTabs=false marks tabs as terminated', () => {
    const tab = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(tab)

    deleteHostCascade(HOST_A, false)

    const content = getPrimaryPane(useTabStore.getState().tabs[tab.id].layout).content
    expect(content.kind).toBe('tmux-session')
    if (content.kind === 'tmux-session') {
      expect(content.terminated).toBe('host-removed')
    }
  })

  it('cascade cleans AgentStore entries', () => {
    const event: NormalizedEvent = {
      agent_type: 'cc',
      status: 'idle',
      raw_event_name: 'Stop',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleNormalizedEvent(HOST_A, 'dev001', event)
    expect(useAgentStore.getState().statuses[`${HOST_A}:dev001`]).toBe('idle')

    deleteHostCascade(HOST_A, true)

    expect(useAgentStore.getState().lastEvents[`${HOST_A}:dev001`]).toBeUndefined()
    expect(useAgentStore.getState().statuses[`${HOST_A}:dev001`]).toBeUndefined()
  })

  it('cascade cleans StreamStore entries', () => {
    useStreamStore.getState().addMessage(HOST_A, 'dev001', { type: 'assistant' } as StreamMessage)
    expect(useStreamStore.getState().sessions[`${HOST_A}:dev001`]).toBeDefined()

    deleteHostCascade(HOST_A, true)

    expect(useStreamStore.getState().sessions[`${HOST_A}:dev001`]).toBeUndefined()
  })

  it('cascade cleans SessionStore entries', () => {
    const sessions: Session[] = [makeSession('dev001', 'Dev')]
    useSessionStore.getState().replaceHost(HOST_A, sessions)
    expect(useSessionStore.getState().sessions[HOST_A]).toBeDefined()

    deleteHostCascade(HOST_A, true)

    expect(useSessionStore.getState().sessions[HOST_A]).toBeUndefined()
  })

  it('undo restores host at original position', () => {
    const restore = deleteHostCascade(HOST_A, true)
    expect(useHostStore.getState().hostOrder).toEqual([HOST_B])

    restore()
    expect(useHostStore.getState().hostOrder).toEqual([HOST_A, HOST_B])
    expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
  })

  it('undo restores sessions', () => {
    const sessions: Session[] = [makeSession('dev001', 'Dev')]
    useSessionStore.getState().replaceHost(HOST_A, sessions)

    const restore = deleteHostCascade(HOST_A, true)
    expect(useSessionStore.getState().sessions[HOST_A]).toBeUndefined()

    restore()
    expect(useSessionStore.getState().sessions[HOST_A]).toEqual(sessions)
  })

  it('undo restores AgentStore data', () => {
    const event: NormalizedEvent = {
      agent_type: 'cc',
      status: 'running',
      raw_event_name: 'UserPromptSubmit',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleNormalizedEvent(HOST_A, 'dev001', event)

    const restore = deleteHostCascade(HOST_A, true)
    expect(useAgentStore.getState().statuses[`${HOST_A}:dev001`]).toBeUndefined()

    restore()
    expect(useAgentStore.getState().statuses[`${HOST_A}:dev001`]).toBe('running')
    expect(useAgentStore.getState().lastEvents[`${HOST_A}:dev001`]).toBeDefined()
  })

  it('undo restores AgentStore models', () => {
    // Seed a model entry via handleNormalizedEvent with model field
    const event: NormalizedEvent = {
      agent_type: 'cc',
      status: 'running',
      model: 'claude-sonnet-4-20250514',
      raw_event_name: 'UserPromptSubmit',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleNormalizedEvent(HOST_A, 'dev001', event)
    expect(useAgentStore.getState().models[`${HOST_A}:dev001`]).toBe('claude-sonnet-4-20250514')

    const restore = deleteHostCascade(HOST_A, true)
    expect(useAgentStore.getState().models[`${HOST_A}:dev001`]).toBeUndefined()

    restore()
    expect(useAgentStore.getState().models[`${HOST_A}:dev001`]).toBe('claude-sonnet-4-20250514')
  })

  it('undo restores StreamStore data', () => {
    const msg = { type: 'assistant' } as StreamMessage
    useStreamStore.getState().addMessage(HOST_A, 'dev001', msg)

    const restore = deleteHostCascade(HOST_A, true)
    expect(useStreamStore.getState().sessions[`${HOST_A}:dev001`]).toBeUndefined()

    restore()
    const restored = useStreamStore.getState().sessions[`${HOST_A}:dev001`]
    expect(restored).toBeDefined()
    expect(restored.messages).toHaveLength(1)
    expect(restored.conn).toBeNull()
  })

  it('undo restores closed tabs (closeTabs=true)', () => {
    const tab = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(tab)

    const restore = deleteHostCascade(HOST_A, true)
    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()

    restore()
    expect(useTabStore.getState().tabs[tab.id]).toBeDefined()
  })

  it('undo clears terminated marking (closeTabs=false)', () => {
    const tab = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(tab)

    const restore = deleteHostCascade(HOST_A, false)
    const terminated = getPrimaryPane(useTabStore.getState().tabs[tab.id].layout).content
    if (terminated.kind === 'tmux-session') {
      expect(terminated.terminated).toBe('host-removed')
    }

    restore()
    const restored = getPrimaryPane(useTabStore.getState().tabs[tab.id].layout).content
    if (restored.kind === 'tmux-session') {
      expect(restored.terminated).toBeUndefined()
    }
  })

  it('does not affect other hosts during cascade', () => {
    const tabB = makeSessionTab(HOST_B, 'stg001')
    useTabStore.getState().addTab(tabB)
    const eventB: NormalizedEvent = {
      agent_type: 'cc',
      status: 'running',
      raw_event_name: 'UserPromptSubmit',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleNormalizedEvent(HOST_B, 'stg001', eventB)
    useStreamStore.getState().addMessage(HOST_B, 'stg001', { type: 'user' } as StreamMessage)

    deleteHostCascade(HOST_A, true)

    // HOST_B data should be untouched
    expect(useTabStore.getState().tabs[tabB.id]).toBeDefined()
    expect(useAgentStore.getState().statuses[`${HOST_B}:stg001`]).toBe('running')
    expect(useStreamStore.getState().sessions[`${HOST_B}:stg001`]).toBeDefined()
  })

  it('cascade (closeTabs=true) does not record to history store', () => {
    const tab = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(tab)

    deleteHostCascade(HOST_A, true)

    expect(useHistoryStore.getState().closedTabs).toHaveLength(0)
  })

  it('undo restores workspace membership (closeTabs=true)', () => {
    const ws = useWorkspaceStore.getState().addWorkspace('Dev WS')
    const tab = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(tab)
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, tab.id)
    expect(useWorkspaceStore.getState().findWorkspaceByTab(tab.id)).not.toBeNull()

    const restore = deleteHostCascade(HOST_A, true)
    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
    expect(useWorkspaceStore.getState().findWorkspaceByTab(tab.id)).toBeNull()

    restore()
    expect(useTabStore.getState().tabs[tab.id]).toBeDefined()
    expect(useWorkspaceStore.getState().findWorkspaceByTab(tab.id)?.id).toBe(ws.id)
  })
})

describe('session-closed detection', () => {
  beforeEach(resetAllStores)

  it('marks tabs as terminated when sessions disappear', () => {
    const tab = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(tab)

    // Simulate session-closed detection (from useMultiHostEventWs)
    const newSessions: Session[] = [] // dev001 no longer exists
    const newCodes = new Set(newSessions.map((s) => s.code))
    const closedCodes = new Set<string>()

    for (const t of Object.values(useTabStore.getState().tabs)) {
      scanPaneTree(t.layout, (pane) => {
        const c = pane.content
        if (c.kind === 'tmux-session' && c.hostId === HOST_A && !c.terminated && !newCodes.has(c.sessionCode)) {
          closedCodes.add(c.sessionCode)
        }
      })
    }

    for (const code of closedCodes) {
      useTabStore.getState().markTerminated(HOST_A, code, 'session-closed')
    }

    const content = getPrimaryPane(useTabStore.getState().tabs[tab.id].layout).content
    expect(content.kind).toBe('tmux-session')
    if (content.kind === 'tmux-session') {
      expect(content.terminated).toBe('session-closed')
    }
  })

  it('does not double-mark already-terminated tabs', () => {
    const tab = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(tab)
    useTabStore.getState().markTerminated(HOST_A, 'dev001', 'session-closed')
    const before = useTabStore.getState().tabs[tab.id]

    // Run detection again
    const newCodes = new Set<string>()
    const closedCodes = new Set<string>()
    for (const t of Object.values(useTabStore.getState().tabs)) {
      scanPaneTree(t.layout, (pane) => {
        const c = pane.content
        if (c.kind === 'tmux-session' && c.hostId === HOST_A && !c.terminated && !newCodes.has(c.sessionCode)) {
          closedCodes.add(c.sessionCode)
        }
      })
    }
    // Already terminated — should not be in closedCodes
    expect(closedCodes.size).toBe(0)

    // Verify tab unchanged
    const after = useTabStore.getState().tabs[tab.id]
    expect(after).toBe(before)
  })

  it('only marks sessions not in the new list', () => {
    const tab1 = makeSessionTab(HOST_A, 'dev001')
    const tab2 = makeSessionTab(HOST_A, 'dev002')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)

    // dev001 still exists, dev002 is gone
    const newSessions: Session[] = [makeSession('dev001', 'Dev')]
    const newCodes = new Set(newSessions.map((s) => s.code))
    const closedCodes = new Set<string>()

    for (const t of Object.values(useTabStore.getState().tabs)) {
      scanPaneTree(t.layout, (pane) => {
        const c = pane.content
        if (c.kind === 'tmux-session' && c.hostId === HOST_A && !c.terminated && !newCodes.has(c.sessionCode)) {
          closedCodes.add(c.sessionCode)
        }
      })
    }

    expect(closedCodes.has('dev002')).toBe(true)
    expect(closedCodes.has('dev001')).toBe(false)

    for (const code of closedCodes) {
      useTabStore.getState().markTerminated(HOST_A, code, 'session-closed')
    }

    // dev001 should not be terminated
    const c1 = getPrimaryPane(useTabStore.getState().tabs[tab1.id].layout).content
    if (c1.kind === 'tmux-session') {
      expect(c1.terminated).toBeUndefined()
    }

    // dev002 should be terminated
    const c2 = getPrimaryPane(useTabStore.getState().tabs[tab2.id].layout).content
    if (c2.kind === 'tmux-session') {
      expect(c2.terminated).toBe('session-closed')
    }
  })
})
