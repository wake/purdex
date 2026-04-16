import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { NewTabCanvas } from './NewTabCanvas'
import { useNewTabLayoutStore } from '../../../stores/useNewTabLayoutStore'
import { clearNewTabRegistry, registerNewTabProvider } from '../../../lib/new-tab-registry'

beforeEach(() => {
  useNewTabLayoutStore.setState(useNewTabLayoutStore.getInitialState(), true)
  clearNewTabRegistry()
  const Dummy = () => null
  registerNewTabProvider({ id: 'a', label: 'a.label', icon: 'List', order: 0, component: Dummy })
  registerNewTabProvider({ id: 'b', label: 'b.label', icon: 'List', order: 1, component: Dummy })
})

function wrap(ui: React.ReactElement) {
  return <DndContext>{ui}</DndContext>
}

describe('NewTabCanvas', () => {
  it('renders the correct number of columns for each profile', () => {
    const { rerender } = render(wrap(<NewTabCanvas profileKey="3col" />))
    expect(screen.getAllByTestId(/^canvas-column-3col-\d+$/)).toHaveLength(3)
    rerender(wrap(<NewTabCanvas profileKey="2col" />))
    expect(screen.getAllByTestId(/^canvas-column-2col-\d+$/)).toHaveLength(2)
    rerender(wrap(<NewTabCanvas profileKey="1col" />))
    expect(screen.getAllByTestId(/^canvas-column-1col-\d+$/)).toHaveLength(1)
  })

  it('renders items placed in profile', () => {
    useNewTabLayoutStore.getState().placeModule('1col', 'a', 0, 0)
    useNewTabLayoutStore.getState().placeModule('1col', 'b', 0, 1)
    render(wrap(<NewTabCanvas profileKey="1col" />))
    expect(screen.getByTestId('canvas-item-1col-a')).toBeInTheDocument()
    expect(screen.getByTestId('canvas-item-1col-b')).toBeInTheDocument()
  })

  it('shows empty placeholder for columns with no items', () => {
    render(wrap(<NewTabCanvas profileKey="3col" />))
    expect(screen.getAllByTestId(/^canvas-column-empty-3col-\d+$/)).toHaveLength(3)
  })

  it('remove button calls store.removeModule', () => {
    useNewTabLayoutStore.getState().placeModule('1col', 'a', 0, 0)
    render(wrap(<NewTabCanvas profileKey="1col" />))
    fireEvent.click(screen.getByTestId('canvas-remove-1col-a'))
    expect(useNewTabLayoutStore.getState().profiles['1col'].columns[0]).not.toContain('a')
  })

  it('skips unknown provider ids silently (does not throw)', () => {
    useNewTabLayoutStore.getState().placeModule('1col', 'ghost', 0, 0)
    expect(() => render(wrap(<NewTabCanvas profileKey="1col" />))).not.toThrow()
    expect(screen.queryByTestId('canvas-item-1col-ghost')).not.toBeInTheDocument()
  })
})
