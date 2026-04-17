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

  it("is disabled when tabPosition='both'", () => {
    useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'both' })
    render(<CollapseButton />)
    expect(screen.getByRole('button')).toBeDisabled()
  })
})
