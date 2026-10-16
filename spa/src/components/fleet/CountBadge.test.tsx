import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { CountBadge } from './CountBadge'

describe('CountBadge', () => {
  it('shows the count, 11px high, 8px figures, a 1px ring in the sidebar background', () => {
    render(<CountBadge n={3} testId="b" />)
    const b = screen.getByTestId('b')
    expect(b).toHaveTextContent('3')
    expect(b.className).toContain('h-[11px]')
    expect(b.className).toContain('text-[8px]')
    expect(b.className).toContain('font-semibold')
    expect(b.style.boxShadow).toContain('1px')
    expect(b.style.boxShadow).toContain('--surface-tertiary')
  })
  it('caps at 99+', () => {
    render(<CountBadge n={120} testId="b" />)
    expect(screen.getByTestId('b')).toHaveTextContent('99+')
  })
})
