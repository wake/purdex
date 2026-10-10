import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { DndContext, PointerSensor, useSensor, useSensors } from '@dnd-kit/core'
import { NewTabModulePalette } from './NewTabModulePalette'

// Real DndContext + PointerSensor (pointer-only, as in NewTabSubsection). The chip is a native <button> (#2538).

const items = [{ id: 'a', label: 'provider.a', inUse: false }]

function Harness({ onDragStart }: { onDragStart: (e: { active: { id: string | number } }) => void }) {
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))
  return (
    <DndContext sensors={sensors} onDragStart={onDragStart}>
      <NewTabModulePalette items={items} onClickAdd={() => {}} />
    </DndContext>
  )
}

afterEach(cleanup)

describe('NewTabModulePalette dnd-kit attributes (#2538)', () => {
  it('chip is a native button with no fake keyboard-drag description', () => {
    render(<Harness onDragStart={() => {}} />)
    const chip = screen.getByTestId('palette-chip-a')
    expect(chip.tagName).toBe('BUTTON')
    expect(chip).not.toHaveAttribute('aria-roledescription')
    expect(chip).not.toHaveAttribute('aria-describedby')
  })

  it('still starts a mouse drag: pointer-down + move fires onDragStart for this chip', async () => {
    const onDragStart = vi.fn()
    render(<Harness onDragStart={onDragStart} />)
    fireEvent.pointerDown(screen.getByTestId('palette-chip-a'), { button: 0, isPrimary: true, clientX: 10, clientY: 10 })
    fireEvent.pointerMove(document, { clientX: 10, clientY: 30 })
    expect(onDragStart).toHaveBeenCalledTimes(1)
    expect(onDragStart.mock.calls[0][0].active.id).toBe('palette:a')
    fireEvent.pointerUp(document)
    await new Promise((r) => setTimeout(r, 50))
    cleanup()
  })
})
