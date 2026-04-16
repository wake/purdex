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
})
