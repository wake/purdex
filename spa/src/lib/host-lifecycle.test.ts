// spa/src/lib/host-lifecycle.test.ts — Tests for host delete cascade and session-closed detection
import { describe, it, expect, beforeEach } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore, type NormalizedEvent } from '../stores/useAgentStore'
import { useStreamStore } from '../stores/useStreamStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { useHostSettingsStore } from '../stores/useHostSettingsStore'
import { useWorkspaceSettingsStore } from '../stores/useWorkspaceSettingsStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { useUndoToast } from '../stores/useUndoToast'
import { createTab } from '../types/tab'
import { getPrimaryPane, scanPaneTree } from './pane-tree'
import { deleteHostCascade } from './host-lifecycle'
import { STORAGE_KEYS } from './storage/keys'
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
  localStorage.clear()
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
  useHostSettingsStore.setState({ hosts: {} })
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

  it('cascade clears persisted host settings for the deleted host', () => {
    useHostSettingsStore.getState().set(HOST_A, 'editor', { homePath: '/tmp/a' })
    useHostSettingsStore.getState().set(HOST_B, 'editor', { homePath: '/tmp/b' })

    deleteHostCascade(HOST_A, true)

    expect(useHostSettingsStore.getState().get(HOST_A, 'editor')).toBeUndefined()
    expect(useHostSettingsStore.getState().get(HOST_B, 'editor')).toEqual({ homePath: '/tmp/b' })

    const raw = localStorage.getItem(STORAGE_KEYS.HOST_SETTINGS)
    expect(raw).toBeTruthy()
    const parsed = JSON.parse(raw!)
    expect(parsed.state.hosts[HOST_A]).toBeUndefined()
    expect(parsed.state.hosts[HOST_B].editor).toEqual({ homePath: '/tmp/b' })
  })

  it('undo restores host settings cleared by the cascade', () => {
    useHostSettingsStore.getState().set(HOST_A, 'editor', { homePath: '/tmp/a' })

    const restore = deleteHostCascade(HOST_A, true)
    expect(useHostSettingsStore.getState().get(HOST_A, 'editor')).toBeUndefined()

    restore()
    expect(useHostSettingsStore.getState().get(HOST_A, 'editor')).toEqual({ homePath: '/tmp/a' })
  })

  it('undo skips restore when the same host id was recreated during the undo window', () => {
    // Simulate user writing settings, then deleting the host.
    useHostSettingsStore.getState().set(HOST_A, 'editor', { homePath: '/tmp/old' })

    const restore = deleteHostCascade(HOST_A, true)
    expect(useHostStore.getState().hosts[HOST_A]).toBeUndefined()
    expect(useHostSettingsStore.getState().get(HOST_A, 'editor')).toBeUndefined()

    // Simulate cross-window BroadcastChannel sync or import re-creating a
    // distinct host with the same id during the undo window, and the user
    // writing new settings for that recreated host.
    useHostStore.setState((s) => ({
      hosts: {
        ...s.hosts,
        [HOST_A]: { id: HOST_A, name: 'Host A (recreated)', ip: '9.9.9.9', port: 7860, order: 2 },
      },
      hostOrder: [...s.hostOrder, HOST_A],
    }))
    useHostSettingsStore.getState().set(HOST_A, 'editor', { homePath: '/tmp/new' })

    restore()

    // Stale snapshot settings must NOT overwrite the user's new settings
    expect(useHostSettingsStore.getState().get(HOST_A, 'editor')).toEqual({ homePath: '/tmp/new' })
    // Recreated host entry must survive (not be clobbered by snapshot)
    expect(useHostStore.getState().hosts[HOST_A]?.name).toBe('Host A (recreated)')
    expect(useHostStore.getState().hosts[HOST_A]?.ip).toBe('9.9.9.9')
  })

  it('undo skips session restore when host was recreated during the undo window', () => {
    const sessions: Session[] = [makeSession('dev001', 'Dev')]
    useSessionStore.getState().replaceHost(HOST_A, sessions)

    const restore = deleteHostCascade(HOST_A, true)
    expect(useSessionStore.getState().sessions[HOST_A]).toBeUndefined()

    // Recreate host with same id and a fresh (different) sessions list
    useHostStore.setState((s) => ({
      hosts: {
        ...s.hosts,
        [HOST_A]: { id: HOST_A, name: 'Host A', ip: '1.2.3.4', port: 7860, order: 2 },
      },
      hostOrder: [...s.hostOrder, HOST_A],
    }))
    const freshSessions: Session[] = [makeSession('dev999', 'Fresh')]
    useSessionStore.getState().replaceHost(HOST_A, freshSessions)

    restore()

    // Stale snapshot sessions must NOT overwrite the freshly written list
    expect(useSessionStore.getState().sessions[HOST_A]).toEqual(freshSessions)
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

  it('aborts cascade with no-op undo when host removal would be vetoed (last host)', () => {
    // Leave only one host so useHostStore.removeHost() will no-op.
    useHostStore.setState({
      hosts: { [HOST_A]: { id: HOST_A, name: 'Host A', ip: '1.2.3.4', port: 7860, order: 0 } },
      hostOrder: [HOST_A],
      activeHostId: HOST_A,
      runtime: {},
    })
    useHostSettingsStore.getState().set(HOST_A, 'editor', { homePath: '/tmp/a' })
    const tab = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(tab)

    const restore = deleteHostCascade(HOST_A, true)

    // Cascade must be inert: host entry, settings and tabs are untouched.
    expect(useHostStore.getState().hosts[HOST_A]).toBeDefined()
    expect(useHostSettingsStore.getState().get(HOST_A, 'editor')).toEqual({ homePath: '/tmp/a' })
    expect(useTabStore.getState().tabs[tab.id]).toBeDefined()

    // Undo is a safe no-op.
    expect(() => restore()).not.toThrow()
    expect(useHostSettingsStore.getState().get(HOST_A, 'editor')).toEqual({ homePath: '/tmp/a' })
  })

  it('undo does not restore closed tabs onto a recreated same-id host (closeTabs=true)', () => {
    const tab = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(tab)

    const restore = deleteHostCascade(HOST_A, true)
    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
    expect(useHostStore.getState().hosts[HOST_A]).toBeUndefined()

    // Same-id host recreated during undo window — it is a different entity.
    useHostStore.setState((s) => ({
      hosts: {
        ...s.hosts,
        [HOST_A]: { id: HOST_A, name: 'Host A (new)', ip: '9.9.9.9', port: 7860, order: 2 },
      },
      hostOrder: [...s.hostOrder, HOST_A],
    }))

    restore()

    // Stale tabs with old hostId/sessionCode must not be bound to the new host.
    expect(useTabStore.getState().tabs[tab.id]).toBeUndefined()
  })

  it('undo does not clear terminated markers when host was recreated (closeTabs=false)', () => {
    const tab = makeSessionTab(HOST_A, 'dev001')
    useTabStore.getState().addTab(tab)

    const restore = deleteHostCascade(HOST_A, false)
    const marked = getPrimaryPane(useTabStore.getState().tabs[tab.id].layout).content
    if (marked.kind === 'tmux-session') {
      expect(marked.terminated).toBe('host-removed')
    }

    // Recreate same-id host during undo window.
    useHostStore.setState((s) => ({
      hosts: {
        ...s.hosts,
        [HOST_A]: { id: HOST_A, name: 'Host A (new)', ip: '9.9.9.9', port: 7860, order: 2 },
      },
      hostOrder: [...s.hostOrder, HOST_A],
    }))

    restore()

    // Terminated marking must stay — those panes belonged to the deleted host.
    const after = getPrimaryPane(useTabStore.getState().tabs[tab.id].layout).content
    if (after.kind === 'tmux-session') {
      expect(after.terminated).toBe('host-removed')
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

// ─────────────────────────────────────────────────────────────────────────────
// §3.4 — #541 Cross-store rehydrate order invariants harness
//
// These tests validate that `deleteHostCascade` (and its undo callback) behave
// correctly under all rehydrate-timing combinations:
//   A. Both useHostStore + useHostSettingsStore fully rehydrated
//   B. Only useHostSettingsStore rehydrated (hosts store still empty — e.g. it
//      rehydrated first in a race)
//   C. Only useHostStore rehydrated (hostSettings store not yet rehydrated)
//   + interleaved-write gate, hostWasRecreated, last-host veto, and workspace
//     settings isolation.
//
// ─────────────────────────────────────────────────────────────────────────────

describe('#541 cross-store rehydrate order invariants', () => {
  const hA = 'hA'
  const hB = 'hB'

  // Wipe all singleton stores to a blank canvas before each test.
  //
  // IMPORTANT: We use setState WITHOUT replace-mode (no `true` second arg) for
  // all stores. This keeps Zustand action methods intact in the store state
  // object, which host-lifecycle.ts needs because it snapshots store state at
  // call time (e.g. `const sessionStore = useSessionStore.getState()`) and then
  // calls action methods on the snapshot (e.g. `sessionStore.removeHost(...)`).
  // Replace-mode would wipe those methods, causing "not a function" errors.
  //
  // The data fields we set here ({hosts:{}, ...}) are shallowly merged on top
  // of the current state, overwriting the default mlab host that
  // createDefaultState() bakes in — so we get a clean slate without clobbering
  // the action closures.
  beforeEach(() => {
    localStorage.clear()
    // Merge-mode: set ALL mutable data fields to blank without clobbering
    // action methods (Finding D fix: explicit field-by-field reset prevents
    // cross-test leakage of fields not listed).
    useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {}, activeHostId: null })
    useHostSettingsStore.setState({ hosts: {} })
    useWorkspaceSettingsStore.setState({ workspaces: {} })
    // visitHistory added — codex Finding D flagged it as a cross-test leak vector
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
    // oscTitles + ccStatus added — codex Finding D flagged them as cross-test leak vectors
    useAgentStore.setState({
      lastEvents: {},
      statuses: {},
      unread: {},
      subagents: {},
      agentTypes: {},
      models: {},
      oscTitles: {},
      ccStatus: {},
    })
    useStreamStore.setState({ sessions: {}, relayStatus: {}, handoffProgress: {} })
    useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
    useWorkspaceStore.getState().reset()
    useUndoToast.setState({ toast: null })
  })

  // ── Seed helpers ──────────────────────────────────────────────────────────

  /** Both stores fully rehydrated: hA + hB in hostStore, hA has settings. */
  function seedBothRehydrated() {
    // Merge-mode: preserve action methods in store state
    useHostStore.setState({
      hosts: {
        [hA]: { id: hA, name: 'Host A', ip: '127.0.0.1', port: 7860, order: 0 },
        [hB]: { id: hB, name: 'Host B', ip: '127.0.0.1', port: 7861, order: 1 },
      },
      hostOrder: [hA, hB],
      runtime: {},
      activeHostId: hA,
    })
    useHostSettingsStore.setState({ hosts: { [hA]: { editor: { homePath: '/tmp/a' } } } })
  }

  /** Only hostStore rehydrated; hostSettings store is empty (hasn't rehydrated yet). */
  function seedHostOnly() {
    // Merge-mode: preserve action methods in store state
    useHostStore.setState({
      hosts: {
        [hA]: { id: hA, name: 'Host A', ip: '127.0.0.1', port: 7860, order: 0 },
        [hB]: { id: hB, name: 'Host B', ip: '127.0.0.1', port: 7861, order: 1 },
      },
      hostOrder: [hA, hB],
      runtime: {},
      activeHostId: hA,
    })
    // hostSettings intentionally left as beforeEach blank { hosts: {} }
  }

  /** Only hostSettings store rehydrated; hostStore is empty (hasn't rehydrated yet). */
  function seedSettingsOnly() {
    // Merge-mode: preserve action methods in store state
    useHostSettingsStore.setState({ hosts: { [hA]: { editor: { homePath: '/tmp/a' } } } })
    // hostStore intentionally left as beforeEach blank { hosts: {} }
  }

  // ── Case 1: Rehydrate order A — both stores rehydrated ───────────────────

  it('A: both rehydrated — removeHost clears hostSettings; undo restores both', () => {
    seedBothRehydrated()

    const restore = deleteHostCascade(hA, true)

    // Cascade: hA gone from both stores
    expect(useHostStore.getState().hosts[hA]).toBeUndefined()
    expect(useHostSettingsStore.getState().hosts[hA]).toBeUndefined()
    // hB untouched
    expect(useHostStore.getState().hosts[hB]).toBeDefined()

    // Undo: both restored
    restore()
    expect(useHostStore.getState().hosts[hA]).toBeDefined()
    expect(useHostSettingsStore.getState().hosts[hA]).toEqual({ editor: { homePath: '/tmp/a' } })
  })

  // ── Case 2: Rehydrate order B — hostSettings only, hostStore empty ────────

  it('B: settings-only rehydrate — removeHost is no-op (host not in store); hostSettings unchanged', () => {
    seedSettingsOnly()

    // hA is NOT in hostStore.hosts, so deleteHostCascade's pre-check aborts
    const restore = deleteHostCascade(hA, true)

    // Settings must be untouched — cascade aborted before touching anything
    expect(useHostSettingsStore.getState().hosts[hA]).toEqual({ editor: { homePath: '/tmp/a' } })

    // Undo must be a safe no-op
    expect(() => restore()).not.toThrow()
    // Settings still intact after undo no-op
    expect(useHostSettingsStore.getState().hosts[hA]).toEqual({ editor: { homePath: '/tmp/a' } })
  })

  // ── Case 3: Rehydrate order C — hostStore only, hostSettings empty ────────

  it('C: host-only rehydrate — removeHost cascades; undo restores host, hostSettings stays empty', () => {
    seedHostOnly()

    const restore = deleteHostCascade(hA, true)

    // Cascade ran: hA removed from hostStore, hostSettings was already empty (no-op clear)
    expect(useHostStore.getState().hosts[hA]).toBeUndefined()
    expect(useHostSettingsStore.getState().hosts[hA]).toBeUndefined()

    // Undo: hostStore restored, hostSettings still empty (nothing was snapshotted)
    restore()
    expect(useHostStore.getState().hosts[hA]).toBeDefined()
    expect(useHostStore.getState().hostOrder).toContain(hA)
    // hostSettings was empty at snapshot time — undo must not invent new data
    expect(useHostSettingsStore.getState().hosts[hA]).toBeUndefined()
  })

  // ── Case 4: Interleaved write during cascade — undo must NOT clobber ──────

  it('interleaved write: fresh hostSettings write during undo window is preserved by undo gate', () => {
    seedBothRehydrated()

    const restore = deleteHostCascade(hA, true)
    expect(useHostSettingsStore.getState().hosts[hA]).toBeUndefined()

    // Simulate a BroadcastChannel sync or re-add writing fresh settings for hA
    // (recreate host first so hostWasRecreated guard kicks in)
    useHostStore.setState((s) => ({
      hosts: {
        ...s.hosts,
        [hA]: { id: hA, name: 'Host A (fresh)', ip: '9.9.9.9', port: 7860, order: 2 },
      },
      hostOrder: [...s.hostOrder, hA],
    }))
    // Write new settings for the recreated host
    useHostSettingsStore.setState((s) => ({
      hosts: { ...s.hosts, [hA]: { editor: { homePath: '/new' } } },
    }))

    restore()

    // Undo must NOT overwrite the freshly written settings (hostWasRecreated gate)
    expect(useHostSettingsStore.getState().hosts[hA]).toEqual({ editor: { homePath: '/new' } })
    // And the recreated host survives
    expect(useHostStore.getState().hosts[hA]?.name).toBe('Host A (fresh)')
  })

  // ── Case 5: hostWasRecreated gate covers all 6 restore categories ─────────

  it('hostWasRecreated: recreating same-id host during undo window blocks all 6 restore categories', () => {
    seedBothRehydrated()

    // Seed additional data in all cascaded stores so we can assert each is gated
    const event: NormalizedEvent = {
      agent_type: 'cc',
      status: 'idle',
      raw_event_name: 'Stop',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleNormalizedEvent(hA, 'dev001', event)
    // Also seed statuses directly so the snapshot is non-vacuous (handleNormalizedEvent
    // may or may not write statuses depending on event.status; seed directly to be sure)
    useAgentStore.setState((s) => ({
      statuses: { ...s.statuses, [`${hA}:dev001`]: 'running' as const },
    }))
    useStreamStore.getState().addMessage(hA, 'dev001', { type: 'assistant' } as StreamMessage)
    useSessionStore.getState().replaceHost(hA, [{ code: 'dev001', name: 'Dev', mode: 'terminal', cwd: '~', cc_session_id: '', cc_model: '', has_relay: false }])
    // Seed a tab in a workspace so the tab-restore gate (category 6) can be asserted
    const ws6 = useWorkspaceStore.getState().addWorkspace('WS for gate test')
    const tab6 = makeSessionTab(hA, 'dev001')
    useTabStore.getState().addTab(tab6)
    useWorkspaceStore.getState().addTabToWorkspace(ws6.id, tab6.id)

    const restore = deleteHostCascade(hA, true)

    // Recreate hA as a different entity during undo window
    useHostStore.setState((s) => ({
      hosts: {
        ...s.hosts,
        [hA]: { id: hA, name: 'Recreated A', ip: '9.9.9.9', port: 7860, order: 2 },
      },
      hostOrder: [...s.hostOrder, hA],
    }))

    restore()

    // hostWasRecreated = true → all 6 restore categories must be blocked:
    // 1. host position/order — recreated entry stays, snapshot hostOrder NOT applied
    expect(useHostStore.getState().hosts[hA]?.name).toBe('Recreated A')
    // 2. hostSettings — not restored (stays undefined)
    expect(useHostSettingsStore.getState().hosts[hA]).toBeUndefined()
    // 3. sessions — not restored
    expect(useSessionStore.getState().sessions[hA]).toBeUndefined()
    // 4. agentStore statuses — not restored (seeded directly above; snapshot captured it; gate must block)
    expect(useAgentStore.getState().statuses[`${hA}:dev001`]).toBeUndefined()
    // 5. streamStore data — not restored
    expect(useStreamStore.getState().sessions[`${hA}:dev001`]).toBeUndefined()
    // 6. tab/workspace-membership restore — tab must NOT be re-added to the workspace
    expect(useTabStore.getState().tabs[tab6.id]).toBeUndefined()
    expect(useWorkspaceStore.getState().findWorkspaceByTab(tab6.id)).toBeNull()
  })

  // ── Case 6: Last-host veto ────────────────────────────────────────────────

  it('last-host veto: removeHost with only one host is no-op; cascade aborted', () => {
    // Seed only hA — removeHost will veto because hosts.length <= 1
    // Merge-mode: preserve action methods
    useHostStore.setState({
      hosts: {
        [hA]: { id: hA, name: 'Host A', ip: '127.0.0.1', port: 7860, order: 0 },
      },
      hostOrder: [hA],
      runtime: {},
      activeHostId: hA,
    })
    useHostSettingsStore.setState({ hosts: { [hA]: { editor: { homePath: '/tmp/a' } } } })

    const restore = deleteHostCascade(hA, true)

    // Host still present (veto)
    expect(useHostStore.getState().hosts[hA]).toBeDefined()
    // Settings untouched (cascade never ran)
    expect(useHostSettingsStore.getState().hosts[hA]).toEqual({ editor: { homePath: '/tmp/a' } })
    // Undo is safe no-op
    expect(() => restore()).not.toThrow()
  })

  // ── Case 7: Cascade regression — PR-1 contract ───────────────────────────

  it('cascade regression: removeHost clears hostSettings.hA (PR-1 contract)', () => {
    seedBothRehydrated()

    deleteHostCascade(hA, true)

    expect(useHostSettingsStore.getState().hosts[hA]).toBeUndefined()
    // hB settings not affected (hB had no settings — stays undefined)
    expect(useHostStore.getState().hosts[hB]).toBeDefined()
  })

  // ── Case 8: Undo regression — PR-1 contract ──────────────────────────────

  it('undo regression: cascade then undo restores hosts + hostSettings (PR-1 contract)', () => {
    seedBothRehydrated()

    const restore = deleteHostCascade(hA, true)
    // Verify cascade ran
    expect(useHostStore.getState().hosts[hA]).toBeUndefined()
    expect(useHostSettingsStore.getState().hosts[hA]).toBeUndefined()

    restore()

    // Both restored
    expect(useHostStore.getState().hosts[hA]).toBeDefined()
    expect(useHostStore.getState().hostOrder).toContain(hA)
    expect(useHostSettingsStore.getState().hosts[hA]).toEqual({ editor: { homePath: '/tmp/a' } })
  })

  // ── Case 9: Workspace cross — useWorkspaceSettingsStore isolation ─────────

  it('workspace cross: removeHost does NOT clear useWorkspaceSettingsStore entries for hA-owned workspace', () => {
    seedBothRehydrated()

    // Seed a workspace with settings that reference hA indirectly (as module owner context)
    const WS_ID = 'ws-hA'
    // Merge-mode: preserve action methods
    useWorkspaceSettingsStore.setState({ workspaces: { [WS_ID]: { 'some-module': { hostId: hA, value: 42 } } } })

    deleteHostCascade(hA, true)

    // deleteHostCascade only uses useWorkspaceStore (tab membership), NOT useWorkspaceSettingsStore
    // — the module-settings store for workspaces must remain untouched as collateral-clear regression
    expect(useWorkspaceSettingsStore.getState().workspaces[WS_ID]).toEqual({
      'some-module': { hostId: hA, value: 42 },
    })
  })
})
