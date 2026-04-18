import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
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

  it('supports placement=top (absolute bottom-full)', () => {
    render(
      <div className="relative group">
        <HoverTooltip placement="top">top tip</HoverTooltip>
      </div>
    )
    const el = screen.getByText('top tip')
    expect(el.className).toContain('bottom-full')
  })

  it('defaults to placement=right (absolute left-full)', () => {
    render(
      <div className="relative group">
        <HoverTooltip>r tip</HoverTooltip>
      </div>
    )
    const el = screen.getByText('r tip')
    expect(el.className).toContain('left-full')
  })

  it('includes fade-on-hover classes (opacity-0 + group-hover:opacity-100 + transition-opacity)', () => {
    render(
      <div className="relative group">
        <HoverTooltip>fade</HoverTooltip>
      </div>
    )
    const el = screen.getByText('fade')
    expect(el.className).toMatch(/\bopacity-0\b/)
    expect(el.className).toMatch(/\bgroup-hover:opacity-100\b/)
    expect(el.className).toMatch(/\btransition-opacity\b/)
  })
})
