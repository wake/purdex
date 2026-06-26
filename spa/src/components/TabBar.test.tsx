import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { TabBar } from './TabBar'
import { createTab } from '../types/tab'
import type { Tab } from '../types/tab'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { useSessionStore } from '../stores/useSessionStore'

const scrollOverflowState = vi.hoisted(() => ({
  canScrollLeft: false,
  canScrollRight: false,
  scrollLeft: vi.fn(),
  scrollRight: vi.fn(),
}))

vi.mock('../hooks/useScrollOverflow', () => ({
  useScrollOverflow: () => ({
    containerRef: { current: null },
    canScrollLeft: scrollOverflowState.canScrollLeft,
    canScrollRight: scrollOverflowState.canScrollRight,
    scrollLeft: scrollOverflowState.scrollLeft,
    scrollRight: scrollOverflowState.scrollRight,
  }),
}))

beforeEach(() => {
  cleanup()
  clearModuleRegistry()
  registerModule({ id: 'session', name: 'Session', panes: [{ kind: 'tmux-session', component: () => null }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: () => null }] })
  // Provide sessions keyed by hostId for SortableTab's label lookups
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  scrollOverflowState.canScrollLeft = false
  scrollOverflowState.canScrollRight = false
  scrollOverflowState.scrollLeft.mockClear()
  scrollOverflowState.scrollRight.mockClear()
})

const defaultHandlers = {
  onSelectTab: vi.fn(),
  onCloseTab: vi.fn(),
  onAddTab: vi.fn(),
  onReorderTabs: vi.fn(),
  onMiddleClick: vi.fn(),
  onContextMenu: vi.fn(),
}

// Helper: create a Tab with a fixed id and specific content
function makeTab(id: string, content: import('../types/tab').PaneContent, opts?: { pinned?: boolean; locked?: boolean }): Tab {
  const tab = createTab(content, { pinned: opts?.pinned })
  return { ...tab, id, locked: opts?.locked ?? false }
}

