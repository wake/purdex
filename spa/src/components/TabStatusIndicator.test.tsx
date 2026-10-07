// spa/src/components/TabStatusIndicator.test.tsx
import { describe, it, expect, afterEach } from 'vitest'
import { render, screen, cleanup, act } from '@testing-library/react'
import { HandPalm } from '@phosphor-icons/react'
import { TabStatusIndicator } from './TabStatusIndicator'
import { useI18nStore } from '../stores/useI18nStore'

/** The path data of a fill-weight HandPalm — identifies the glyph regardless of size/colour. */
function handPalmPath(): string | null {
  const { container, unmount } = render(<HandPalm weight="fill" />)
  const d = container.querySelector('path')?.getAttribute('d') ?? null
  unmount()
  return d
}

// Permission channel PC2 / spec §5.4, user decision 2026-10-08: on a tab, 「等待核准」 is the warning-coloured
// HandPalm where the light sits, with the existing `executions.activity.awaiting_approval` text as its tooltip
// and accessible name — not label text.
describe('TabStatusIndicator — awaiting approval', () => {
  afterEach(() => { act(() => { useI18nStore.getState().setLocale('en') }) })

  it('overlay: the HandPalm instead of the dot, warning colour, tooltip + aria-label (en)', () => {
    cleanup()
    act(() => { useI18nStore.getState().setLocale('en') })
    const expected = handPalmPath()
    render(<TabStatusIndicator status="waiting" mode="overlay" isActive={false} awaitingApproval />)
    expect(screen.queryByTestId('tab-status-indicator')).toBeNull()
    const hand = screen.getByTestId('tab-status-awaiting')
    expect(hand).toHaveAttribute('title', 'Awaiting approval')
    expect(hand).toHaveAttribute('aria-label', 'Awaiting approval')
    expect(hand.style.position).toBe('absolute')
    const svg = hand.querySelector('svg')!
    expect(svg.getAttribute('width')).toBe('10')
    expect(svg.getAttribute('fill')).toBe('#facc15')
    expect(svg.querySelector('path')!.getAttribute('d')).toBe(expected)
  })

  it('overlay, zh-TW: tooltip + aria-label read 等待核准', () => {
    cleanup()
    act(() => { useI18nStore.getState().setLocale('zh-TW') })
    render(<TabStatusIndicator status="waiting" mode="overlay" isActive awaitingApproval />)
    const hand = screen.getByTestId('tab-status-awaiting')
    expect(hand).toHaveAttribute('title', '等待核准')
    expect(hand).toHaveAttribute('aria-label', '等待核准')
  })

  it('overlay + unread: the hand keeps its warning colour (a tab that needs an answer does not turn red)', () => {
    cleanup()
    render(<TabStatusIndicator status="waiting" mode="overlay" isActive={false} isUnread awaitingApproval />)
    expect(screen.getByTestId('tab-status-awaiting').querySelector('svg')!.getAttribute('fill')).toBe('#facc15')
  })

  it('replace: the HandPalm instead of the dot, tooltip + aria-label (en, then zh-TW)', () => {
    cleanup()
    act(() => { useI18nStore.getState().setLocale('en') })
    const expected = handPalmPath()
    const { unmount } = render(<TabStatusIndicator status="waiting" mode="replace" isActive={false} awaitingApproval />)
    expect(screen.queryByTestId('tab-status-indicator')).toBeNull()
    const hand = screen.getByTestId('tab-status-awaiting')
    expect(hand).toHaveAttribute('title', 'Awaiting approval')
    expect(hand).toHaveAttribute('aria-label', 'Awaiting approval')
    expect(hand.style.position).not.toBe('absolute')
    const svg = hand.querySelector('svg')!
    expect(svg.getAttribute('fill')).toBe('#facc15')
    expect(svg.querySelector('path')!.getAttribute('d')).toBe(expected)
    unmount()
    act(() => { useI18nStore.getState().setLocale('zh-TW') })
    render(<TabStatusIndicator status="waiting" mode="replace" isActive={false} awaitingApproval />)
    expect(screen.getByTestId('tab-status-awaiting')).toHaveAttribute('title', '等待核准')
    expect(screen.getByTestId('tab-status-awaiting')).toHaveAttribute('aria-label', '等待核准')
  })

  it.each(['overlay', 'replace'] as const)('%s: a plain `waiting` (a terminal agent) keeps the yellow dot, no hand', (mode) => {
    cleanup()
    render(<TabStatusIndicator status="waiting" mode={mode} isActive={false} />)
    const dot = screen.getByTestId('tab-status-indicator')
    expect(dot.style.backgroundColor).toBe('rgb(250, 204, 21)')
    expect(dot).not.toHaveAttribute('title')
    expect(screen.queryByTestId('tab-status-awaiting')).toBeNull()
  })

  it('awaitingApproval={false} is the default rendering', () => {
    cleanup()
    render(<TabStatusIndicator status="waiting" mode="overlay" isActive={false} awaitingApproval={false} />)
    expect(screen.getByTestId('tab-status-indicator')).toBeTruthy()
    expect(screen.queryByTestId('tab-status-awaiting')).toBeNull()
  })

  it('no status: nothing, even when awaiting (the tab has no light to replace)', () => {
    cleanup()
    const { container } = render(<TabStatusIndicator status={undefined} mode="overlay" isActive={false} awaitingApproval />)
    expect(container.firstChild).toBeNull()
  })
})

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
    expect(dot.style.backgroundColor).toBe('rgb(239, 68, 68)')
  })

  it('overlay mode: running animates breathe when not unread', () => {
    cleanup()
    render(
      <TabStatusIndicator status="running" mode="overlay" isActive={false} />,
    )
    const dot = screen.getByTestId('tab-status-indicator')
    expect(dot.className).toContain('animate-breathe')
  })

  it('overlay mode: unread dot stays static (no animate-breathe)', () => {
    // Component-level invariant: unread tint is informational, not a call-to-
    // action — it must not pulse. The store layer also clears unread on
    // running, but the indicator must not breathe even if both flags arrive.
    cleanup()
    render(
      <TabStatusIndicator status="running" mode="overlay" isActive={false} isUnread />,
    )
    const dot = screen.getByTestId('tab-status-indicator')
    expect(dot.className).not.toContain('animate-breathe')
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
