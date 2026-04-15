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

  describe('swap menu', () => {
    const swapTargets = [
      { id: 'pane-1', label: 'Terminal' },
      { id: 'pane-2', label: 'Editor' },
    ]

    function renderWithSwap() {
      const onSwap = vi.fn()
      render(
        <PaneHeader
          title="Dashboard"
          onClose={vi.fn()}
          onSwap={onSwap}
          swapTargets={swapTargets}
        />,
      )
      return { onSwap }
    }

    it('shows swap targets when toggle button clicked', () => {
      renderWithSwap()
      fireEvent.click(screen.getByTitle('Swap with...'))
      expect(screen.getByText('Terminal')).toBeTruthy()
      expect(screen.getByText('Editor')).toBeTruthy()
    })

    it('closes swap menu on click outside', () => {
      renderWithSwap()
      fireEvent.click(screen.getByTitle('Swap with...'))
      expect(screen.getByText('Terminal')).toBeTruthy()

      // Click outside the menu
      fireEvent.mouseDown(document.body)
      expect(screen.queryByText('Terminal')).toBeNull()
    })

    it('closes swap menu on Escape key', () => {
      renderWithSwap()
      fireEvent.click(screen.getByTitle('Swap with...'))
      expect(screen.getByText('Terminal')).toBeTruthy()

      fireEvent.keyDown(document, { key: 'Escape' })
      expect(screen.queryByText('Terminal')).toBeNull()
    })

    it('calls onSwap and closes menu when target selected', () => {
      const { onSwap } = renderWithSwap()
      fireEvent.click(screen.getByTitle('Swap with...'))
      fireEvent.click(screen.getByText('Terminal'))
      expect(onSwap).toHaveBeenCalledWith('pane-1')
      expect(screen.queryByText('Terminal')).toBeNull()
    })
  })

})
