import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceColorPicker } from './WorkspaceColorPicker'
import { WORKSPACE_COLORS } from '../constants'

describe('WorkspaceColorPicker', () => {
  beforeEach(() => { cleanup() })

  it('renders all color options', () => {
    render(<WorkspaceColorPicker currentColor="#e75a5a" onSelect={vi.fn()} onCancel={vi.fn()} />)
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-color'))
    expect(buttons.length).toBe(WORKSPACE_COLORS.length)
  })

  it('highlights current color', () => {
    render(<WorkspaceColorPicker currentColor="#e75a5a" onSelect={vi.fn()} onCancel={vi.fn()} />)
    const activeBtn = screen.getByRole('button', { pressed: true })
    expect(activeBtn.getAttribute('data-color')).toBe('#e75a5a')
  })

  it('calls onSelect with chosen color', () => {
    const onSelect = vi.fn()
    render(<WorkspaceColorPicker currentColor="#e75a5a" onSelect={onSelect} onCancel={vi.fn()} />)
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-color'))
    const different = buttons.find((b) => b.getAttribute('data-color') !== '#e75a5a')!
    fireEvent.click(different)
    expect(onSelect).toHaveBeenCalledWith(different.getAttribute('data-color'))
  })
})
