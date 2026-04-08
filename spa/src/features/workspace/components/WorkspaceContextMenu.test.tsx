import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react'
import { WorkspaceContextMenu } from './WorkspaceContextMenu'

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
