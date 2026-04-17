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

describe('InlineTab — drag-safe pointerdown + isPinned data', () => {
  it('click on row still fires onSelect after pointerdown', () => {
    const onSelect = vi.fn()
    render(
      <DndContext>
        <SortableContext items={['t1']}>
          <InlineTab
            tab={mkTab({ pinned: false })}
            title="T1"
            isActive={false}
            sourceWsId={null}
            onSelect={onSelect}
            onClose={vi.fn()}
            onMiddleClick={vi.fn()}
            onContextMenu={vi.fn()}
          />
        </SortableContext>
      </DndContext>,
    )
    const row = screen.getByTestId('inline-tab-row')
    fireEvent.pointerDown(row, { button: 0, clientX: 10, clientY: 10 })
    fireEvent.click(row)
    expect(onSelect).toHaveBeenCalledWith('t1')
  })

  it('pointerdown on active tab prevents default to stop focus theft', () => {
    render(
      <DndContext>
        <SortableContext items={['t1']}>
          <InlineTab
            tab={mkTab({ pinned: false })}
            title="T1"
            isActive={true}
            sourceWsId={null}
            onSelect={vi.fn()}
            onClose={vi.fn()}
            onMiddleClick={vi.fn()}
            onContextMenu={vi.fn()}
          />
        </SortableContext>
      </DndContext>,
    )
    const row = screen.getByTestId('inline-tab-row')
    const evt = new Event('pointerdown', { bubbles: true, cancelable: true })
    row.dispatchEvent(evt)
    expect(evt.defaultPrevented).toBe(true)
  })

  it('pointerdown on inactive tab does NOT preventDefault', () => {
    render(
      <DndContext>
        <SortableContext items={['t1']}>
          <InlineTab
            tab={mkTab({ pinned: false })}
            title="T1"
            isActive={false}
            sourceWsId={null}
            onSelect={vi.fn()}
            onClose={vi.fn()}
            onMiddleClick={vi.fn()}
            onContextMenu={vi.fn()}
          />
        </SortableContext>
      </DndContext>,
    )
    const row = screen.getByTestId('inline-tab-row')
    const evt = new Event('pointerdown', { bubbles: true, cancelable: true })
    row.dispatchEvent(evt)
    expect(evt.defaultPrevented).toBe(false)
  })
})
