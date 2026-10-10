import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { DndContext, PointerSensor, useSensor, useSensors } from '@dnd-kit/core'
import { SortableContext } from '@dnd-kit/sortable'
import { InlineTab } from './InlineTab'
import type { Tab } from '../../../types/tab'

// Real DndContext / PointerSensor / useSortable (the sibling InlineTab.test.tsx mocks useSortable).
// The sensors are pointer-only (no KeyboardSensor), so dnd-kit's `attributes` would only add a fake
// "press space to pick up" description (aria-describedby / aria-roledescription) (#2531).
// The row keeps its own role=button + tabIndex=0 (it is the primary interactive element).

const tab = {
  id: 't1',
  kind: 'tmux-session',
  locked: false,
  layout: { type: 'leaf', pane: { id: 't1-pane', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'S1', terminated: false } } },
} as never as Tab

function Harness({ onDragStart, onSelect = () => {}, onClose = () => {} }: {
  onDragStart: (e: { active: { id: string | number } }) => void
  onSelect?: (id: string) => void
  onClose?: (id: string) => void
}) {
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))
  return (
    <DndContext sensors={sensors} onDragStart={onDragStart}>
      <SortableContext items={['t1']}>
        <InlineTab tab={tab} isActive={false} onSelect={onSelect} onClose={onClose} onMiddleClick={() => {}} onContextMenu={() => {}} />
      </SortableContext>
    </DndContext>
  )
}

afterEach(cleanup)

describe('InlineTab dnd-kit attributes (#2531)', () => {
  it('has no fake keyboard-drag description, but keeps its own role=button / tabindex=0', () => {
    render(<Harness onDragStart={() => {}} />)
    const row = screen.getByTestId('inline-tab-row')
    expect(row).not.toHaveAttribute('aria-roledescription')
    expect(row).not.toHaveAttribute('aria-describedby')
    expect(row).toHaveAttribute('role', 'button')
    expect(row).toHaveAttribute('tabindex', '0')
  })

  it('still starts a mouse drag: pointer-down + move fires onDragStart for this tab', async () => {
    const onDragStart = vi.fn()
    render(<Harness onDragStart={onDragStart} />)
    const row = screen.getByTestId('inline-tab-row')
    fireEvent.pointerDown(row, { button: 0, isPrimary: true, clientX: 10, clientY: 10 })
    fireEvent.pointerMove(document, { clientX: 10, clientY: 30 })
    expect(onDragStart).toHaveBeenCalledTimes(1)
    expect(onDragStart.mock.calls[0][0].active.id).toBe('t1')
    fireEvent.pointerUp(document)
    // dnd-kit swallows the next click for one macrotask after a drag.
    await new Promise((r) => setTimeout(r, 50))
    cleanup()
  })
})

describe('InlineTab keyboard select (#2538)', () => {
  it('Enter and Space on the row select the tab (Space is preventDefault-ed)', () => {
    const onSelect = vi.fn()
    render(<Harness onDragStart={() => {}} onSelect={onSelect} />)
    const row = screen.getByTestId('inline-tab-row')
    expect(fireEvent.keyDown(row, { key: 'Enter' })).toBe(false)
    expect(fireEvent.keyDown(row, { key: ' ' })).toBe(false)
    expect(onSelect).toHaveBeenCalledTimes(2)
    expect(onSelect).toHaveBeenNthCalledWith(1, 't1')
    expect(onSelect).toHaveBeenNthCalledWith(2, 't1')
  })

  it('other keys do not select', () => {
    const onSelect = vi.fn()
    render(<Harness onDragStart={() => {}} onSelect={onSelect} />)
    const row = screen.getByTestId('inline-tab-row')
    for (const key of ['a', 'Tab', 'Escape', 'ArrowDown']) fireEvent.keyDown(row, { key })
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('Enter / Space inside an inner control (close button) does not select the row', () => {
    const onSelect = vi.fn()
    render(<Harness onDragStart={() => {}} onSelect={onSelect} />)
    const close = screen.getByRole('button', { name: /^Close/ })
    fireEvent.keyDown(close, { key: 'Enter' })
    fireEvent.keyDown(close, { key: ' ' })
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('pointer drag is unaffected: no select from drag, drag still starts', async () => {
    const onSelect = vi.fn()
    const onDragStart = vi.fn()
    render(<Harness onDragStart={onDragStart} onSelect={onSelect} />)
    const row = screen.getByTestId('inline-tab-row')
    fireEvent.pointerDown(row, { button: 0, isPrimary: true, clientX: 10, clientY: 10 })
    fireEvent.pointerMove(document, { clientX: 10, clientY: 30 })
    expect(onDragStart).toHaveBeenCalledTimes(1)
    expect(onSelect).not.toHaveBeenCalled()
    fireEvent.pointerUp(document)
    await new Promise((r) => setTimeout(r, 50))
    cleanup()
  })
})
