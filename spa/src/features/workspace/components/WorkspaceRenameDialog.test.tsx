import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceRenameDialog } from './WorkspaceRenameDialog'

describe('WorkspaceRenameDialog', () => {
  beforeEach(() => { cleanup() })

  it('renders with current name pre-filled', () => {
    render(<WorkspaceRenameDialog currentName="Old Name" onConfirm={vi.fn()} onCancel={vi.fn()} />)
    const input = screen.getByRole('textbox') as HTMLInputElement
    expect(input.value).toBe('Old Name')
  })

  it('calls onConfirm with new name', () => {
    const onConfirm = vi.fn()
    render(<WorkspaceRenameDialog currentName="Old" onConfirm={onConfirm} onCancel={vi.fn()} />)
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'New Name' } })
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    expect(onConfirm).toHaveBeenCalledWith('New Name')
  })

  it('calls onConfirm on Enter key', () => {
    const onConfirm = vi.fn()
    render(<WorkspaceRenameDialog currentName="Old" onConfirm={onConfirm} onCancel={vi.fn()} />)
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Via Enter' } })
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' })
    expect(onConfirm).toHaveBeenCalledWith('Via Enter')
  })

  it('calls onCancel on Escape key', () => {
    const onCancel = vi.fn()
    render(<WorkspaceRenameDialog currentName="Old" onConfirm={vi.fn()} onCancel={onCancel} />)
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Escape' })
    expect(onCancel).toHaveBeenCalled()
  })

  it('does not confirm with empty name', () => {
    const onConfirm = vi.fn()
    render(<WorkspaceRenameDialog currentName="Old" onConfirm={onConfirm} onCancel={vi.fn()} />)
    fireEvent.change(screen.getByRole('textbox'), { target: { value: '  ' } })
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    expect(onConfirm).not.toHaveBeenCalled()
  })
})
