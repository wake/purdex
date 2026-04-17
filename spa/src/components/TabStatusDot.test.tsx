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
    // overlay: absolute positioned, 6px, nudged -1px up / -2px right
    expect(dot.style.width).toBe('6px')
    expect(dot.style.height).toBe('6px')
    expect(dot.style.position).toBe('absolute')
    expect(dot.style.top).toBe('-1px')
    expect(dot.style.right).toBe('-2px')
    // running = green
    expect(dot.style.backgroundColor).toBe('rgb(74, 222, 128)')
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

  it('overlay style: tints dot red when isUnread (not error)', () => {
    cleanup()
    render(
      <TabStatusDot status="idle" style="overlay" isActive={false} isUnread />,
    )
    const dot = screen.getByTestId('tab-status-dot')
    // Unread red #b91c1c
    expect(dot.style.backgroundColor).toBe('rgb(185, 28, 28)')
  })

  it('overlay style: renders warning-diamond instead of dot when status is error', () => {
    cleanup()
    render(
      <TabStatusDot status="error" style="overlay" isActive={false} />,
    )
    expect(screen.queryByTestId('tab-status-dot')).toBeNull()
    expect(screen.getByTestId('tab-status-error')).toBeTruthy()
  })

  it('replace style: renders warning-diamond instead of dot when status is error', () => {
    cleanup()
    render(
      <TabStatusDot status="error" style="replace" isActive={false} />,
    )
    expect(screen.queryByTestId('tab-status-dot')).toBeNull()
    expect(screen.getByTestId('tab-status-error')).toBeTruthy()
  })
})
