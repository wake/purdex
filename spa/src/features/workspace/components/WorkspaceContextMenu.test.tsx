import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceContextMenu } from './WorkspaceContextMenu'

describe('WorkspaceContextMenu', () => {
  beforeEach(() => { cleanup() })

  it('renders all menu items', () => {
    render(
      <WorkspaceContextMenu
        position={{ x: 100, y: 200 }}
        workspaceName="My WS"
        onRename={vi.fn()}
        onChangeColor={vi.fn()}
        onChangeIcon={vi.fn()}
        onDelete={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    expect(screen.getByText(/rename/i)).toBeInTheDocument()
    expect(screen.getByText(/color/i)).toBeInTheDocument()
    expect(screen.getByText(/icon/i)).toBeInTheDocument()
    expect(screen.getByText(/delete/i)).toBeInTheDocument()
  })

  it('calls onRename when clicking rename', () => {
    const onRename = vi.fn()
    render(
      <WorkspaceContextMenu position={{ x: 100, y: 200 }} workspaceName="WS"
        onRename={onRename} onChangeColor={vi.fn()} onChangeIcon={vi.fn()} onDelete={vi.fn()} onClose={vi.fn()} />,
    )
    fireEvent.click(screen.getByText(/rename/i))
    expect(onRename).toHaveBeenCalled()
  })

  it('calls onDelete when clicking delete', () => {
    const onDelete = vi.fn()
    render(
      <WorkspaceContextMenu position={{ x: 100, y: 200 }} workspaceName="WS"
        onRename={vi.fn()} onChangeColor={vi.fn()} onChangeIcon={vi.fn()} onDelete={onDelete} onClose={vi.fn()} />,
    )
    fireEvent.click(screen.getByText(/delete/i))
    expect(onDelete).toHaveBeenCalled()
  })

  it('calls onClose on backdrop click', () => {
    const onClose = vi.fn()
    render(
      <WorkspaceContextMenu position={{ x: 100, y: 200 }} workspaceName="WS"
        onRename={vi.fn()} onChangeColor={vi.fn()} onChangeIcon={vi.fn()} onDelete={vi.fn()} onClose={onClose} />,
    )
    fireEvent.mouseDown(screen.getByTestId('context-menu-backdrop'))
    expect(onClose).toHaveBeenCalled()
  })
})
