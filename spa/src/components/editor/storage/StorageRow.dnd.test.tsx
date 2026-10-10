import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { DndContext, PointerSensor, useSensor, useSensors } from '@dnd-kit/core'
import { StorageRow } from './StorageRow'
import type { TreeNode } from '../../../lib/storage-tree'
import type { FsBackend } from '../../../lib/fs-backend'

vi.mock('../../../lib/fs-backend', () => ({
  getFsBackend: () => ({ read: vi.fn().mockResolvedValue(new Uint8Array(0)) }) as unknown as FsBackend,
  registerFsBackend: vi.fn(),
}))

// Real DndContext + PointerSensor (pointer-only, as in StoragePane). The row is a div that sets its own role=button +
// tabIndex=0 and handles Enter/Space via onKeyDown, so it stays focusable without dnd-kit `attributes` (#2538).

const node: TreeNode = { path: '/buffer/note.md', name: 'note.md', isDir: false, size: 4 }

function Harness({ onDragStart, onSelect = () => {} }: { onDragStart: (e: { active: { id: string | number } }) => void; onSelect?: () => void }) {
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }))
  return (
    <DndContext sensors={sensors} onDragStart={onDragStart}>
      <StorageRow
        node={node} depth={0} selected={false} expanded={false}
        onToggle={() => {}} onSelect={onSelect} onOpen={() => {}} onRename={() => {}} onDelete={() => {}} onToggleSelect={() => {}}
      />
    </DndContext>
  )
}

afterEach(cleanup)

describe('StorageRow dnd-kit attributes (#2538)', () => {
  it('has no fake keyboard-drag description, but keeps its own role=button / tabindex=0', () => {
    render(<Harness onDragStart={() => {}} />)
    const row = screen.getByTestId('buffer-row')
    expect(row).not.toHaveAttribute('aria-roledescription')
    expect(row).not.toHaveAttribute('aria-describedby')
    expect(row).toHaveAttribute('role', 'button')
    expect(row).toHaveAttribute('tabindex', '0')
  })

  it('Space still selects via the row onKeyDown', () => {
    const onSelect = vi.fn()
    render(<Harness onDragStart={() => {}} onSelect={onSelect} />)
    fireEvent.keyDown(screen.getByTestId('buffer-row'), { key: ' ' })
    expect(onSelect).toHaveBeenCalledWith('/buffer/note.md', false)
  })

  it('still starts a mouse drag: pointer-down + move fires onDragStart for this row', async () => {
    const onDragStart = vi.fn()
    render(<Harness onDragStart={onDragStart} />)
    fireEvent.pointerDown(screen.getByTestId('buffer-row'), { button: 0, isPrimary: true, clientX: 10, clientY: 10 })
    fireEvent.pointerMove(document, { clientX: 10, clientY: 30 })
    expect(onDragStart).toHaveBeenCalledTimes(1)
    expect(onDragStart.mock.calls[0][0].active.id).toBe('/buffer/note.md')
    fireEvent.pointerUp(document)
    await new Promise((r) => setTimeout(r, 50))
    cleanup()
  })
})
