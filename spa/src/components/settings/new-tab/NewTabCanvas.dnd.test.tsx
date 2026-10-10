import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { DndContext, PointerSensor, useSensor, useSensors } from '@dnd-kit/core'
import { NewTabCanvas } from './NewTabCanvas'
import { useNewTabLayoutStore } from '../../../stores/useNewTabLayoutStore'
import { clearNewTabRegistry, registerNewTabProvider } from '../../../lib/new-tab-registry'

// Real DndContext + PointerSensor (same sensors as NewTabSubsection: pointer-only, no KeyboardSensor), so dnd-kit's
// `attributes` would only add a fake "press space to pick up" description (#2538). The drag handle is a native <button>.

beforeEach(() => {
  useNewTabLayoutStore.setState(useNewTabLayoutStore.getInitialState(), true)
  clearNewTabRegistry()
  registerNewTabProvider({ id: 'a', label: 'a.label', icon: 'List', order: 0, component: () => null })
  useNewTabLayoutStore.getState().placeModule('1col', 'a', 0, 0)
})
afterEach(cleanup)

function Harness({ onDragStart }: { onDragStart: (e: { active: { id: string | number } }) => void }) {
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))
  return (
    <DndContext sensors={sensors} onDragStart={onDragStart}>
      <NewTabCanvas presetKey="1col" />
    </DndContext>
  )
}

describe('NewTabCanvas dnd-kit attributes (#2538)', () => {
  it('drag handle is a native button with no fake keyboard-drag description', () => {
    render(<Harness onDragStart={() => {}} />)
    const handle = screen.getByRole('button', { name: 'a.label' })
    expect(handle.tagName).toBe('BUTTON')
    expect(handle).not.toHaveAttribute('aria-roledescription')
    expect(handle).not.toHaveAttribute('aria-describedby')
  })

  it('the pointer-only drag handle is not a Tab stop (it has no keyboard action)', () => {
    render(<Harness onDragStart={() => {}} />)
    expect(screen.getByRole('button', { name: 'a.label' })).toHaveAttribute('tabindex', '-1')
    // The remove button stays reachable by keyboard.
    expect(screen.getByTestId('canvas-remove-1col-a')).not.toHaveAttribute('tabindex', '-1')
  })

  it('still starts a mouse drag: pointer-down + move fires onDragStart for this item', async () => {
    const onDragStart = vi.fn()
    render(<Harness onDragStart={onDragStart} />)
    fireEvent.pointerDown(screen.getByRole('button', { name: 'a.label' }), { button: 0, isPrimary: true, clientX: 10, clientY: 10 })
    fireEvent.pointerMove(document, { clientX: 10, clientY: 30 })
    expect(onDragStart).toHaveBeenCalledTimes(1)
    expect(onDragStart.mock.calls[0][0].active.id).toBe('item:1col:a')
    fireEvent.pointerUp(document)
    await new Promise((r) => setTimeout(r, 50))
    cleanup()
  })
})
