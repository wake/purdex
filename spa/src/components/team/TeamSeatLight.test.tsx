// The seat light sits on a 16 px agent icon, so it is drawn like the tab rows' overlay dot on the same icon (TabStatusIndicator): the same size, ring width and place. Compared against the exported constants, not numbers.
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { OVERLAY_DOT, OVERLAY_ERROR, OVERLAY_HAND_RIGHT, OVERLAY_HAND_SIZE, OVERLAY_RING, SEAT_LIGHT_POSITION } from '../overlay-dot'
import { useI18nStore } from '../../stores/useI18nStore'
import { STATUS_COLORS } from '../status-colors'
import { TeamSeatLight } from './TeamSeatLight'

const indicator = vi.hoisted(() => ({ value: { agentStatus: 'idle', isUnread: false, tabIndicatorStyle: 'overlay' } as Record<string, unknown> }))
vi.mock('../../hooks/useSessionAgentIndicator', () => ({ useSessionAgentIndicator: () => indicator.value }))

afterEach(() => cleanup())

describe('TeamSeatLight', () => {
  it('is the tab row overlay dot\'s size, with its ring width', () => {
    render(<TeamSeatLight hostId="h" sessionCode="s" />)
    const dot = screen.getByTestId('team-panel-light-dot')
    expect(dot.style.width).toBe(`${OVERLAY_DOT.size}px`)
    expect(dot.style.height).toBe(`${OVERLAY_DOT.size}px`)
    expect(dot.style.boxShadow).toContain(`${OVERLAY_RING}px`)
    expect(dot.style.boxShadow).toContain('var(--seat-ring')
  })

  it('is placed where the tab row puts its dot: top and right of the icon box', () => {
    expect(SEAT_LIGHT_POSITION).toEqual({ top: OVERLAY_DOT.top, right: OVERLAY_DOT.right })
  })

  it('an error is the tab rows\' warning diamond, not a dot: its size, and its place against the same icon corner', () => {
    indicator.value = { agentStatus: 'error', isUnread: false, tabIndicatorStyle: 'overlay' }
    render(<TeamSeatLight hostId="h" sessionCode="s" />)
    expect(screen.queryByTestId('team-panel-light-dot')).toBeNull()
    const diamond = screen.getByTestId('team-panel-light-error')
    expect(diamond.getAttribute('width')).toBe(String(OVERLAY_ERROR.size))
    expect(diamond.getAttribute('height')).toBe(String(OVERLAY_ERROR.size))
    // the light's box already sits at the dot's place, so the diamond is offset by the difference between the two places
    expect(diamond.style.top).toBe(`${OVERLAY_ERROR.top - OVERLAY_DOT.top}px`)
    expect(diamond.style.right).toBe(`${OVERLAY_ERROR.right - OVERLAY_DOT.right}px`)
    expect(diamond.style.filter).toContain('var(--seat-ring')
    indicator.value = { agentStatus: 'idle', isUnread: false, tabIndicatorStyle: 'overlay' }
  })

  it('an error stays an error when unread, and the icon-only setting still shows nothing', () => {
    indicator.value = { agentStatus: 'error', isUnread: true, tabIndicatorStyle: 'overlay' }
    const { unmount } = render(<TeamSeatLight hostId="h" sessionCode="s" />)
    expect(screen.getByTestId('team-panel-light-error')).toBeInTheDocument()
    unmount()
    indicator.value = { agentStatus: 'error', isUnread: false, tabIndicatorStyle: 'icon' }
    render(<TeamSeatLight hostId="h" sessionCode="s" />)
    expect(screen.queryByTestId('team-panel-light-error')).toBeNull()
    indicator.value = { agentStatus: 'idle', isUnread: false, tabIndicatorStyle: 'overlay' }
  })

  it('keeps its rules: no dot without a status, none with the icon-only setting', () => {
    indicator.value = { agentStatus: undefined, isUnread: false, tabIndicatorStyle: 'overlay' }
    render(<TeamSeatLight hostId="h" sessionCode="s" />)
    expect(screen.queryByTestId('team-panel-light-dot')).toBeNull()
    cleanup()
    indicator.value = { agentStatus: 'idle', isUnread: false, tabIndicatorStyle: 'icon' }
    render(<TeamSeatLight hostId="h" sessionCode="s" />)
    expect(screen.queryByTestId('team-panel-light-dot')).toBeNull()
    indicator.value = { agentStatus: 'idle', isUnread: false, tabIndicatorStyle: 'overlay' }
  })

  it('unread tints red but never over a question or an error; running breathes', () => {
    indicator.value = { agentStatus: 'running', isUnread: true, tabIndicatorStyle: 'overlay' }
    const { unmount } = render(<TeamSeatLight hostId="h" sessionCode="s" />)
    expect(screen.getByTestId('team-panel-light-dot').className).not.toContain('animate-breathe')
    unmount()
    indicator.value = { agentStatus: 'waiting', isUnread: true, tabIndicatorStyle: 'overlay' }
    render(<TeamSeatLight hostId="h" sessionCode="s" />)
    expect(screen.getByTestId('team-panel-light-dot').style.backgroundColor).not.toBe('rgb(239, 68, 68)')
    indicator.value = { agentStatus: 'idle', isUnread: false, tabIndicatorStyle: 'overlay' }
  })

  // MON-3830: 「等待核准」 as the tab row draws it (TabStatusIndicator, overlay): a waiting dot, the hand to its left, the words as tooltip and accessible name.
  describe('awaiting approval', () => {
    const label = () => useI18nStore.getState().t('executions.activity.awaiting_approval')
    const set = (over: Record<string, unknown>) => { indicator.value = { agentStatus: 'idle', isUnread: false, tabIndicatorStyle: 'overlay', isAwaitingApproval: true, ...over } }
    afterEach(() => { indicator.value = { agentStatus: 'idle', isUnread: false, tabIndicatorStyle: 'overlay' } })

    it('draws a waiting dot and the hand, with 「等待核准」 as tooltip and accessible name', () => {
      set({ agentStatus: 'running' })
      render(<TeamSeatLight hostId="h" sessionCode="s" />)
      const wrap = screen.getByTestId('team-panel-light-awaiting')
      expect(wrap.getAttribute('title')).toBe(label())
      expect(wrap.getAttribute('aria-label')).toBe(label())
      expect(screen.getByTestId('team-panel-light-dot').getAttribute('data-status')).toBe('waiting')
      expect(screen.getByTestId('team-panel-light-dot').style.backgroundColor).not.toBe('')
      expect(screen.getByTestId('team-panel-light-hand')).toBeInTheDocument()
    })

    it('draws it even when agentStatus has not been written yet', () => {
      set({ agentStatus: undefined })
      render(<TeamSeatLight hostId="h" sessionCode="s" />)
      expect(screen.getByTestId('team-panel-light-hand')).toBeInTheDocument()
      expect(screen.getByTestId('team-panel-light-dot').getAttribute('data-status')).toBe('waiting')
    })

    it('the hand has the tab row\'s size and sits one pixel left of the dot, on its top line (the box is already at the dot\'s place)', () => {
      set({})
      render(<TeamSeatLight hostId="h" sessionCode="s" />)
      const hand = screen.getByTestId('team-panel-light-hand')
      expect(hand.getAttribute('width')).toBe(String(OVERLAY_HAND_SIZE))
      expect(hand.getAttribute('height')).toBe(String(OVERLAY_HAND_SIZE))
      expect(hand.style.top).toBe('0px')
      expect(hand.style.right).toBe(`${OVERLAY_HAND_RIGHT - OVERLAY_DOT.right}px`)
      expect(hand.getAttribute('color') ?? hand.style.color).toContain(STATUS_COLORS.waiting)
    })

    it('awaiting wins over an error, and unread never tints it (the dot stays the waiting colour)', () => {
      set({ agentStatus: 'error', isUnread: true })
      render(<TeamSeatLight hostId="h" sessionCode="s" />)
      expect(screen.queryByTestId('team-panel-light-error')).toBeNull()
      expect(screen.getByTestId('team-panel-light-dot').style.backgroundColor).not.toBe('rgb(239, 68, 68)')
    })

    it('lights off (icon-only): an awaiting worker still shows its light, as in the tab row, with no unread tint', () => {
      set({ tabIndicatorStyle: 'icon', agentStatus: 'running', isUnread: true })
      render(<TeamSeatLight hostId="h" sessionCode="s" />)
      expect(screen.getByTestId('team-panel-light-hand')).toBeInTheDocument()
      expect(screen.getByTestId('team-panel-light-dot').getAttribute('data-status')).toBe('waiting')
    })

    it('lights off and nothing awaiting: still nothing', () => {
      set({ tabIndicatorStyle: 'icon', isAwaitingApproval: false })
      render(<TeamSeatLight hostId="h" sessionCode="s" />)
      expect(screen.queryByTestId('team-panel-light-dot')).toBeNull()
      expect(screen.queryByTestId('team-panel-light-hand')).toBeNull()
    })

    it('not awaiting: no hand, no wrapper', () => {
      set({ isAwaitingApproval: false })
      render(<TeamSeatLight hostId="h" sessionCode="s" />)
      expect(screen.queryByTestId('team-panel-light-hand')).toBeNull()
      expect(screen.queryByTestId('team-panel-light-awaiting')).toBeNull()
    })
  })
})
