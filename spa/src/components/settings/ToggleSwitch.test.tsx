import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ToggleSwitch } from './ToggleSwitch'

describe('ToggleSwitch', () => {
  it('renders with role switch', () => {
    render(<ToggleSwitch label="Test" checked={false} onChange={vi.fn()} />)
    expect(screen.getByRole('switch')).toBeTruthy()
  })

  it('reflects checked state via aria-checked', () => {
    render(<ToggleSwitch label="Test" checked={true} onChange={vi.fn()} />)
    expect(screen.getByRole('switch').getAttribute('aria-checked')).toBe('true')
  })

  it('calls onChange with toggled value on click', () => {
    const onChange = vi.fn()
    render(<ToggleSwitch label="Test" checked={false} onChange={onChange} />)
    fireEvent.click(screen.getByRole('switch'))
    expect(onChange).toHaveBeenCalledWith(true)
  })

  it('uses label as aria-label', () => {
    render(<ToggleSwitch label="My Toggle" checked={false} onChange={vi.fn()} />)
    expect(screen.getByLabelText('My Toggle')).toBeTruthy()
  })

  it('has type="button" to prevent form submission', () => {
    render(<ToggleSwitch label="Test" checked={false} onChange={vi.fn()} />)
    expect(screen.getByRole('switch').getAttribute('type')).toBe('button')
  })

  it('applies active color when checked', () => {
    render(<ToggleSwitch label="Test" checked={true} onChange={vi.fn()} />)
    expect(screen.getByRole('switch').className).toContain('bg-accent')
  })
})
