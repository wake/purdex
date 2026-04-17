// spa/src/components/TabStatusIndicator.test.tsx
import { describe, it, expect } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { TabStatusIndicator } from './TabStatusIndicator'

describe('TabStatusIndicator', () => {
  it('renders nothing when status is undefined', () => {
    cleanup()
    const { container } = render(
      <TabStatusIndicator status={undefined} mode="overlay" isActive={false} />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('renders dot for overlay mode with running status', () => {
    cleanup()
    render(
      <TabStatusIndicator status="running" mode="overlay" isActive={false} />,
    )
    const dot = screen.getByTestId('tab-status-indicator')
    expect(dot).toBeTruthy()
    expect(dot.style.width).toBe('6px')
    expect(dot.style.height).toBe('6px')
    expect(dot.style.position).toBe('absolute')
    expect(dot.style.top).toBe('-1px')
    expect(dot.style.right).toBe('-2px')
    expect(dot.style.backgroundColor).toBe('rgb(74, 222, 128)')
  })

  it('renders dot for replace mode', () => {
    cleanup()
    render(
      <TabStatusIndicator status="waiting" mode="replace" isActive={true} />,
    )
    const dot = screen.getByTestId('tab-status-indicator')
    expect(dot).toBeTruthy()
    expect(dot.style.width).toBe('8px')
    expect(dot.style.height).toBe('8px')
    expect(dot.style.backgroundColor).toBe('rgb(250, 204, 21)')
  })

  it('overlay mode: tints dot red when isUnread (not error)', () => {
    cleanup()
    render(
      <TabStatusIndicator status="idle" mode="overlay" isActive={false} isUnread />,
    )
    const dot = screen.getByTestId('tab-status-indicator')
    expect(dot.style.backgroundColor).toBe('rgb(185, 28, 28)')
  })

  it('overlay mode: renders warning-diamond instead of dot when status is error', () => {
    cleanup()
    render(
      <TabStatusIndicator status="error" mode="overlay" isActive={false} />,
    )
    expect(screen.queryByTestId('tab-status-indicator')).toBeNull()
    expect(screen.getByTestId('tab-status-error')).toBeTruthy()
  })

  it('replace mode: renders warning-diamond instead of dot when status is error', () => {
    cleanup()
    render(
      <TabStatusIndicator status="error" mode="replace" isActive={false} />,
    )
    expect(screen.queryByTestId('tab-status-indicator')).toBeNull()
    expect(screen.getByTestId('tab-status-error')).toBeTruthy()
  })
})
