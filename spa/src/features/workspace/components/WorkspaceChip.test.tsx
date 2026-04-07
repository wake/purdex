import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceChip } from './WorkspaceChip'

describe('WorkspaceChip', () => {
  beforeEach(() => { cleanup() })

  it('renders workspace name and color dot', () => {
    render(<WorkspaceChip name="My Workspace" color="#7a6aaa" onClick={vi.fn()} onContextMenu={vi.fn()} />)
    expect(screen.getByText('My Workspace')).toBeInTheDocument()
    const dot = screen.getByTestId('workspace-color-dot')
    expect(dot.style.backgroundColor).toBe('rgb(122, 106, 170)')
  })

  it('calls onClick when clicked', () => {
    const onClick = vi.fn()
    render(<WorkspaceChip name="WS" color="#aaa" onClick={onClick} onContextMenu={vi.fn()} />)
    fireEvent.click(screen.getByText('WS'))
    expect(onClick).toHaveBeenCalled()
  })

  it('calls onContextMenu on right click', () => {
    const onContextMenu = vi.fn()
    render(<WorkspaceChip name="WS" color="#aaa" onClick={vi.fn()} onContextMenu={onContextMenu} />)
    fireEvent.contextMenu(screen.getByText('WS'))
    expect(onContextMenu).toHaveBeenCalled()
  })

  it('does not render when name is null', () => {
    const { container } = render(<WorkspaceChip name={null} color={null} onClick={vi.fn()} onContextMenu={vi.fn()} />)
    expect(container.firstChild).toBeNull()
  })
})
