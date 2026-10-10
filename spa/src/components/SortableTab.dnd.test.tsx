import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, fireEvent, cleanup } from '@testing-library/react'
import { DndContext, PointerSensor, useSensor, useSensors } from '@dnd-kit/core'
import { SortableContext } from '@dnd-kit/sortable'
import { SortableTab } from './SortableTab'
import { createTab } from '../types/tab'

// Real DndContext / PointerSensor / useSortable (the sibling SortableTab.test.tsx mocks useSortable).
// The sensors are pointer-only (no KeyboardSensor), so dnd-kit's `attributes` would only add a fake
// "press space to pick up" description (aria-describedby / aria-roledescription) (#2531).
// The unpinned tab is role=tab and must stay focusable (its own onKeyDown selects it), so tabIndex=0 stays.

function mk(pinned: boolean) {
  const t = createTab(
    { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sc1', mode: 'terminal' as const, cachedName: '', tmuxInstance: '' },
    { pinned },
  )
  return { ...t, id: 't1' }
}

function Harness({ pinned, onDragStart }: { pinned: boolean; onDragStart: (e: { active: { id: string | number } }) => void }) {
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))
  return (
    <DndContext sensors={sensors} onDragStart={onDragStart}>
      <SortableContext items={['t1']}>
        <SortableTab
          tab={mk(pinned)}
          pinned={pinned}
          isActive={false}
          onSelect={() => {}}
          onClose={() => {}}
          onMiddleClick={() => {}}
          onContextMenu={() => {}}
        />
      </SortableContext>
    </DndContext>
  )
}

function el(container: HTMLElement) {
  return container.querySelector('[data-tab-id="t1"]') as HTMLElement
}

afterEach(cleanup)

describe.each([[false], [true]])('SortableTab dnd-kit attributes (#2531), pinned=%s', (pinned) => {
  it('has no fake keyboard-drag description', () => {
    const { container } = render(<Harness pinned={pinned} onDragStart={() => {}} />)
    expect(el(container)).not.toHaveAttribute('aria-roledescription')
    expect(el(container)).not.toHaveAttribute('aria-describedby')
  })

  it('still starts a mouse drag: pointer-down + move fires onDragStart for this tab', async () => {
    const onDragStart = vi.fn()
    const { container } = render(<Harness pinned={pinned} onDragStart={onDragStart} />)
    fireEvent.pointerDown(el(container), { button: 0, isPrimary: true, clientX: 10, clientY: 10 })
    fireEvent.pointerMove(document, { clientX: 30, clientY: 10 })
    expect(onDragStart).toHaveBeenCalledTimes(1)
    expect(onDragStart.mock.calls[0][0].active.id).toBe('t1')
    fireEvent.pointerUp(document)
    await new Promise((r) => setTimeout(r, 50))
    cleanup()
  })
})

describe('SortableTab focus semantics survive dropping `attributes` (#2531)', () => {
  it('unpinned tab keeps role=tab and tabindex=0 (keyboard select via onKeyDown needs focus)', () => {
    const { container } = render(<Harness pinned={false} onDragStart={() => {}} />)
    expect(el(container)).toHaveAttribute('role', 'tab')
    expect(el(container)).toHaveAttribute('tabindex', '0')
  })

  it('pinned tab is a native <button> (focusable by default, no redundant role)', () => {
    const { container } = render(<Harness pinned onDragStart={() => {}} />)
    expect(el(container).tagName).toBe('BUTTON')
    expect(el(container)).not.toHaveAttribute('role')
  })
})