const mockTabs: Tab[] = [
  makeTab('t1', { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' }),
  makeTab('t2', { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'cld001', mode: 'stream', cachedName: '', tmuxInstance: '' }),
  makeTab('t3', { kind: 'dashboard' }),
]

const pinnedTabs: Tab[] = [
  makeTab('p1', { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'aaa001', mode: 'terminal', cachedName: '', tmuxInstance: '' }, { pinned: true }),
  makeTab('t1', { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'bbb001', mode: 'terminal', cachedName: '', tmuxInstance: '' }),
  makeTab('t2', { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'ccc001', mode: 'terminal', cachedName: '', tmuxInstance: '' }),
]

describe('TabBar', () => {
  it('renders all tabs', () => {
    render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} />)
    // Session tabs show sessionCode as fallback label (no session store data).
    // Each label appears twice per tab: visible span + HoverTooltip sibling.
    expect(screen.getAllByText('dev001')).toHaveLength(2)
    expect(screen.getAllByText('cld001')).toHaveLength(2)
    expect(screen.getAllByText('Dashboard')).toHaveLength(2)
  })

  it('highlights active tab', () => {
    render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} />)
    // First match is the visible span; either is inside the same tab root.
    const activeTab = screen.getAllByText('dev001')[0].closest('[role="tab"]')!
    expect(activeTab.className).toContain('text-white')
  })

  it('calls onSelectTab on click', () => {
    const onSelect = vi.fn()
    render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} onSelectTab={onSelect} />)
    fireEvent.click(screen.getAllByText('cld001')[0])
    expect(onSelect).toHaveBeenCalledWith('t2')
  })

  it('calls onCloseTab on close button click', () => {
    const onClose = vi.fn()
    render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} onCloseTab={onClose} />)
    const closeButtons = screen.getAllByTitle('Close tab')
    fireEvent.click(closeButtons[0])
    expect(onClose).toHaveBeenCalledWith('t1')
  })

  it('renders pinned tabs as icon-only with HoverTooltip label', () => {
    const { container } = render(<TabBar tabs={pinnedTabs} activeTabId="t1" {...defaultHandlers} />)
    // Pinned tab no longer uses native title attr — label comes via HoverTooltip,
    // which portals its role="tooltip" element to document.body (NOT inside the tab
    // subtree). Scope by accessible name to the target pinned tab's tooltip.
    const tooltip = screen.getByRole('tooltip', { name: 'aaa001' })
    expect(tooltip).toBeInTheDocument()
    expect(tooltip.textContent).toBe('aaa001')
    // Pinned tab should not render the label as a visible (non-tooltip) text node
    // inside the button — only the icon + the (visually hidden until hover) tooltip.
    const pinnedRoot = container.querySelector('[data-tab-id="p1"]')!
    expect(pinnedRoot.querySelector('span.overflow-hidden')).toBeNull()
  })

  it('renders normal tabs with label', () => {
    render(<TabBar tabs={pinnedTabs} activeTabId="t1" {...defaultHandlers} />)
    // Each label appears twice per tab: visible span + HoverTooltip sibling.
    expect(screen.getAllByText('bbb001')).toHaveLength(2)
    expect(screen.getAllByText('ccc001')).toHaveLength(2)
  })

  it('locked tab hides close button', () => {
    const lockedTabs: Tab[] = [
      makeTab('t1', { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'xxx001', mode: 'terminal', cachedName: '', tmuxInstance: '' }, { locked: true }),
    ]
    render(<TabBar tabs={lockedTabs} activeTabId="t1" {...defaultHandlers} />)
    expect(screen.queryByTitle('Close tab')).not.toBeInTheDocument()
  })

  it('shows lock icon on locked non-pinned tab', () => {
    const lockedTabs: Tab[] = [
      makeTab('t1', { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'xxx001', mode: 'terminal', cachedName: '', tmuxInstance: '' }, { locked: true }),
    ]
    render(<TabBar tabs={lockedTabs} activeTabId="t1" {...defaultHandlers} />)
    // Label appears twice per tab: visible span + HoverTooltip sibling.
    expect(screen.getAllByText('xxx001')).toHaveLength(2)
    // Lock icon rendered — verify SVG with Lock's presence
    const tabBtn = screen.getAllByText('xxx001')[0].closest('[role="tab"]')!
    const svgs = tabBtn.querySelectorAll('svg')
    // Should have at least 2 SVGs: tab icon + lock icon
    expect(svgs.length).toBeGreaterThanOrEqual(2)
  })

  it('activates tab on Enter key', () => {
    const onSelect = vi.fn()
    render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} onSelectTab={onSelect} />)
    const tab = screen.getAllByText('cld001')[0].closest('[role="tab"]')!
    fireEvent.keyDown(tab, { key: 'Enter' })
    expect(onSelect).toHaveBeenCalledWith('t2')
  })

  it('close button is a real <button> element', () => {
    render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} />)
    const closeBtn = screen.getAllByTitle('Close tab')[0]
    expect(closeBtn.tagName).toBe('BUTTON')
  })

  it('calls onAddTab on + button', () => {
    const onAdd = vi.fn()
    render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} onAddTab={onAdd} />)
    fireEvent.click(screen.getByTitle('New tab'))
    expect(onAdd).toHaveBeenCalled()
  })

  it('shows separator between pinned and normal zones', () => {
    const { container } = render(<TabBar tabs={pinnedTabs} activeTabId="t1" {...defaultHandlers} />)
    const separator = container.querySelector('.bg-border-default')
    expect(separator).toBeInTheDocument()
  })

  it('no pinned-zone separator when no pinned tabs', () => {
    const { container } = render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} />)
    // No pinned/normal zone divider (h-4 height, distinct from tab separators which are h-3.5)
    const zoneDividers = container.querySelectorAll('.w-px.h-4.bg-border-default')
    expect(zoneDividers.length).toBe(0)
  })

  it('lets normal tabs shrink before the scroller overflows', () => {
    const { container } = render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} />)
    const scroller = container.querySelector('.overflow-x-auto.scrollbar-hide')!
    const strip = scroller.firstElementChild as HTMLElement

    expect(strip.className).toContain('flex-1')
    expect(strip.className).toContain('min-w-0')
    expect(strip.style.minWidth).toBe('')
    expect(strip.style.maxWidth).toBe('max-content')
  })

  it('renders right scroll control with a fade area and opaque button', () => {
    scrollOverflowState.canScrollRight = true

    const { container } = render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} />)
    const fade = container.querySelector('[data-testid="tab-scroll-right-fade"]')!
    const gradient = container.querySelector('[data-testid="tab-scroll-right-gradient"]')!
    const button = fade.querySelector('button')!

    expect(fade.className).toContain('w-16')
    expect(fade.className).toContain('pointer-events-none')
    expect(gradient.className).toContain('bg-gradient-to-r')
    expect(gradient.className).toContain('from-transparent')
    expect(gradient.className).toContain('to-surface-secondary')
    expect(button.className).toContain('bg-surface-secondary')
    expect(button.className).toContain('pointer-events-auto')
  })
})
