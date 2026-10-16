import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { CountBadge } from './CountBadge'

describe('CountBadge', () => {
  it('shows the count, 12px high, 8px figures, a 1px ring in the sidebar background', () => {
    render(<CountBadge n={3} testId="b" />)
    const b = screen.getByTestId('b')
    expect(b).toHaveTextContent('3')
    expect(b.className).toContain('text-[8px]')
    expect(b.className).toContain('font-semibold')
    expect(b.style.boxShadow).toContain('1px')
    expect(b.style.boxShadow).toContain('--surface-tertiary')
  })
  // The figures' ink is 6 px tall and lands on whole CSS pixels, so the room left in the badge must be EVEN to split equally:
  // at 11 px the 5 px left split 3 / 2 and the figures sat a pixel low (or high, by where the badge fell); at 12 px it is
  // 3 / 3 at every sub-pixel position (measured on 2x screenshots, 10-16).
  it('is 12px high and at least 12px wide, line-height 1, so the figures sit in the vertical middle', () => {
    render(<CountBadge n={3} testId="b" />)
    const cls = screen.getByTestId('b').className.split(' ')
    for (const c of ['h-[12px]', 'min-w-[12px]', 'leading-none', 'items-center', 'justify-center']) expect(cls).toContain(c)
    expect(cls).not.toContain('h-[11px]')
    expect(cls).not.toContain('min-w-[11px]')
  })
  it('caps at 99+', () => {
    render(<CountBadge n={120} testId="b" />)
    expect(screen.getByTestId('b')).toHaveTextContent('99+')
  })
})
