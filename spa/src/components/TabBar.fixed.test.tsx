import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import type { DragEndEvent } from '@dnd-kit/core'
import { TabBar } from './TabBar'
import { createTab, type PaneContent, type Tab } from '../types/tab'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { useI18nStore } from '../stores/useI18nStore'

// The fixed row is the watcher area's tab row (spec §3): nothing is added, locked, closed or menu'd, but the order can be dragged.
const dnd = vi.hoisted(() => ({ onDragEnd: null as null | ((e: DragEndEvent) => void) }))
vi.mock('@dnd-kit/core', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@dnd-kit/core')>()
  return { ...actual, DndContext: (props: { children: React.ReactNode; onDragEnd: (e: DragEndEvent) => void }) => { dnd.onDragEnd = props.onDragEnd; return <>{props.children}</> } }
})
vi.mock('../hooks/useScrollOverflow', () => ({
  useScrollOverflow: () => ({ containerRef: { current: null }, canScrollLeft: false, canScrollRight: false, scrollLeft: vi.fn(), scrollRight: vi.fn() }),
}))

function watchTab(id: string, content: PaneContent): Tab {
  return { ...createTab(content), id, locked: true }
}
const tabs: Tab[] = [
  watchTab('watch-host-a', { kind: 'watch-host', fleetHostId: 'a' }),
  watchTab('watch-host-b', { kind: 'watch-host', fleetHostId: 'b' }),
]
const handlers = () => ({
  onSelectTab: vi.fn(), onCloseTab: vi.fn(), onAddTab: vi.fn(), onReorderTabs: vi.fn(), onMiddleClick: vi.fn(), onContextMenu: vi.fn(),
})

beforeEach(() => {
  cleanup()
  clearModuleRegistry()
  registerModule({ id: 'watch-area', name: 'W', panes: [{ kind: 'watch-host', component: () => null }] })
})

describe('TabBar fixed (the watcher area row)', () => {
  it('has no "+" button; an ordinary bar does', () => {
    const h = handlers()
    const { rerender } = render(<TabBar tabs={tabs} activeTabId="watch-host-a" fixed {...h} />)
    expect(screen.queryByTitle(useI18nStore.getState().t('nav.new_tab'))).toBeNull()
    rerender(<TabBar tabs={tabs} activeTabId="watch-host-a" {...h} />)
    expect(screen.getByTitle(useI18nStore.getState().t('nav.new_tab'))).toBeInTheDocument()
  })

  it('shows no lock icon on a locked tab, in the normal and the pinned branch', () => {
    const pinned = { ...watchTab('watch-pinned', { kind: 'watch-host', fleetHostId: 'p' }), pinned: true }
    const h = handlers()
    const { container, rerender } = render(<TabBar tabs={[pinned, ...tabs]} activeTabId="watch-host-a" fixed {...h} />)
    expect(container.querySelector('svg[class*="ml-0.5"]')).toBeNull()
    expect(container.querySelectorAll('[data-tab-id] svg.absolute').length).toBe(0)
    // the control: without `fixed` the lock is drawn (normal branch and pinned branch)
    rerender(<TabBar tabs={[pinned, ...tabs]} activeTabId="watch-host-a" {...h} />)
    expect(container.querySelector('[data-tab-id="watch-host-a"] svg.ml-0\\.5')).not.toBeNull()
    expect(container.querySelector('[data-tab-id="watch-pinned"] svg.absolute')).not.toBeNull()
  })

  it('a middle click, a context menu and a double click call nothing', () => {
    const h = { ...handlers(), onRenameTab: vi.fn() }
    render(<TabBar tabs={tabs} activeTabId="watch-host-a" fixed {...h} />)
    const el = document.querySelector('[data-tab-id="watch-host-b"]')!
    fireEvent.mouseUp(el, { button: 1 })
    fireEvent.contextMenu(el)
    fireEvent.doubleClick(el)
    expect(h.onMiddleClick).not.toHaveBeenCalled()
    expect(h.onContextMenu).not.toHaveBeenCalled()
    expect(h.onRenameTab).not.toHaveBeenCalled()
    fireEvent.click(el)
    expect(h.onSelectTab).toHaveBeenCalledWith('watch-host-b')
  })

  it('dragging still reorders', () => {
    const h = handlers()
    render(<TabBar tabs={tabs} activeTabId="watch-host-a" fixed {...h} />)
    dnd.onDragEnd!({ active: { id: 'watch-host-b' }, over: { id: 'watch-host-a' } } as unknown as DragEndEvent)
    expect(h.onReorderTabs).toHaveBeenCalledWith(['watch-host-b', 'watch-host-a'])
  })
})

describe('TabBar — the watcher area back button (journal v5 §3.7, U11)', () => {
  afterEach(() => useI18nStore.getState().setLocale('en'))

  it('is the first item of the row, ahead of the pinned and the normal tabs; a click calls onLeave and selects no tab', () => {
    useI18nStore.getState().setLocale('zh-TW')
    const pinned = { ...watchTab('watch-pinned', { kind: 'watch-host', fleetHostId: 'p' }), pinned: true }
    const h = handlers()
    const onLeave = vi.fn()
    const { container } = render(<TabBar tabs={[pinned, ...tabs]} activeTabId="watch-host-a" fixed onLeave={onLeave} {...h} />)
    const back = screen.getByRole('button', { name: '離開守門員專區' })
    expect(back).toHaveAttribute('title', '離開守門員專區')
    expect(back.querySelector('svg')).not.toBeNull()
    expect(container.firstElementChild!.firstElementChild).toBe(back)
    for (const el of container.querySelectorAll('[data-tab-id]')) {
      expect(back.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    }
    fireEvent.click(back)
    expect(onLeave).toHaveBeenCalledTimes(1)
    expect(h.onSelectTab).not.toHaveBeenCalled()
  })

  it('looks like the row\'s other controls, in theme tokens only', () => {
    const h = handlers()
    render(<TabBar tabs={tabs} activeTabId="watch-host-a" fixed onLeave={vi.fn()} {...h} />)
    const back = screen.getByTestId('watch-area-back')
    // the size and the resting colour of the "+" button; its hover in a token that has a light and a dark value
    for (const cls of ['w-7', 'h-7', 'rounded-md', 'text-text-secondary', 'hover:text-text-primary', 'hover:bg-surface-hover']) {
      expect(back.className.split(' ')).toContain(cls)
    }
    expect(back.className).not.toMatch(/#[0-9a-f]{3,8}\b|white|black|\[/i)
  })

  it('no back button without onLeave', () => {
    const h = handlers()
    render(<TabBar tabs={tabs} activeTabId="watch-host-a" fixed {...h} />)
    expect(screen.queryByTestId('watch-area-back')).toBeNull()
  })
})
