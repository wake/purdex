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

// Permission channel PC2 / spec §5.4, user decision 2026-10-08: on a tab, 「等待核准」 is a warning-coloured
// HandPalm at the light, with the existing `executions.activity.awaiting_approval` text as its tooltip and
// accessible name — not label text. Coordinator ruling (PR #1792 review): the light itself does not move or
// resize when a request arrives or is answered — overlay keeps the waiting dot exactly and adds the hand to its
// left; replace draws the hand inside the dot's own 8×8 slot.
describe('TabStatusIndicator — awaiting approval', () => {
  afterEach(() => { act(() => { useI18nStore.getState().setLocale('en') }) })

  it('overlay: the waiting dot stays, the HandPalm sits beside it, tooltip + aria-label on the wrapper (en)', () => {
    cleanup()
    act(() => { useI18nStore.getState().setLocale('en') })
    const expected = handPalmPath()
    render(<TabStatusIndicator status="waiting" mode="overlay" isActive={false} awaitingApproval />)
    const wrapper = screen.getByTestId('tab-status-awaiting')
    expect(wrapper).toHaveAttribute('role', 'img')
    expect(wrapper).toHaveAttribute('title', 'Awaiting approval')
    expect(wrapper).toHaveAttribute('aria-label', 'Awaiting approval')
    // The wrapper generates no box: both children keep positioning against the icon slot like the bare dot.
    expect(wrapper.style.display).toBe('contents')
    const dot = screen.getByTestId('tab-status-indicator')
    const hand = screen.getByTestId('tab-status-awaiting-hand')
    expect(wrapper.contains(dot)).toBe(true)
    expect(wrapper.contains(hand)).toBe(true)
    expect(dot.style.backgroundColor).toBe('rgb(250, 204, 21)')
    expect(hand.getAttribute('fill')).toBe('#facc15')
    expect(hand.querySelector('path')!.getAttribute('d')).toBe(expected)
  })

  it('overlay, zh-TW: tooltip + aria-label read 等待核准', () => {
    cleanup()
    act(() => { useI18nStore.getState().setLocale('zh-TW') })
    render(<TabStatusIndicator status="waiting" mode="overlay" isActive awaitingApproval />)
    const wrapper = screen.getByTestId('tab-status-awaiting')
    expect(wrapper).toHaveAttribute('title', '等待核准')
    expect(wrapper).toHaveAttribute('aria-label', '等待核准')
  })

  it.each([false, true])('overlay (isActive=%s): a request arriving / being answered never moves, resizes or recolours the dot', (isActive) => {
    cleanup()
    const ring = isActive ? 'var(--surface-active)' : 'var(--surface-secondary)'
    const { rerender } = render(<TabStatusIndicator status="waiting" mode="overlay" isActive={isActive} />)
    const plain = screen.getByTestId('tab-status-indicator')
    const plainStyle = plain.getAttribute('style')
    const plainClass = plain.className
    expect(screen.queryByTestId('tab-status-awaiting-hand')).toBeNull()

    rerender(<TabStatusIndicator status="waiting" mode="overlay" isActive={isActive} awaitingApproval />)
    const dot = screen.getByTestId('tab-status-indicator')
    expect(dot.getAttribute('style')).toBe(plainStyle)
    expect(dot.className).toBe(plainClass)
    expect(dot.style.width).toBe('6px')
    expect(dot.style.height).toBe('6px')
    expect(dot.style.position).toBe('absolute')
    expect(dot.style.top).toBe('-1px')
    expect(dot.style.right).toBe('-2px')
    expect(dot.style.boxShadow).toBe(`0 0 0 1.5px ${ring}`)
    expect(dot.style.backgroundColor).toBe('rgb(250, 204, 21)')

    // The hand: 8px, immediately left of the dot (dot right + dot width + 1px), same top, same ring colour.
    const hand = screen.getByTestId('tab-status-awaiting-hand')
    expect(hand.getAttribute('width')).toBe('8')
    expect(hand.getAttribute('height')).toBe('8')
    expect(hand.style.position).toBe('absolute')
    expect(hand.style.top).toBe('-1px')
    expect(hand.style.right).toBe('5px')
    expect(hand.style.filter).toBe(`drop-shadow(0 0 1px ${ring})`)

    rerender(<TabStatusIndicator status="waiting" mode="overlay" isActive={isActive} />)
    expect(screen.getByTestId('tab-status-indicator').getAttribute('style')).toBe(plainStyle)
    expect(screen.queryByTestId('tab-status-awaiting-hand')).toBeNull()
    expect(screen.queryByTestId('tab-status-awaiting')).toBeNull()
  })

  it('overlay + unread: a waiting dot stays yellow (unread never overrides ask); the hand keeps the warning colour', () => {
    cleanup()
    const { rerender } = render(<TabStatusIndicator status="waiting" mode="overlay" isActive={false} isUnread />)
    const plainStyle = screen.getByTestId('tab-status-indicator').getAttribute('style')
    rerender(<TabStatusIndicator status="waiting" mode="overlay" isActive={false} isUnread awaitingApproval />)
    const dot = screen.getByTestId('tab-status-indicator')
    expect(dot.getAttribute('style')).toBe(plainStyle)
    expect(dot.style.backgroundColor).toBe('rgb(250, 204, 21)')
    expect(screen.getByTestId('tab-status-awaiting-hand').getAttribute('fill')).toBe('#facc15')
  })

  it('replace: the HandPalm inside the dot\'s own 8×8 slot, tooltip + aria-label (en, then zh-TW)', () => {
    cleanup()
    act(() => { useI18nStore.getState().setLocale('en') })
    const expected = handPalmPath()
    const { unmount } = render(<TabStatusIndicator status="waiting" mode="replace" isActive={false} awaitingApproval />)
    expect(screen.queryByTestId('tab-status-indicator')).toBeNull()
    const slot = screen.getByTestId('tab-status-awaiting')
    expect(slot).toHaveAttribute('role', 'img')
    expect(slot).toHaveAttribute('title', 'Awaiting approval')
    expect(slot).toHaveAttribute('aria-label', 'Awaiting approval')
    expect(slot.style.position).not.toBe('absolute')
    const hand = screen.getByTestId('tab-status-awaiting-hand')
    expect(slot.contains(hand)).toBe(true)
    expect(hand.getAttribute('fill')).toBe('#facc15')
    expect(hand.querySelector('path')!.getAttribute('d')).toBe(expected)
    unmount()
    act(() => { useI18nStore.getState().setLocale('zh-TW') })
    render(<TabStatusIndicator status="waiting" mode="replace" isActive={false} awaitingApproval />)
    expect(screen.getByTestId('tab-status-awaiting')).toHaveAttribute('title', '等待核准')
    expect(screen.getByTestId('tab-status-awaiting')).toHaveAttribute('aria-label', '等待核准')
  })

  it.each([false, true])('replace (isActive=%s): a request arriving / being answered keeps the 8×8 slot', (isActive) => {
    cleanup()
    const { rerender } = render(<TabStatusIndicator status="waiting" mode="replace" isActive={isActive} />)
    const plain = screen.getByTestId('tab-status-indicator')
    const { width, height } = plain.style
    expect([width, height]).toEqual(['8px', '8px'])
    expect(plain.className).toContain('flex-shrink-0')
    expect(screen.queryByTestId('tab-status-awaiting-hand')).toBeNull()

    rerender(<TabStatusIndicator status="waiting" mode="replace" isActive={isActive} awaitingApproval />)
    const slot = screen.getByTestId('tab-status-awaiting')
    expect(slot.style.width).toBe(width)
    expect(slot.style.height).toBe(height)
    expect(slot.className).toContain('flex-shrink-0')
    const hand = screen.getByTestId('tab-status-awaiting-hand')
    expect(hand.getAttribute('width')).toBe('8')
    expect(hand.getAttribute('height')).toBe('8')

    rerender(<TabStatusIndicator status="waiting" mode="replace" isActive={isActive} />)
    expect(screen.getByTestId('tab-status-indicator').style.width).toBe(width)
    expect(screen.queryByTestId('tab-status-awaiting-hand')).toBeNull()
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

  // The summary can carry the pending request before useWorkerAgentProjection has written a status (first paint
  // after a cold load), or while the projection still says something else: awaiting is a waiting light regardless.
  it.each(['overlay', 'replace'] as const)('%s: awaiting with no status yet still shows the awaiting light', (mode) => {
    cleanup()
    render(<TabStatusIndicator status={undefined} mode={mode} isActive={false} awaitingApproval />)
    expect(screen.getByTestId('tab-status-awaiting')).toHaveAttribute('title', 'Awaiting approval')
    expect(screen.getByTestId('tab-status-awaiting-hand')).toBeTruthy()
  })

  it.each(['running', 'idle', 'error'] as const)('overlay: awaiting while the projection still says %s → drawn as waiting', (status) => {
    cleanup()
    render(<TabStatusIndicator status={status} mode="overlay" isActive={false} awaitingApproval />)
    expect(screen.queryByTestId('tab-status-error')).toBeNull()
    const dot = screen.getByTestId('tab-status-indicator')
    expect(dot.style.backgroundColor).toBe('rgb(250, 204, 21)')
    expect(dot.className).not.toContain('animate-breathe')
    expect(screen.getByTestId('tab-status-awaiting-hand')).toBeTruthy()
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

  it('overlay mode: waiting + unread stays yellow', () => {
    cleanup()
    render(<TabStatusIndicator status="waiting" mode="overlay" isActive={false} isUnread />)
    expect(screen.getByTestId('tab-status-indicator').style.backgroundColor).toBe('rgb(250, 204, 21)')
  })

  it('overlay mode: running + unread is red', () => {
    cleanup()
    render(<TabStatusIndicator status="running" mode="overlay" isActive={false} isUnread />)
    expect(screen.getByTestId('tab-status-indicator').style.backgroundColor).toBe('rgb(239, 68, 68)')
  })

  it('replace mode: waiting + unread keeps the yellow dot', () => {
    cleanup()
    render(<TabStatusIndicator status="waiting" mode="replace" isActive={false} isUnread />)
    expect(screen.getByTestId('tab-status-indicator').style.backgroundColor).toBe('rgb(250, 204, 21)')
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
