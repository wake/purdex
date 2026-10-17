import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { CountBadge } from './CountBadge'

describe('CountBadge — the default size', () => {
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
  it('the default size (the watcher shield\'s) sits 4px out of the corner', () => {
    render(<CountBadge n={3} testId="b" />)
    const cls = screen.getByTestId('b').className.split(' ')
    for (const c of ['-top-[4px]', '-right-[4px]']) expect(cls).toContain(c)
  })
})

// The host cell's badge (journal v5 §3.6, the user's U9: too big, further to the upper right). 9 px leaves 4 px around the 7 px
// figures' ~5 px ink, still an even split.
describe('CountBadge — sm (the host cell)', () => {
  const SM = ['h-[9px]', 'min-w-[9px]', 'text-[7px]', '-top-[5px]', '-right-[5px]']
  const MD = ['h-[12px]', 'min-w-[12px]', 'text-[8px]', '-top-[4px]', '-right-[4px]']
  it('is 9px high, at least 9px wide, 7px figures, 5px out of the corner; none of the default size\'s classes', () => {
    render(<CountBadge n={3} size="sm" testId="b" />)
    const cls = screen.getByTestId('b').className.split(' ')
    for (const c of SM) expect(cls).toContain(c)
    for (const c of MD) expect(cls).not.toContain(c)
    for (const c of ['leading-none', 'items-center', 'justify-center', 'font-semibold', 'tabular-nums']) expect(cls).toContain(c)
  })
  // Anchored by its right edge with only a minimum width: two digits and 99+ widen it to the left, over the host badge, so its
  // right edge (what the indicator after it must clear) is the same as for one digit, and nothing cuts the figures.
  it.each([[12, '12'], [120, '99+']])('%s: shown whole, widening to the left (right-anchored, no fixed width, no clipping)', (n, text) => {
    render(<CountBadge n={n} size="sm" testId="b" />)
    const b = screen.getByTestId('b')
    expect(b).toHaveTextContent(text)
    const cls = b.className.split(' ')
    expect(cls).toContain('-right-[5px]')
    expect(cls.filter((c) => /^(-?(left|inset)|w|max-w)-/.test(c))).toEqual([])
    for (const c of ['overflow-hidden', 'truncate', 'text-ellipsis']) expect(cls).not.toContain(c)
  })
})
