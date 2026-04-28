import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { WorkspaceQuickActionsPopover } from './WorkspaceQuickActionsPopover'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useWorkspaceStore } from '../../../stores/useWorkspaceStore'
import { useTabStore } from '../../../stores/useTabStore'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { clearModuleRegistry, registerModule } from '../../../lib/module-registry'

// codex round-1 P2 + round-2 fix tests need to drive runWorkspaceSlot to
// completion or pause it at the picker stage. importActual preserves every
// other host-api / execute-command export so unrelated callers still resolve.
// Default: createSession resolves quickly so transaction-safety tests can
// reach switchToSession; tests that need to inspect cwd/double-click before
// completion still get accurate call args because runWorkspaceSlot synchronously
// calls createSession from the executor.
vi.mock('../../../lib/host-api', async () => {
  const actual = await vi.importActual<typeof import('../../../lib/host-api')>(
    '../../../lib/host-api',
  )
  return {
    ...actual,
    createSession: vi.fn().mockResolvedValue({
      code: 'sess-new',
      name: 'sess-new',
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

function setup() {
  useQuickCommandStore.setState({
    global: [{ id: 'cmd-a', name: 'Alpha', command: 'a' }],
    byHost: {},
    bindings: { 'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS] },
  })
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  // HostPickerPopover 內部讀 useHostStore；至少塞一個 host 避免空狀態誤判
  useHostStore.setState({
    hosts: { h1: { id: 'h1', name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 } },
    hostOrder: ['h1'],
    runtime: { h1: { status: 'connected' } },
    activeHostId: 'h1',
  } as Partial<ReturnType<typeof useHostStore.getState>> as never)
  // Tab + workspace stores need a baseline shape so insertTab / setActiveTab
  // don't blow up when the executor reaches switchToSession.
  useTabStore.setState({
    tabs: {},
    tabOrder: [],
    activeTabId: null,
  } as Partial<ReturnType<typeof useTabStore.getState>> as never)
  useWorkspaceStore.setState({
    workspaces: [{ id: 'w1', name: 'WS', tabs: [], activeTabId: null, moduleConfig: {} }],
    activeWorkspaceId: 'w1',
  } as Partial<ReturnType<typeof useWorkspaceStore.getState>> as never)
  clearModuleRegistry()
  registerModule({ id: 'quick-commands', name: 'Quick Commands', disableable: true })
  vi.clearAllMocks()
  // Restore the default createSession resolved value after clearAllMocks wipes it.
  vi.mocked(createSession).mockResolvedValue({
    code: 'sess-new',
    name: 'sess-new',
    cwd: '/tmp',
    mode: 'terminal',
  } as Awaited<ReturnType<typeof createSession>>)
  vi.mocked(executeCommand).mockResolvedValue(undefined)
}

beforeEach(() => {
  cleanup()
  setup()
})

describe('WorkspaceQuickActionsPopover', () => {
  it('renders bound commands as chips', () => {
    render(<WorkspaceQuickActionsPopover workspaceId="w1" hostId="h1" />)
    expect(screen.getByLabelText(/^Alpha/)).toBeInTheDocument()
  })

  it('renders chips when hostId is null (picker handles host resolution at click time)', () => {
    // Spec v4 §3.2.2 — null hostId is a valid state (workspace has no
    // tmux-session tabs); we still surface the chips so user can pick a host.
    render(<WorkspaceQuickActionsPopover workspaceId="w1" hostId={null} />)
    expect(screen.getByLabelText(/^Alpha/)).toBeInTheDocument()
  })

  it('returns null when no commands bound', () => {
    useQuickCommandStore.setState({ bindings: {} })
    const { container } = render(
      <WorkspaceQuickActionsPopover workspaceId="w1" hostId="h1" />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('returns null when module disabled', () => {
    useModuleEnabledStore.getState().setEnabled('quick-commands', false)
    const { container } = render(
      <WorkspaceQuickActionsPopover workspaceId="w1" hostId="h1" />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('hostId=null + click chip → opens HostPickerPopover', () => {
    render(<WorkspaceQuickActionsPopover workspaceId="w1" hostId={null} />)
    expect(screen.queryByRole('listbox')).toBeNull()
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    expect(screen.getByRole('listbox')).toBeInTheDocument()
  })

  // codex round-2 — picker resolver cleanup (popover unmount via mouseleave on parent)
  it('unmount while picker is open → pending Promise resolves to null, no throw', () => {
    const { unmount } = render(<WorkspaceQuickActionsPopover workspaceId="w1" hostId={null} />)
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    expect(screen.getByRole('listbox')).toBeInTheDocument()
    // simulate parent hub mouseleave force-closing the popover wrapper.
    expect(() => unmount()).not.toThrow()
  })

  // codex round-1 P2 (F1) — cwd parity with context-menu path. Without this fix
  // the hover popover would call createSession with cwd='~' even when the
  // workspace has files.projectPath configured, while the right-click context
  // menu correctly inherits projectPath. Same command, divergent behavior.
  it('hostId known + workspace has projectPath → createSession invoked with cwd=projectPath', async () => {
    useWorkspaceStore.setState({
      workspaces: [
        {
          id: 'w1',
          name: 'WS',
          tabs: [],
          activeTabId: null,
          moduleConfig: { files: { projectPath: '/srv/work' } },
        },
      ],
      activeWorkspaceId: 'w1',
    } as Partial<ReturnType<typeof useWorkspaceStore.getState>> as never)
    render(<WorkspaceQuickActionsPopover workspaceId="w1" hostId="h1" />)
    await act(async () => {
      fireEvent.click(screen.getByLabelText(/^Alpha/))
    })
    expect(createSession).toHaveBeenCalledTimes(1)
    // createSession(hostId, name, cwd, mode)
    expect(vi.mocked(createSession).mock.calls[0]?.[2]).toBe('/srv/work')
  })

  // codex round-1 P2 (F2) — double-click race. With hostId already known the
  // picker never opens, so without an explicit executing guard busy stayed
  // false for the entire async createSession round-trip and a fast double-click
  // would queue two pipelines. Synchronous ref guard catches the same-tick
  // double-fire before React re-renders the disabled state.
  it('rapid double-click on chip → executor fires only once (executing race guard)', async () => {
    render(<WorkspaceQuickActionsPopover workspaceId="w1" hostId="h1" />)
    const chip = screen.getByLabelText(/^Alpha/)
    await act(async () => {
      fireEvent.click(chip)
      fireEvent.click(chip)
    })
    expect(createSession).toHaveBeenCalledTimes(1)
  })

  // codex round-1 P2 (F3) — picker hover-dismissal. The parent hub uses
  // onPickerOpenChange to decide whether to honor mouseleave / pointerdown
  // close events. Mount notifies false; opening the picker notifies true;
  // selecting/cancelling notifies false; unmount notifies false again.
  it('onPickerOpenChange fires true when picker opens, false when picker closes', () => {
    const onPickerOpenChange = vi.fn()
    render(
      <WorkspaceQuickActionsPopover
        workspaceId="w1"
        hostId={null}
        onPickerOpenChange={onPickerOpenChange}
      />,
    )
    // initial mount fires false; record the count then verify subsequent transitions.
    onPickerOpenChange.mockClear()
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    expect(onPickerOpenChange).toHaveBeenCalledWith(true)

    // select host → picker closes
    onPickerOpenChange.mockClear()
    fireEvent.click(screen.getByText(/mlab/))
    expect(onPickerOpenChange).toHaveBeenCalledWith(false)
  })

  it('onPickerOpenChange fires false on unmount even if picker was never opened', () => {
    const onPickerOpenChange = vi.fn()
    const { unmount } = render(
      <WorkspaceQuickActionsPopover
        workspaceId="w1"
        hostId="h1"
        onPickerOpenChange={onPickerOpenChange}
      />,
    )
    onPickerOpenChange.mockClear()
    unmount()
    expect(onPickerOpenChange).toHaveBeenCalledWith(false)
  })

  // ────────────────────────────────────────────────────────────────────
  // codex round-2 fix tests (R2)
  // ────────────────────────────────────────────────────────────────────

  // codex round-2 D1 — picker positioning when wrapper has CSS transform
  // (-translate-y-1/2). HostPickerPopover uses position:fixed; portal'ing it
  // out of the transformed subtree restores viewport-anchored layout.
  it('R2 D1: HostPickerPopover renders via portal into document.body, NOT inside the wrapper', () => {
    const { container } = render(<WorkspaceQuickActionsPopover workspaceId="w1" hostId={null} />)
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    const listbox = screen.getByRole('listbox')
    expect(listbox).toBeInTheDocument()
    // The container is the (transformed) wrapper subtree; listbox must NOT be inside it.
    expect(container.contains(listbox)).toBe(false)
    expect(document.body.contains(listbox)).toBe(true)
  })

  // codex round-2 D2 — pendingResolverRef cleanup. Unmount while picker is up
  // must actually settle the executor's await Promise (resolveHostId) so the
  // executor returns through its `finally` block. Without the ref, settling
  // relied on a setState updater that could miss a torn-down component, leaving
  // executingRef stuck and chips disabled forever.
  it('R2 D2: unmount while picker open → executor resolveHostId resolves null → createSession NOT called', async () => {
    const { unmount } = render(<WorkspaceQuickActionsPopover workspaceId="w1" hostId={null} />)
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    expect(screen.getByRole('listbox')).toBeInTheDocument()
    unmount()
    await new Promise<void>((r) => setTimeout(r, 0))
    expect(createSession).not.toHaveBeenCalled()
  })

  // codex round-2 F1 — early-return-null cleanup. When module is disabled (or
  // bindings disappear) mid-pick, the popover hides via early-return; the
  // unmount cleanup never fires because the component itself is still mounted.
  // Without the F1 effect, resolveHostId hangs forever and the chips stay busy.
  it('R2 F1: module disabled while picker open → picker closes, executor short-circuits without createSession', async () => {
    render(<WorkspaceQuickActionsPopover workspaceId="w1" hostId={null} />)
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    expect(screen.getByRole('listbox')).toBeInTheDocument()

    act(() => {
      useModuleEnabledStore.getState().setEnabled('quick-commands', false)
    })
    await new Promise<void>((r) => setTimeout(r, 0))

    // Picker DOM gone; executor returned early with hostId=null cancellation.
    expect(screen.queryByRole('listbox')).toBeNull()
    expect(createSession).not.toHaveBeenCalled()
  })

  // codex round-3 R3 F1 — sync open notification. The hub's pickerOpenRef must
  // be flipped BEFORE child popover effects fire (HostPickerPopover auto-focuses
  // its first option, which steals focus from the chip and triggers the hub's
  // onBlurCapture). If the notification ran in a useEffect (post-commit, AFTER
  // child effects), pickerOpenRef would still be false during blur and the hub
  // would unconditionally collapse popover, unmounting the picker before the
  // user could click. Verify the very first post-click notify is `(true)` —
  // i.e. fired synchronously inside resolveHostId, not asynchronously in a
  // useEffect tick (which would have produced an unrelated initial-mount false
  // first or fired after blur/render order).
  it('R3 F1: onPickerOpenChange(true) fires synchronously inside resolveHostId, not via post-commit useEffect', () => {
    const onPickerOpenChange = vi.fn()
    render(
      <WorkspaceQuickActionsPopover
        workspaceId="w1"
        hostId={null}
        onPickerOpenChange={onPickerOpenChange}
      />,
    )
    onPickerOpenChange.mockClear()
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    // The very first call after click MUST be (true). The legacy useEffect-based
    // notify could produce other patterns; sync notify produces exactly (true)
    // first, with no spurious initial false in between.
    expect(onPickerOpenChange.mock.calls[0]?.[0]).toBe(true)
  })

  // codex round-3 R3 F1 — same race, settle path. Selecting a host must sync
  // notify (false) so the hub's pickerOpenRef updates before HostPickerPopover
  // unmounts (which would otherwise fire focus-restore blur events with
  // pickerOpenRef still true → wrong, hub thinks picker is up).
  it('R3 F1: onPickerOpenChange(false) fires synchronously inside settlePicker (select)', () => {
    const onPickerOpenChange = vi.fn()
    render(
      <WorkspaceQuickActionsPopover
        workspaceId="w1"
        hostId={null}
        onPickerOpenChange={onPickerOpenChange}
      />,
    )
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    onPickerOpenChange.mockClear()
    fireEvent.click(screen.getByText(/mlab/))
    // Sync settle: the very first call after select MUST be (false), no
    // intervening true.
    expect(onPickerOpenChange.mock.calls[0]?.[0]).toBe(false)
  })

  // codex round-2 A1 — switchToSession transaction safety. If the workspace
  // is deleted in the executeCommand await window (after assertContextLive
  // already passed), switchToSession's pre-check fast-fails BEFORE
  // openSingletonTab, so no orphan tab + no active-tab/workspace mutations
  // leak through.
  it('R2 A1: workspace deleted during executeCommand await → switchToSession pre-check rejects, no orphan tab', async () => {
    let resumeExecuteCommand: (() => void) | null = null
    vi.mocked(executeCommand).mockImplementationOnce(
      () => new Promise<void>((resolve) => {
        resumeExecuteCommand = resolve
      }),
    )

    const prevTabsCount = Object.keys(useTabStore.getState().tabs).length
    const prevActiveTabId = useTabStore.getState().activeTabId

    render(<WorkspaceQuickActionsPopover workspaceId="w1" hostId="h1" />)
    fireEvent.click(screen.getByLabelText(/^Alpha/))

    // Flush createSession + assertContextLive; we are now paused inside the
    // executeCommand await.
    await act(async () => {
      await new Promise<void>((r) => setTimeout(r, 0))
    })
    expect(executeCommand).toHaveBeenCalledTimes(1)
    expect(resumeExecuteCommand).not.toBeNull()

    // Delete the workspace mid-flight.
    act(() => {
      useWorkspaceStore.setState({
        workspaces: [],
        activeWorkspaceId: null,
      } as Partial<ReturnType<typeof useWorkspaceStore.getState>> as never)
    })

    // Resume executeCommand → trySwitch → switchToSession's pre-check throws.
    // trySwitch swallows the throw and returns false; slot-executor surfaces
    // switch_failed toast and returns. Tab store stays clean.
    await act(async () => {
      resumeExecuteCommand?.()
      await new Promise<void>((r) => setTimeout(r, 0))
    })

    expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(prevTabsCount)
    expect(useTabStore.getState().activeTabId).toBe(prevActiveTabId)
    // Workspace stays deleted; we did NOT force activeWorkspaceId back to 'w1'.
    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === 'w1')).toBeUndefined()
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe(null)
  })
})
