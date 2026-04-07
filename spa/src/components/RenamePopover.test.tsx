import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { RenamePopover } from './RenamePopover'

describe('RenamePopover', () => {
  const defaultProps = {
    anchorRect: { left: 100, top: 30, width: 120, height: 26, bottom: 56, right: 220 } as DOMRect,
    currentName: 'my-session',
    onConfirm: vi.fn().mockResolvedValue(undefined),
    onCancel: vi.fn(),
  }

  beforeEach(() => { cleanup(); vi.clearAllMocks() })
  afterEach(() => cleanup())

  it('renders input with current name', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    expect(input).toBeInTheDocument()
  })

  it('selects all text on mount', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session') as HTMLInputElement
    expect(input.selectionStart).toBe(0)
    expect(input.selectionEnd).toBe('my-session'.length)
  })

  it('calls onConfirm with new name on Enter', async () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    fireEvent.change(input, { target: { value: 'new-name' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(defaultProps.onConfirm).toHaveBeenCalledWith('new-name')
  })

  it('calls onCancel on Escape', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(defaultProps.onCancel).toHaveBeenCalled()
  })

  it('does not call onConfirm with empty name', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    fireEvent.change(input, { target: { value: '' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(defaultProps.onConfirm).not.toHaveBeenCalled()
  })

  it('does not call onConfirm when name unchanged', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(defaultProps.onConfirm).not.toHaveBeenCalled()
  })

  it('shows error message when provided', () => {
    render(<RenamePopover {...defaultProps} error="Rename failed" />)
    expect(screen.getByText('Rename failed')).toBeInTheDocument()
  })
})
