import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { CommandSlot } from './CommandSlot'
import { useQuickCommandStore } from '../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'
import { QUICK_COMMAND_SLOTS } from '../lib/quick-command-slots'
import { clearModuleRegistry, registerModule } from '../lib/module-registry'

function resetStores() {
  useQuickCommandStore.setState({
    global: [
      { id: 'cmd-a', name: 'A', command: 'a' },
      { id: 'cmd-b', name: 'B', command: 'b' },
      { id: 'cmd-c', name: 'C', command: 'c' },
    ],
    byHost: {},
    bindings: {
      'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS],
      'cmd-c': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS],
    },
  })
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  clearModuleRegistry()
  registerModule({
    id: 'quick-commands',
    name: 'Quick Commands',
    disableable: true,
  })
}

describe('CommandSlot', () => {
  beforeEach(() => resetStores())
  afterEach(() => clearModuleRegistry())

  it('renders bound commands in capability order (cmd-a then cmd-c, NOT bindings key order — codex round-1 C12)', () => {
    const exec = vi.fn().mockResolvedValue(undefined)
    render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: 'h1', workspaceId: 'w1' }}
        executor={exec}
      />,
    )
    // codex round-1 C12 — assert exact order via accessible names; NOT arrayContaining
    const buttons = screen.getAllByRole('button')
    const names = buttons.map((b) => b.getAttribute('aria-label'))
    // cmd-a first (capability index 0), cmd-c second (capability index 2);
    // cmd-b is unbound → not in DOM at all
    expect(names).toEqual(['A', 'C'])
    expect(screen.queryByLabelText(/^B/)).toBeNull()
  })

  it('order follows capability list even when bindings record key order is reversed (codex round-1 C13)', () => {
    // Same global capability list; rebuild bindings in REVERSE key order.
    // The order seen on screen must still be capability order (cmd-a → cmd-c).
    useQuickCommandStore.setState({
      global: [
        { id: 'cmd-a', name: 'A', command: 'a' },
        { id: 'cmd-b', name: 'B', command: 'b' },
        { id: 'cmd-c', name: 'C', command: 'c' },
      ],
      byHost: {},
      bindings: {
        // Insert cmd-c first, then cmd-a — Object.keys order would be [cmd-c, cmd-a]
        'cmd-c': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS],
        'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS],
      },
    })
    render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: null, workspaceId: 'w1' }} // hostId=null per codex C13 ask
        executor={vi.fn()}
      />,
    )
    const buttons = screen.getAllByRole('button')
    const names = buttons.map((b) => b.getAttribute('aria-label'))
    // Capability order is cmd-a (index 0) then cmd-c (index 2) — NOT bindings record order
    expect(names).toEqual(['A', 'C'])
  })

  it('returns null when quick-commands module is disabled (short-circuit)', () => {
    useModuleEnabledStore.getState().setEnabled('quick-commands', false)
    const { container } = render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: 'h1', workspaceId: 'w1' }}
        executor={vi.fn()}
      />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('returns null when no commands are bound', () => {
    useQuickCommandStore.setState({
      global: [
        { id: 'cmd-a', name: 'A', command: 'a' },
        { id: 'cmd-b', name: 'B', command: 'b' },
        { id: 'cmd-c', name: 'C', command: 'c' },
      ],
      byHost: {},
      bindings: {},
    })
    const { container } = render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: 'h1', workspaceId: 'w1' }}
        executor={vi.fn()}
      />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('clicking a button calls executor with (cmd, ctx)', async () => {
    const exec = vi.fn().mockResolvedValue(undefined)
    render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: 'h1', workspaceId: 'w1' }}
        executor={exec}
      />,
    )
    fireEvent.click(screen.getByLabelText(/^A/))
    expect(exec).toHaveBeenCalledTimes(1)
    expect(exec.mock.calls[0][0]).toMatchObject({ id: 'cmd-a' })
    expect(exec.mock.calls[0][1]).toMatchObject({ hostId: 'h1', workspaceId: 'w1' })
  })

  it('hostId=null is valid — passes null through to executor (caller resolves via picker)', () => {
    const exec = vi.fn().mockResolvedValue(undefined)
    render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: null, workspaceId: 'w1' }}
        executor={exec}
      />,
    )
    // commands are still rendered (hostId-null doesn't suppress UI)
    fireEvent.click(screen.getByLabelText(/^A/))
    expect(exec).toHaveBeenCalledTimes(1)
    expect(exec.mock.calls[0][1]).toMatchObject({ hostId: null, workspaceId: 'w1' })
  })

  it('supports custom render prop', () => {
    render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: 'h1' }}
        executor={vi.fn()}
        render={(cmd) => <span data-testid="custom">{cmd.id}</span>}
      />,
    )
    expect(screen.getAllByTestId('custom').map((n) => n.textContent)).toEqual(['cmd-a', 'cmd-c'])
  })

  // codex round-2 — render prop receives `run` as 3rd arg so custom UIs can
  // actually trigger executor. Without `run` custom render is an inert footgun.
  it('custom render receives `run` callback that triggers executor', () => {
    const exec = vi.fn().mockResolvedValue(undefined)
    render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: 'h1' }}
        executor={exec}
        render={(cmd, _ctx, run) => (
          <button data-testid={`custom-${cmd.id}`} onClick={run}>
            {cmd.name}
          </button>
        )}
      />,
    )
    fireEvent.click(screen.getByTestId('custom-cmd-a'))
    expect(exec).toHaveBeenCalledTimes(1)
    expect(exec.mock.calls[0][0].id).toBe('cmd-a')
    expect(exec.mock.calls[0][1]).toMatchObject({ hostId: 'h1' })
  })

  it('custom render `run` respects busy guard (no executor when busy=true)', () => {
    const exec = vi.fn().mockResolvedValue(undefined)
    render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: 'h1' }}
        executor={exec}
        busy={true}
        render={(cmd, _ctx, run) => (
          <button data-testid={`custom-${cmd.id}`} onClick={run}>
            {cmd.name}
          </button>
        )}
      />,
    )
    fireEvent.click(screen.getByTestId('custom-cmd-a'))
    expect(exec).not.toHaveBeenCalled()
  })

  it('disables all chip buttons while busy=true (codex round-1 C11 — picker resolver race guard)', () => {
    // Picker open → caller flips busy=true to prevent double-click on chip
    // (which would create a second pending Promise and a second picker instance).
    const exec = vi.fn().mockResolvedValue(undefined)
    render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: null, workspaceId: 'w1' }}
        executor={exec}
        busy={true}
      />,
    )
    const buttons = screen.getAllByRole('button')
    expect(buttons.length).toBeGreaterThan(0)
    buttons.forEach((b) => {
      expect((b as HTMLButtonElement).disabled).toBe(true)
    })
    // Click while busy → executor must NOT fire
    fireEvent.click(buttons[0])
    expect(exec).not.toHaveBeenCalled()
  })
})
