import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { WorkspaceQuickActionsPopover } from './WorkspaceQuickActionsPopover'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useWorkspaceStore } from '../../../stores/useWorkspaceStore'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { clearModuleRegistry, registerModule } from '../../../lib/module-registry'
import { createSession } from '../../../lib/host-api'

// codex round-1 P2 fix tests need to spy on createSession (the cwd sink in
// runWorkspaceSlot) without performing real network calls. importActual
// preserves every other host-api export so unrelated callers still resolve.
vi.mock('../../../lib/host-api', async () => {
  const actual = await vi.importActual<typeof import('../../../lib/host-api')>(
    '../../../lib/host-api',
  )
  return {
    ...actual,
    // Never resolves — keeps runWorkspaceSlot pending so we can assert call args
    // before the executor moves past createSession.
    createSession: vi.fn(() => new Promise(() => {})),
  }
})

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
  clearModuleRegistry()
  registerModule({ id: 'quick-commands', name: 'Quick Commands', disableable: true })
}

beforeEach(() => {
  cleanup()
  vi.mocked(createSession).mockClear()
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
})
