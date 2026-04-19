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
})
