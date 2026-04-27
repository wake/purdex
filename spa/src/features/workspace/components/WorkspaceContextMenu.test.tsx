import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react'
import { WorkspaceContextMenu } from './WorkspaceContextMenu'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { clearModuleRegistry, registerModule } from '../../../lib/module-registry'

describe('WorkspaceContextMenu', () => {
  beforeEach(() => { cleanup() })

  afterEach(() => {
    // Restore electronAPI after each test
    Object.defineProperty(window, 'electronAPI', {
      value: undefined,
      writable: true,
      configurable: true,
    })
  })

  it('renders Settings menu item', () => {
    render(<WorkspaceContextMenu position={{ x: 100, y: 200 }} onSettings={vi.fn()} onClose={vi.fn()} />)
    expect(screen.getByText(/settings/i)).toBeInTheDocument()
  })

  it('calls onSettings and onClose when clicking settings', () => {
    const onSettings = vi.fn()
    const onClose = vi.fn()
    render(<WorkspaceContextMenu position={{ x: 100, y: 200 }} onSettings={onSettings} onClose={onClose} />)
    fireEvent.click(screen.getByText(/settings/i))
    expect(onSettings).toHaveBeenCalled()
    expect(onClose).toHaveBeenCalled()
  })

  it('calls onClose on backdrop click', () => {
    const onClose = vi.fn()
    render(<WorkspaceContextMenu position={{ x: 100, y: 200 }} onSettings={vi.fn()} onClose={onClose} />)
    fireEvent.mouseDown(screen.getByTestId('context-menu-backdrop'))
    expect(onClose).toHaveBeenCalled()
  })

  it('shows tear-off option when onTearOff is provided', () => {
    Object.defineProperty(window, 'electronAPI', {
      value: { getWindows: vi.fn().mockResolvedValue([]) },
      writable: true,
      configurable: true,
    })
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={vi.fn()}
        onTearOff={vi.fn()}
      />,
    )
    expect(screen.getByText(/move to new window/i)).toBeInTheDocument()
  })

  it('hides tear-off option when onTearOff is not provided', () => {
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    expect(screen.queryByText(/move to new window/i)).not.toBeInTheDocument()
  })

  it('shows merge submenu trigger when onMergeTo is provided and windows exist', async () => {
    Object.defineProperty(window, 'electronAPI', {
      value: {
        getWindows: vi.fn().mockResolvedValue([
          { id: 'win-1', title: 'Window 1' },
          { id: 'win-2', title: 'Window 2' },
        ]),
      },
      writable: true,
      configurable: true,
    })
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={vi.fn()}
        onMergeTo={vi.fn()}
      />,
    )
    await waitFor(() => {
      expect(screen.getByText(/move to window/i)).toBeInTheDocument()
    })
  })

  it('hides merge when onMergeTo is not provided', async () => {
    Object.defineProperty(window, 'electronAPI', {
      value: {
        getWindows: vi.fn().mockResolvedValue([
          { id: 'win-1', title: 'Window 1' },
        ]),
      },
      writable: true,
      configurable: true,
    })
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    // Give time for async load
    await waitFor(() => {
      expect(screen.queryByText(/move to window/i)).not.toBeInTheDocument()
    })
  })

  it('hides merge when window list is empty', async () => {
    Object.defineProperty(window, 'electronAPI', {
      value: {
        getWindows: vi.fn().mockResolvedValue([]),
      },
      writable: true,
      configurable: true,
    })
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={vi.fn()}
        onMergeTo={vi.fn()}
      />,
    )
    await waitFor(() => {
      expect(screen.queryByText(/move to window/i)).not.toBeInTheDocument()
    })
  })

  it('shows loading state while fetching windows', () => {
    // getWindows never resolves in this test (pending promise)
    Object.defineProperty(window, 'electronAPI', {
      value: {
        getWindows: vi.fn().mockReturnValue(new Promise(() => {})),
      },
      writable: true,
      configurable: true,
    })
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={vi.fn()}
        onMergeTo={vi.fn()}
      />,
    )
    expect(screen.getByText(/loading/i)).toBeInTheDocument()
  })

  it('calls onTearOff and onClose when tear-off clicked', () => {
    Object.defineProperty(window, 'electronAPI', {
      value: { getWindows: vi.fn().mockResolvedValue([]) },
      writable: true,
      configurable: true,
    })
    const onTearOff = vi.fn()
    const onClose = vi.fn()
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={onClose}
        onTearOff={onTearOff}
      />,
    )
    fireEvent.click(screen.getByText(/move to new window/i))
    expect(onTearOff).toHaveBeenCalled()
    expect(onClose).toHaveBeenCalled()
  })

  it('shows fallback label when window title is empty', async () => {
    Object.defineProperty(window, 'electronAPI', {
      value: {
        getWindows: vi.fn().mockResolvedValue([
          { id: 'win-abc', title: '' },
        ]),
      },
      writable: true,
      configurable: true,
    })
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={vi.fn()}
        onMergeTo={vi.fn()}
      />,
    )
    await waitFor(() => {
      expect(screen.getByText('Purdex')).toBeInTheDocument()
    })
  })

  // codex round-1 B8 — quick commands section integration (Phase 1b)
  it('renders quick commands section above Settings when WORKSPACE_ACTIONS bindings exist', () => {
    useQuickCommandStore.setState({
      global: [{ id: 'cmd-x', name: 'XCmd', command: 'x' }],
      byHost: {},
      bindings: { 'cmd-x': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS] },
    })
    useModuleEnabledStore.setState({ enabled: {}, baseline: null })
    clearModuleRegistry()
    registerModule({ id: 'quick-commands', name: 'Quick Commands', disableable: true })
    render(
      <WorkspaceContextMenu
        position={{ x: 0, y: 0 }}
        workspaceId="w1"
        hostId="h1"
        onSettings={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    expect(screen.getByLabelText(/^XCmd/)).toBeInTheDocument()
  })

  // codex round-1 P2 — own-property guard: a capability id colliding with an
  // inherited Object.prototype method (toString / valueOf / hasOwnProperty)
  // would have crashed the menu render via `bindings[c.id]?.includes(...)`
  // resolving to a non-array function. `getBindingTargets` is the fix.
  it('does not crash when a capability id collides with Object.prototype method (toString)', () => {
    useQuickCommandStore.setState({
      global: [{ id: 'toString', name: 'Evil', command: 'evil' }],
      byHost: {},
      bindings: {},
    })
    useModuleEnabledStore.setState({ enabled: {}, baseline: null })
    clearModuleRegistry()
    registerModule({ id: 'quick-commands', name: 'Quick Commands', disableable: true })
    expect(() =>
      render(
        <WorkspaceContextMenu
          position={{ x: 0, y: 0 }}
          workspaceId="w1"
          hostId="h1"
          onSettings={vi.fn()}
          onClose={vi.fn()}
        />,
      ),
    ).not.toThrow()
  })

  it('omits the quick commands section (and its separator) when no WORKSPACE_ACTIONS bindings exist', () => {
    useQuickCommandStore.setState({ global: [], byHost: {}, bindings: {} })
    useModuleEnabledStore.setState({ enabled: {}, baseline: null })
    clearModuleRegistry()
    registerModule({ id: 'quick-commands', name: 'Quick Commands', disableable: true })
    render(
      <WorkspaceContextMenu
        position={{ x: 0, y: 0 }}
        workspaceId="w1"
        hostId="h1"
        onSettings={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    // No quick command chip; existing Settings button still present
    expect(screen.queryByRole('toolbar', { name: /quick|快速/i })).toBeNull()
    expect(screen.getByText(/settings/i)).toBeInTheDocument()
  })

  it('calls onMergeTo with windowId when merge target clicked', async () => {
    Object.defineProperty(window, 'electronAPI', {
      value: {
        getWindows: vi.fn().mockResolvedValue([
          { id: 'win-42', title: 'My Other Window' },
        ]),
      },
      writable: true,
      configurable: true,
    })
    const onMergeTo = vi.fn()
    const onClose = vi.fn()
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={onClose}
        onMergeTo={onMergeTo}
      />,
    )
    // Wait for window list to load and appear
    const target = await screen.findByText('My Other Window')
    fireEvent.click(target)
    expect(onMergeTo).toHaveBeenCalledWith('win-42')
    expect(onClose).toHaveBeenCalled()
  })
})
