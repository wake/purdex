import { describe, it, expect } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { HoverTooltip } from './HoverTooltip'

describe('HoverTooltip', () => {
  it('renders the provided text', () => {
    render(
      <div className="relative group">
        <span>trigger</span>
        <HoverTooltip>Hello world</HoverTooltip>
      </div>
    )
    expect(screen.getByText('Hello world')).toBeInTheDocument()
  })

  it('supports placement=top', () => {
    render(
      <div className="relative group">
        <HoverTooltip placement="top">top tip</HoverTooltip>
      </div>
    )
    const el = screen.getByText('top tip')
    expect(el).toHaveAttribute('data-placement', 'top')
  })

  it('defaults to placement=right', () => {
    render(
      <div className="relative group">
        <HoverTooltip>r tip</HoverTooltip>
      </div>
    )
    const el = screen.getByText('r tip')
    expect(el).toHaveAttribute('data-placement', 'right')
  })

  it('includes fade classes and becomes visible when parent is hovered', () => {
    render(
      <div data-testid="trigger" className="relative group">
        <HoverTooltip>fade</HoverTooltip>
      </div>
    )
    const el = screen.getByText('fade')
    expect(el.className).toMatch(/\bopacity-0\b/)
    expect(el.className).toMatch(/\btransition-opacity\b/)
    fireEvent.mouseEnter(screen.getByTestId('trigger'))
    expect(el.className).toMatch(/\bopacity-100\b/)
  })

  it('has role="tooltip" for screen readers', () => {
    render(
      <div className="relative group">
        <HoverTooltip>a11y</HoverTooltip>
      </div>
    )
    expect(screen.getByText('a11y').getAttribute('role')).toBe('tooltip')
  })

  it('renders through document.body so overflow ancestors cannot clip it', () => {
    const { container } = render(
      <div className="overflow-x-auto">
        <div data-testid="trigger" className="relative group">
          <HoverTooltip>portal tip</HoverTooltip>
        </div>
      </div>
    )
    const tooltip = screen.getByText('portal tip')
    expect(container.contains(tooltip)).toBe(false)
    expect(document.body.contains(tooltip)).toBe(true)
    fireEvent.mouseEnter(screen.getByTestId('trigger'))
    expect(tooltip.className).toContain('fixed')
    expect(tooltip.className).toContain('opacity-100')
  })
})
