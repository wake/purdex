import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceQuickActionsPopover } from './WorkspaceQuickActionsPopover'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { useHostStore } from '../../../stores/useHostStore'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { clearModuleRegistry, registerModule } from '../../../lib/module-registry'

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
})
