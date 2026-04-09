import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { PaneHeader } from './PaneHeader'

describe('PaneHeader', () => {
  it('renders close button', () => {
    render(<PaneHeader title="Dashboard" onClose={vi.fn()} />)
    expect(screen.getByTitle('Close pane')).toBeTruthy()
  })

  it('calls onClose when close button clicked', () => {
    const onClose = vi.fn()
    render(<PaneHeader title="Dashboard" onClose={onClose} />)
    fireEvent.click(screen.getByTitle('Close pane'))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('renders detach button when onDetach is provided', () => {
    render(<PaneHeader title="Dashboard" onClose={vi.fn()} onDetach={vi.fn()} />)
    expect(screen.getByTitle('Detach to tab')).toBeTruthy()
  })

  it('does not render detach button when onDetach is not provided', () => {
    render(<PaneHeader title="Dashboard" onClose={vi.fn()} />)
    expect(screen.queryByTitle('Detach to tab')).toBeNull()
  })

  it('hides header when isSinglePane is true', () => {
    const { container } = render(
      <PaneHeader title="Dashboard" onClose={vi.fn()} isSinglePane={true} />
    )
    expect(container.innerHTML).toBe('')
  })
})
