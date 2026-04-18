import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { CollapseButton } from './CollapseButton'
import { useLayoutStore } from '../../../stores/useLayoutStore'

beforeEach(() => {
  useLayoutStore.setState(useLayoutStore.getInitialState())
})

describe('CollapseButton', () => {
  it('shows expand tooltip when narrow', () => {
    render(<CollapseButton />)
    const btn = screen.getByRole('button')
    expect(btn).toHaveAttribute('title', expect.stringMatching(/expand/i))
  })

  it('shows collapse tooltip when wide', () => {
    useLayoutStore.setState({ activityBarWidth: 'wide' })
    render(<CollapseButton />)
    const btn = screen.getByRole('button')
    expect(btn).toHaveAttribute('title', expect.stringMatching(/collapse/i))
  })

  it('click toggles width when tabPosition=top', () => {
    render(<CollapseButton />)
    fireEvent.click(screen.getByRole('button'))
    expect(useLayoutStore.getState().activityBarWidth).toBe('wide')
    fireEvent.click(screen.getByRole('button'))
    expect(useLayoutStore.getState().activityBarWidth).toBe('narrow')
  })

  it('is disabled when tabPosition=left', () => {
    useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'left' })
    render(<CollapseButton />)
    const btn = screen.getByRole('button')
    expect(btn).toBeDisabled()
    expect(btn.getAttribute('title')).toMatch(/locked|left/i)
  })

  it('reflects wide/narrow state via aria-pressed', () => {
    const { unmount } = render(<CollapseButton />)
    expect(screen.getByRole('button')).toHaveAttribute('aria-pressed', 'false')
    unmount()
    useLayoutStore.setState({ activityBarWidth: 'wide' })
    render(<CollapseButton />)
    expect(screen.getByRole('button')).toHaveAttribute('aria-pressed', 'true')
  })

  it('locked state does not include cursor-pointer class', () => {
    useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'left' })
    render(<CollapseButton />)
    const btn = screen.getByRole('button')
    expect(btn.className).not.toMatch(/\bcursor-pointer\b/)
    expect(btn.className).toMatch(/\bcursor-not-allowed\b/)
  })
})

describe('CollapseButton — variants', () => {
  it("defaults to data-variant='header-right'", () => {
    render(<CollapseButton />)
    expect(screen.getByRole('button')).toHaveAttribute('data-variant', 'header-right')
  })

  it("renders data-variant='divider'", () => {
    render(<CollapseButton variant="divider" />)
    expect(screen.getByRole('button')).toHaveAttribute('data-variant', 'divider')
  })

  it("renders data-variant='topbar'", () => {
    render(<CollapseButton variant="topbar" />)
    expect(screen.getByRole('button')).toHaveAttribute('data-variant', 'topbar')
  })

  it("is enabled when tabPosition='both' (top tabs remain reachable)", () => {
    useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'both' })
    render(<CollapseButton />)
    const btn = screen.getByRole('button')
    expect(btn).not.toBeDisabled()
    fireEvent.click(btn)
    expect(useLayoutStore.getState().activityBarWidth).toBe('narrow')
  })
})

describe('CollapseButton — icon', () => {
  // SidebarSimple regular path has this distinctive opening signature (a
  // 216×176 rounded rect with a vertical divider near the left). Caret icons
  // don't share it — this assertion proves we switched to SidebarSimple
  // rather than any Caret variant.
  const SIDEBAR_SIMPLE_SIGNATURE = /M216,40H40/

  it('renders the SidebarSimple icon when narrow', () => {
    render(<CollapseButton />)
    const path = screen.getByRole('button').querySelector('svg path')
    expect(path?.getAttribute('d')).toMatch(SIDEBAR_SIMPLE_SIGNATURE)
  })

  it('renders the SidebarSimple icon when wide', () => {
    useLayoutStore.setState({ activityBarWidth: 'wide' })
    render(<CollapseButton />)
    const path = screen.getByRole('button').querySelector('svg path')
    expect(path?.getAttribute('d')).toMatch(SIDEBAR_SIMPLE_SIGNATURE)
  })
})

describe('CollapseButton — topbar variant active state', () => {
  // Matches the visual treatment of the region-toggle buttons in TitleBar's
  // right cluster: accent tint when the region is "visible" (here: activity
  // bar is wide), neutral secondary styling otherwise.
  it('shows accent colors when wide (active)', () => {
    useLayoutStore.setState({ activityBarWidth: 'wide' })
    render(<CollapseButton variant="topbar" />)
    const btn = screen.getByRole('button')
    expect(btn.className).toMatch(/text-accent-base/)
    expect(btn.className).toMatch(/bg-accent-base/)
  })

  it('shows secondary colors when narrow (inactive)', () => {
    render(<CollapseButton variant="topbar" />)
    const btn = screen.getByRole('button')
    expect(btn.className).toMatch(/text-text-secondary/)
    expect(btn.className).not.toMatch(/text-accent-base/)
  })

  it('uses the p-1 rounded pattern shared with region toggles', () => {
    render(<CollapseButton variant="topbar" />)
    const btn = screen.getByRole('button')
    expect(btn.className).toMatch(/\bp-1\b/)
    expect(btn.className).toMatch(/\brounded\b/)
  })
})
