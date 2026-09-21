import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { HomeRow } from './HomeRow'
import { useLayoutStore } from '../../../stores/useLayoutStore'

beforeEach(() => {
  cleanup()
  useLayoutStore.setState(useLayoutStore.getInitialState())
})

function renderRow(overrides: Partial<React.ComponentProps<typeof HomeRow>> = {}) {
  // No DndContext: the row is no drop target any more, and must not need one to render.
  return render(<HomeRow isActive={false} onSelectHome={() => {}} {...overrides} />)
}

describe('HomeRow', () => {
  it('renders Home label', () => {
    renderRow()
    expect(screen.getByText(/home/i)).toBeInTheDocument()
  })

  it('header click calls onSelectHome', () => {
    const onSelectHome = vi.fn()
    renderRow({ onSelectHome })
    fireEvent.click(screen.getByText(/home/i))
    expect(onSelectHome).toHaveBeenCalled()
  })

  // Every tab belongs to a workspace (Profile Sync spec §4.3): Home heads no tab list, so a click is a click
  // whatever the row's state or the tab position. (Was: active + inline tabs → the click toggled the list.)
  it.each(['top', 'left', 'both'] as const)("clicking an ACTIVE Home selects, and expands nothing (tabPosition='%s')", (tabPosition) => {
    useLayoutStore.setState({ tabPosition, activityBarWidth: 'wide' })
    const onSelectHome = vi.fn()
    renderRow({ isActive: true, onSelectHome })
    fireEvent.click(screen.getByText(/home/i))
    expect(onSelectHome).toHaveBeenCalledTimes(1)
    expect(useLayoutStore.getState().workspaceExpanded['home']).toBeFalsy()
  })

  it('clicking an INACTIVE Home selects', () => {
    useLayoutStore.setState({ tabPosition: 'left', activityBarWidth: 'wide' })
    const onSelectHome = vi.fn()
    renderRow({ isActive: false, onSelectHome })
    fireEvent.click(screen.getByText(/home/i))
    expect(onSelectHome).toHaveBeenCalledTimes(1)
  })

  it.each(['top', 'left', 'both'] as const)("is one plain button: no expand/collapse chevron (tabPosition='%s')", (tabPosition) => {
    useLayoutStore.setState({ tabPosition, activityBarWidth: 'wide', workspaceExpanded: { home: true } })
    renderRow()
    expect(screen.getAllByRole('button')).toHaveLength(1)
    expect(screen.queryByLabelText(/expand home|collapse home/i)).not.toBeInTheDocument()
  })

  it('keeps data-testid=home-header (P3d turns this button into the profile switcher)', () => {
    renderRow()
    expect(screen.getByTestId('home-header')).toBeInTheDocument()
  })
})
