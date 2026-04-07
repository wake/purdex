import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceColorPicker } from './WorkspaceColorPicker'
import { WORKSPACE_COLORS } from '../constants'

describe('WorkspaceColorPicker', () => {
  beforeEach(() => { cleanup() })

  it('renders all color options', () => {
    render(<WorkspaceColorPicker currentColor="#7a6aaa" onSelect={vi.fn()} onCancel={vi.fn()} />)
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-color'))
    expect(buttons.length).toBe(WORKSPACE_COLORS.length)
  })

  it('highlights current color', () => {
    render(<WorkspaceColorPicker currentColor="#7a6aaa" onSelect={vi.fn()} onCancel={vi.fn()} />)
    const activeBtn = screen.getByRole('button', { pressed: true })
    expect(activeBtn.getAttribute('data-color')).toBe('#7a6aaa')
  })

  it('calls onSelect with chosen color', () => {
    const onSelect = vi.fn()
    render(<WorkspaceColorPicker currentColor="#7a6aaa" onSelect={onSelect} onCancel={vi.fn()} />)
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-color'))
    const different = buttons.find((b) => b.getAttribute('data-color') !== '#7a6aaa')!
    fireEvent.click(different)
    expect(onSelect).toHaveBeenCalledWith(different.getAttribute('data-color'))
  })
})
