// spa/src/components/TabStatusDot.test.tsx
import { describe, it, expect } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { TabStatusDot } from './TabStatusDot'

describe('TabStatusDot', () => {
  it('renders nothing when status is undefined', () => {
    cleanup()
    const { container } = render(
      <TabStatusDot status={undefined} style="overlay" isActive={false} />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('renders dot for overlay style with running status', () => {
    cleanup()
    render(
      <TabStatusDot status="running" style="overlay" isActive={false} />,
    )
    const dot = screen.getByTestId('tab-status-dot')
    expect(dot).toBeTruthy()
    // overlay: absolute positioned, 6px
    expect(dot.style.width).toBe('6px')
    expect(dot.style.height).toBe('6px')
    expect(dot.style.position).toBe('absolute')
    // running = green
    expect(dot.style.getPropertyValue('--breathe-color')).toBe('#4ade80')
  })

  it('renders dot for replace style', () => {
    cleanup()
    render(
      <TabStatusDot status="waiting" style="replace" isActive={true} />,
    )
    const dot = screen.getByTestId('tab-status-dot')
    expect(dot).toBeTruthy()
    // replace: inline, 8px
    expect(dot.style.width).toBe('8px')
    expect(dot.style.height).toBe('8px')
    // waiting = yellow, no breathe animation
    expect(dot.style.backgroundColor).toBe('rgb(250, 204, 21)')
  })

  it('renders dot for inline style', () => {
    cleanup()
    render(
      <TabStatusDot status="idle" style="inline" isActive={false} />,
    )
    const dot = screen.getByTestId('tab-status-dot')
    expect(dot).toBeTruthy()
    // inline: 6px
    expect(dot.style.width).toBe('6px')
    expect(dot.style.height).toBe('6px')
    // idle = gray, no breathe animation
    expect(dot.style.backgroundColor).toBe('rgb(107, 114, 128)')
  })
})
