import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { WorkspaceQuickCommandsContextMenu } from './WorkspaceQuickCommandsContextMenu'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { clearModuleRegistry, registerModule } from '../../../lib/module-registry'

// codex round-1 B8 — full executable test body (subagent must not insert TODO stubs)
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../../../stores/useWorkspaceStore'

vi.mock('../../../lib/host-api', async () => {
  const actual = await vi.importActual<typeof import('../../../lib/host-api')>(
    '../../../lib/host-api',
  )
  return {
    ...actual,
    createSession: vi.fn().mockResolvedValue({
      code: 'sess-new',
      name: 'Alpha',
      cwd: '/tmp',
      mode: 'terminal',
    }),
  }
})

vi.mock('../../../lib/execute-command', () => ({
  executeCommand: vi.fn().mockResolvedValue(undefined),
}))

import { createSession } from '../../../lib/host-api'
import { executeCommand } from '../../../lib/execute-command'

function setup(workspaceId = 'w1', hostId = 'h1') {
  useQuickCommandStore.setState({
    global: [
      { id: 'cmd-a', name: 'Alpha', command: 'a' },
      { id: 'cmd-b', name: 'Bravo', command: 'b' },
    ],
    byHost: {},
    bindings: { 'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS] },
  })
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  // Tab + workspace stores need a base shape so insertTab / setActiveTab don't blow up
  useTabStore.setState({
    tabs: {},
    tabOrder: [],
    activeTabId: null,
  } as Partial<ReturnType<typeof useTabStore.getState>> as never)
  // codex round-2 B8 — useWorkspaceStore.workspaces 是 Workspace[]（非 Record），
  // 沒有 workspaceOrder 欄位（spa/src/features/workspace/store.ts:10 確認）。
  useWorkspaceStore.setState({
    workspaces: [{ id: workspaceId, name: 'WS', tabs: [], activeTabId: null, moduleConfig: {} }],
    activeWorkspaceId: workspaceId,
  } as Partial<ReturnType<typeof useWorkspaceStore.getState>> as never)
  clearModuleRegistry()
  registerModule({ id: 'quick-commands', name: 'Quick Commands', disableable: true })
  vi.clearAllMocks()
  return { workspaceId, hostId }
}

describe('WorkspaceQuickCommandsContextMenu', () => {
  beforeEach(() => setup())

  it('renders bound WORKSPACE_ACTIONS commands and hides unbound ones', () => {
    render(
      <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId="h1" onClose={() => {}} />,
    )
    expect(screen.getByLabelText(/^Alpha/)).toBeInTheDocument()
    expect(screen.queryByLabelText(/^Bravo/)).toBeNull()
  })

  it('returns null when no commands are bound (lets parent skip separator)', () => {
    useQuickCommandStore.setState({ bindings: {} })
    const { container } = render(
      <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId="h1" onClose={() => {}} />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('clicking a command calls onClose after executor finishes', async () => {
    const onClose = vi.fn()
    render(
      <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId="h1" onClose={onClose} />,
    )
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    // executor is async (createSession + executeCommand) → flush microtasks
    await new Promise<void>((r) => setTimeout(r, 0))
    expect(onClose).toHaveBeenCalled()
  })

  it('happy path — calls createSession with the inferred hostId and inserts tab into workspace (codex round-1 B5/B6)', async () => {
    render(
      <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId="h1" onClose={() => {}} />,
    )
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    await new Promise<void>((r) => setTimeout(r, 0))
    expect(createSession).toHaveBeenCalledWith('h1', expect.any(String), expect.any(String), 'terminal')
    // Tab should be inserted into the workspace (codex round-1 B6 — full
    // openSingletonAndSelect equivalent: openSingletonTab → insertTab → setActive).
    // codex round-2 B8 — workspaces 是 Workspace[]，用 .find() 不是 record key 索引
    const tabIds = useWorkspaceStore.getState().workspaces.find((w) => w.id === 'w1')?.tabs ?? []
    expect(tabIds.length).toBeGreaterThan(0)
    const tabId = tabIds[tabIds.length - 1]
    const tab = useTabStore.getState().tabs[tabId]
    expect(tab).toBeDefined()
    // codex round-1 B5 — tmux-session content has all required fields populated
    if (tab && tab.layout.type === 'leaf' && tab.layout.pane.content.kind === 'tmux-session') {
      const c = tab.layout.pane.content
      expect(c.hostId).toBe('h1')
      expect(c.sessionCode).toBe('sess-new')
      expect(c.mode).toBe('terminal')
      expect(c.cachedName).toBeDefined()
      expect(c.tmuxInstance).toBeDefined()
    }
    // active workspace + tab updated
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe('w1')
    expect(useTabStore.getState().activeTabId).toBe(tabId)
  })

  it('returns null when quick-commands module is disabled', () => {
    useModuleEnabledStore.getState().setEnabled('quick-commands', false)
    const { container } = render(
      <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId="h1" onClose={() => {}} />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('hostId=null — clicking a command opens HostPickerPopover (does NOT call createSession until user picks)', async () => {
    // Spec v4 §3.2.2 — when inferWorkspaceHostId returns null we must let the
    // user choose. The chip is rendered as soon as bindings exist; click triggers
    // the picker before executor.
    render(
      <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId={null} onClose={() => {}} />,
    )
    expect(screen.queryByRole('listbox')).toBeNull()
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    expect(await screen.findByRole('listbox')).toBeInTheDocument()
    // createSession not yet called — waiting for user to pick a host
    expect(createSession).not.toHaveBeenCalled()
  })

  it('hostId=null — picker Esc cancel → no createSession, no insertTab (codex round-1 B8 — full assertion)', async () => {
    const onClose = vi.fn()
    render(
      <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId={null} onClose={onClose} />,
    )
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    expect(await screen.findByRole('listbox')).toBeInTheDocument()
    fireEvent.keyDown(document, { key: 'Escape' })
    await new Promise<void>((r) => setTimeout(r, 0))
    expect(createSession).not.toHaveBeenCalled()
    // No tab inserted into workspace
    // codex round-2 B8 — workspaces 是 Workspace[]，用 .find() 不是 record key 索引
    expect(
      useWorkspaceStore.getState().workspaces.find((w) => w.id === 'w1')?.tabs ?? [],
    ).toHaveLength(0)
    // onClose still fires (executor's finally block runs even on cancel path)
    expect(onClose).toHaveBeenCalled()
  })

  // codex round-2 — picker resolver cleanup (parent unmount + duplicate resolve safety)
  it('unmount while picker is open → pending Promise resolves to null, executor finally runs, no throw', async () => {
    const onClose = vi.fn()
    const { unmount } = render(
      <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId={null} onClose={onClose} />,
    )
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    expect(await screen.findByRole('listbox')).toBeInTheDocument()
    // simulate the parent menu closing externally (e.g. user clicks outside the
    // WorkspaceContextMenu) while a picker resolver is mid-flight.
    expect(() => unmount()).not.toThrow()
    await new Promise<void>((r) => setTimeout(r, 0))
    // executor's finally should have run despite no explicit user action
    expect(onClose).toHaveBeenCalled()
    expect(createSession).not.toHaveBeenCalled()
  })

  // codex round-1 P2 — double-click race: when hostId is already known the
  // picker never opens, so without an explicit executing flag the chip stays
  // enabled across the entire createSession + send-keys round-trip. A fast
  // double-click would queue two pipelines and create two sessions.
  it('double-click before executor finishes only triggers createSession once', async () => {
    render(
      <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId="h1" onClose={vi.fn()} />,
    )
    const button = screen.getByLabelText(/^Alpha/)
    fireEvent.click(button)
    // Second click happens synchronously before the executor's first await
    // resolves — exact reproduction of the original race window.
    fireEvent.click(button)
    await new Promise<void>((r) => setTimeout(r, 0))
    expect(createSession).toHaveBeenCalledTimes(1)
  })

  // codex round-2 — spec §3.2: WORKSPACE_ACTIONS sessions inherit
  // workspace.moduleConfig.files.projectPath as cwd. Without this the slot
  // executor falls back to ~ and right-click commands run in the wrong
  // filesystem context.
  it('passes workspace projectPath as cwd to createSession (spec §3.2)', async () => {
    useWorkspaceStore.setState({
      workspaces: [
        {
          id: 'w1',
          name: 'WS',
          tabs: [],
          activeTabId: null,
          moduleConfig: { files: { projectPath: '/projects/foo' } },
        },
      ],
      activeWorkspaceId: 'w1',
    } as Partial<ReturnType<typeof useWorkspaceStore.getState>> as never)
    render(
      <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId="h1" onClose={vi.fn()} />,
    )
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    await new Promise<void>((r) => setTimeout(r, 0))
    expect(createSession).toHaveBeenCalledWith('h1', expect.any(String), '/projects/foo', 'terminal')
  })

  it('falls back to ~ when workspace has no projectPath configured', async () => {
    // Default setup() builds a workspace without moduleConfig.files; assert
    // the executor receives ~ as cwd.
    render(
      <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId="h1" onClose={vi.fn()} />,
    )
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    await new Promise<void>((r) => setTimeout(r, 0))
    expect(createSession).toHaveBeenCalledWith('h1', expect.any(String), '~', 'terminal')
  })

  // codex round-2 (high) + round-3 (high) — concurrent-delete transaction
  // safety. round-2 only asserted activeWorkspaceId; round-3 noted the orphan
  // tab + activeTabId mutation still leaked. The fix combines a pre-check
  // (closes most races) with a rollback path (closeTab + restore prior
  // activeTabId) for the residual window.
  it('workspace deleted while createSession in flight → no orphan tab, no active-tab mutation, no active-workspace mutation', async () => {
    const onClose = vi.fn()
    // Snapshot the pre-click tabStore state.
    const prevTabsCount = Object.keys(useTabStore.getState().tabs).length
    const prevActiveTabId = useTabStore.getState().activeTabId
    const initialActiveWs = useWorkspaceStore.getState().activeWorkspaceId
    render(
      <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId="h1" onClose={onClose} />,
    )
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    // Simulate concurrent deletion BEFORE the createSession Promise resolves.
    useWorkspaceStore.setState({
      workspaces: [],
      activeWorkspaceId: null,
    } as Partial<ReturnType<typeof useWorkspaceStore.getState>> as never)
    await new Promise<void>((r) => setTimeout(r, 0))

    // Pre-check fast-fails before openSingletonTab → tabStore unchanged.
    expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(prevTabsCount)
    expect(useTabStore.getState().activeTabId).toBe(prevActiveTabId)
    // Workspace stays gone; activeWorkspaceId NOT forced back to 'w1'.
    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === 'w1')).toBeUndefined()
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(null)
    expect(initialActiveWs).toBe('w1')
    // codex round-4 — assertContextLive returned false after createSession,
    // so executeCommand MUST NOT have been called. (round-3 only checked tab
    // state; destructive commands could still ship without this assertion.)
    expect(executeCommand).not.toHaveBeenCalled()
    // executor's finally still fires onClose
    expect(onClose).toHaveBeenCalled()
  })

  // codex round-1 P2 — capability ids that collide with inherited
  // Object.prototype methods would otherwise crash the slot host before it
  // could render. `getBindingTargets` is the own-property guard.
  it('does not crash when capability id collides with Object.prototype method (toString)', () => {
    useQuickCommandStore.setState({
      global: [{ id: 'toString', name: 'Evil', command: 'evil' }],
      byHost: {},
      bindings: {},
    })
    expect(() =>
      render(
        <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId="h1" onClose={vi.fn()} />,
      ),
    ).not.toThrow()
  })

  it('duplicate onSelect / onCancel calls are safe (resolver is nulled-out after first invocation)', async () => {
    const onClose = vi.fn()
    render(
      <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId={null} onClose={onClose} />,
    )
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    expect(await screen.findByRole('listbox')).toBeInTheDocument()
    // First Esc → cancels normally
    fireEvent.keyDown(document, { key: 'Escape' })
    // Second Esc (e.g. fast double-tap) → must be a no-op, NOT throw
    expect(() => fireEvent.keyDown(document, { key: 'Escape' })).not.toThrow()
    await new Promise<void>((r) => setTimeout(r, 0))
    expect(createSession).not.toHaveBeenCalled()
  })
})
