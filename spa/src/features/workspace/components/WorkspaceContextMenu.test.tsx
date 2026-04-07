import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceContextMenu } from './WorkspaceContextMenu'

describe('WorkspaceContextMenu', () => {
  beforeEach(() => { cleanup() })

  it('renders Settings menu item', () => {
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        onSettings={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    expect(screen.getByText(/settings/i)).toBeInTheDocument()
  })

  it('calls onSettings and onClose when clicking settings', () => {
    const onSettings = vi.fn()
    const onClose = vi.fn()
    render(
      <WorkspaceContextMenu position={{ x: 100, y: 200 }} onSettings={onSettings} onClose={onClose} />,
    )
    fireEvent.click(screen.getByText(/settings/i))
    expect(onSettings).toHaveBeenCalled()
    expect(onClose).toHaveBeenCalled()
  })

  it('calls onClose on backdrop click', () => {
    const onClose = vi.fn()
    render(
      <WorkspaceContextMenu position={{ x: 100, y: 200 }} onSettings={vi.fn()} onClose={onClose} />,
    )
    fireEvent.mouseDown(screen.getByTestId('context-menu-backdrop'))
    expect(onClose).toHaveBeenCalled()
  })
})
