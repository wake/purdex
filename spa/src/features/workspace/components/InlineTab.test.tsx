import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { SortableContext } from '@dnd-kit/sortable'
import { InlineTab } from './InlineTab'
import type { Tab } from '../../../types/tab'

function renderWith(
  tab: Tab,
  title: string,
  overrides: Partial<React.ComponentProps<typeof InlineTab>> = {},
) {
  return render(
    <DndContext>
      <SortableContext items={[tab.id]}>
        <InlineTab
          tab={tab}
          title={title}
          isActive={false}
          onSelect={() => {}}
          onClose={() => {}}
          onMiddleClick={() => {}}
          onContextMenu={() => {}}
          {...overrides}
        />
      </SortableContext>
    </DndContext>,
  )
}

const mkTab = (overrides: Partial<Tab> = {}): Tab =>
  ({
    id: 't1',
    kind: 'new-tab',
    locked: false,
    layout: { type: 'single' } as Tab['layout'],
    ...overrides,
  }) as Tab

describe('InlineTab', () => {
  it('renders given title', () => {
    renderWith(mkTab(), 'My Tab')
    expect(screen.getByText('My Tab')).toBeInTheDocument()
  })

  it('click triggers onSelect', () => {
    const onSelect = vi.fn()
    renderWith(mkTab(), 'Untitled', { onSelect })
    fireEvent.click(screen.getByText('Untitled'))
    expect(onSelect).toHaveBeenCalledWith('t1')
  })

  it('close button triggers onClose and stops propagation', () => {
    const onSelect = vi.fn()
    const onClose = vi.fn()
    renderWith(mkTab(), 'Untitled', { onSelect, onClose })
    fireEvent.click(screen.getByRole('button', { name: /close/i }))
    expect(onClose).toHaveBeenCalledWith('t1')
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('active state adds a purple ring class', () => {
    const { container } = renderWith(mkTab(), 'Untitled', { isActive: true })
    const row = container.querySelector('[data-testid="inline-tab-row"]')!
    expect(row.className).toMatch(/ring/)
  })

  it('middle click triggers onMiddleClick', () => {
    const onMiddleClick = vi.fn()
    renderWith(mkTab(), 'Untitled', { onMiddleClick })
    const row = screen.getByText('Untitled').closest('[data-testid="inline-tab-row"]')!
    fireEvent.mouseDown(row, { button: 1 })
    expect(onMiddleClick).toHaveBeenCalledWith('t1')
  })
})
