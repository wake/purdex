// The seat light sits on a 16 px agent icon, so it is drawn like the tab rows' overlay dot on the same icon (TabStatusIndicator): the same size, ring width and place. Compared against the exported constants, not numbers.
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { OVERLAY_DOT, OVERLAY_RING, SEAT_LIGHT_POSITION } from '../overlay-dot'
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
})
