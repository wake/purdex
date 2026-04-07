import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'

// Mock icon-loader to avoid CSR deep-import resolution failures in test env
vi.mock('../generated/icon-loader', () => ({
  iconLoaders: {},
}))

import { WorkspaceChip } from './WorkspaceChip'

describe('WorkspaceChip', () => {
  beforeEach(() => { cleanup() })

  it('renders workspace name', () => {
    render(<WorkspaceChip name="My Workspace" color="#7a6aaa" icon={undefined} onClick={vi.fn()} onContextMenu={vi.fn()} />)
    expect(screen.getByText('My Workspace')).toBeInTheDocument()
  })

  it('renders icon square with first char when no icon', () => {
    render(<WorkspaceChip name="Default" color="#7a6aaa" icon={undefined} onClick={vi.fn()} onContextMenu={vi.fn()} />)
    expect(screen.getByTestId('workspace-chip-icon')).toBeInTheDocument()
    expect(screen.getByText('D')).toBeInTheDocument()
  })

  it('calls onClick when clicked', () => {
    const onClick = vi.fn()
    render(<WorkspaceChip name="WS" color="#aaa" icon={undefined} onClick={onClick} onContextMenu={vi.fn()} />)
    fireEvent.click(screen.getByRole('button'))
    expect(onClick).toHaveBeenCalled()
  })

  it('calls onContextMenu on right click', () => {
    const onContextMenu = vi.fn()
    render(<WorkspaceChip name="WS" color="#aaa" icon={undefined} onClick={vi.fn()} onContextMenu={onContextMenu} />)
    fireEvent.contextMenu(screen.getByRole('button'))
    expect(onContextMenu).toHaveBeenCalled()
  })

  it('does not render when name is null', () => {
    const { container } = render(<WorkspaceChip name={null} color={null} icon={undefined} onClick={vi.fn()} onContextMenu={vi.fn()} />)
    expect(container.firstChild).toBeNull()
  })

  it('renders separator div', () => {
    render(<WorkspaceChip name="WS" color="#aaa" icon={undefined} onClick={vi.fn()} onContextMenu={vi.fn()} />)
    expect(screen.getByTestId('workspace-chip-separator')).toBeInTheDocument()
  })
})
