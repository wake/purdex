import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceIconPicker } from './WorkspaceIconPicker'
import { WORKSPACE_ICONS } from '../constants'

describe('WorkspaceIconPicker', () => {
  beforeEach(() => { cleanup() })

  it('renders all icon options', () => {
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={vi.fn()} onCancel={vi.fn()} />)
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-icon'))
    expect(buttons.length).toBe(WORKSPACE_ICONS.length)
  })

  it('calls onSelect with chosen icon', () => {
    const onSelect = vi.fn()
    render(<WorkspaceIconPicker currentIcon={undefined} onSelect={onSelect} onCancel={vi.fn()} />)
    const buttons = screen.getAllByRole('button').filter((b) => b.getAttribute('data-icon'))
    fireEvent.click(buttons[0])
    expect(onSelect).toHaveBeenCalledWith(buttons[0].getAttribute('data-icon'))
  })

  it('highlights current icon', () => {
    render(<WorkspaceIconPicker currentIcon={WORKSPACE_ICONS[2]} onSelect={vi.fn()} onCancel={vi.fn()} />)
    const activeBtn = screen.getByRole('button', { pressed: true })
    expect(activeBtn.getAttribute('data-icon')).toBe(WORKSPACE_ICONS[2])
  })
})
