import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { NewTabModulePalette } from './NewTabModulePalette'

function renderWithDnd(ui: React.ReactElement) {
  return render(<DndContext>{ui}</DndContext>)
}

describe('NewTabModulePalette', () => {
  const items = [
    { id: 'a', label: 'provider.a', inUse: false },
    { id: 'b', label: 'provider.b', inUse: true },
    { id: 'c', label: 'provider.c', inUse: false, unavailable: true },
  ]

  it('renders a chip per item', () => {
    renderWithDnd(<NewTabModulePalette items={items} onClickAdd={() => {}} />)
    expect(screen.getByTestId('palette-chip-a')).toBeInTheDocument()
    expect(screen.getByTestId('palette-chip-b')).toBeInTheDocument()
    expect(screen.getByTestId('palette-chip-c')).toBeInTheDocument()
  })

  it('marks inUse chips as disabled-looking and non-clickable for add', () => {
    const onClickAdd = vi.fn()
    renderWithDnd(<NewTabModulePalette items={items} onClickAdd={onClickAdd} />)
    fireEvent.click(screen.getByTestId('palette-chip-b'))
    expect(onClickAdd).not.toHaveBeenCalled()
  })

  it('fires onClickAdd for available, not-in-use chips', () => {
    const onClickAdd = vi.fn()
    renderWithDnd(<NewTabModulePalette items={items} onClickAdd={onClickAdd} />)
    fireEvent.click(screen.getByTestId('palette-chip-a'))
    expect(onClickAdd).toHaveBeenCalledWith('a')
  })

  it('marks unavailable chips with an "unavailable" data attribute', () => {
    renderWithDnd(<NewTabModulePalette items={items} onClickAdd={() => {}} />)
    expect(screen.getByTestId('palette-chip-c')).toHaveAttribute('data-unavailable', 'true')
  })
})
