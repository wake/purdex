import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceDeleteDialog } from './WorkspaceDeleteDialog'

const mockTabs = [
  { id: 't1', label: 'dev session' },
  { id: 't2', label: 'settings' },
]

describe('WorkspaceDeleteDialog', () => {
  beforeEach(() => {
    cleanup()
  })

  it('renders workspace name and tab list', () => {
    render(
      <WorkspaceDeleteDialog
        workspaceName="My Workspace"
        tabs={mockTabs}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    expect(screen.getByText(/My Workspace/)).toBeInTheDocument()
    expect(screen.getByText('dev session')).toBeInTheDocument()
    expect(screen.getByText('settings')).toBeInTheDocument()
  })

  it('all tabs are checked by default', () => {
    render(
      <WorkspaceDeleteDialog
        workspaceName="WS"
        tabs={mockTabs}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    const checkboxes = screen.getAllByRole('checkbox')
    expect(checkboxes).toHaveLength(2)
    checkboxes.forEach((cb) => expect(cb).toBeChecked())
  })

  it('unchecking a tab excludes it from closedTabIds', () => {
    const onConfirm = vi.fn()
    render(
      <WorkspaceDeleteDialog
        workspaceName="WS"
        tabs={mockTabs}
        onConfirm={onConfirm}
        onCancel={vi.fn()}
      />,
    )
    const checkboxes = screen.getAllByRole('checkbox')
    fireEvent.click(checkboxes[0])
    fireEvent.click(screen.getByRole('button', { name: /delete/i }))
    expect(onConfirm).toHaveBeenCalledWith(['t2'])
  })

  it('confirm with all checked sends all tab ids', () => {
    const onConfirm = vi.fn()
    render(
      <WorkspaceDeleteDialog
        workspaceName="WS"
        tabs={mockTabs}
        onConfirm={onConfirm}
        onCancel={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /delete/i }))
    expect(onConfirm).toHaveBeenCalledWith(['t1', 't2'])
  })

  it('cancel calls onCancel', () => {
    const onCancel = vi.fn()
    render(
      <WorkspaceDeleteDialog
        workspaceName="WS"
        tabs={mockTabs}
        onConfirm={vi.fn()}
        onCancel={onCancel}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(onCancel).toHaveBeenCalled()
  })

  it('renders empty tab list gracefully', () => {
    const onConfirm = vi.fn()
    render(
      <WorkspaceDeleteDialog
        workspaceName="WS"
        tabs={[]}
        onConfirm={onConfirm}
        onCancel={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /delete/i }))
    expect(onConfirm).toHaveBeenCalledWith([])
  })

  it('calls onCancel on Escape key', () => {
    const onCancel = vi.fn()
    render(
      <WorkspaceDeleteDialog
        workspaceName="WS"
        tabs={mockTabs}
        onConfirm={vi.fn()}
        onCancel={onCancel}
      />,
    )
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onCancel).toHaveBeenCalled()
  })
})
