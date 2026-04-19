import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { StatuslineConflictDialog } from './StatuslineConflictDialog'

describe('StatuslineConflictDialog', () => {
  it('shows the existing command', () => {
    render(
      <StatuslineConflictDialog existingCommand="ccstatusline" onWrap={vi.fn()} onCancel={vi.fn()} />
    )
    expect(screen.getByText('ccstatusline')).toBeInTheDocument()
  })

  it('Wrap button invokes onWrap', () => {
    const onWrap = vi.fn()
    render(<StatuslineConflictDialog existingCommand="x" onWrap={onWrap} onCancel={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: /wrap/i }))
    expect(onWrap).toHaveBeenCalledOnce()
  })

  it('Cancel button invokes onCancel', () => {
    const onCancel = vi.fn()
    render(<StatuslineConflictDialog existingCommand="x" onWrap={vi.fn()} onCancel={onCancel} />)
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(onCancel).toHaveBeenCalledOnce()
  })

  it('Escape key invokes onCancel', () => {
    const onCancel = vi.fn()
    render(<StatuslineConflictDialog existingCommand="x" onWrap={vi.fn()} onCancel={onCancel} />)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onCancel).toHaveBeenCalledOnce()
  })

  it('backdrop click invokes onCancel', () => {
    const onCancel = vi.fn()
    render(<StatuslineConflictDialog existingCommand="x" onWrap={vi.fn()} onCancel={onCancel} />)
    const dialog = screen.getByRole('dialog')
    fireEvent.click(dialog)
    expect(onCancel).toHaveBeenCalledOnce()
  })

  it('click inside card does not invoke onCancel', () => {
    const onCancel = vi.fn()
    render(<StatuslineConflictDialog existingCommand="foo" onWrap={vi.fn()} onCancel={onCancel} />)
    fireEvent.click(screen.getByText('foo'))
    expect(onCancel).not.toHaveBeenCalled()
  })
})
