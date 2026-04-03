import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { TabBar } from './TabBar'
import { createTab } from '../types/tab'
import type { Tab } from '../types/tab'
import { registerPaneRenderer, clearPaneRegistry } from '../lib/pane-registry'
import { useSessionStore } from '../stores/useSessionStore'

beforeEach(() => {
  cleanup()
  clearPaneRegistry()
  registerPaneRenderer('tmux-session', { component: () => null })
  registerPaneRenderer('dashboard', { component: () => null })
  // Provide sessions keyed by hostId for SortableTab's label lookups
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
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
    // Session tabs show sessionCode as fallback label (no session store data)
    expect(screen.getByText('dev001')).toBeTruthy()
    expect(screen.getByText('cld001')).toBeTruthy()
    expect(screen.getByText('Dashboard')).toBeTruthy()
  })

  it('highlights active tab', () => {
    render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} />)
    const activeTab = screen.getByText('dev001').closest('[role="tab"]')!
    expect(activeTab.className).toContain('text-white')
  })

  it('calls onSelectTab on click', () => {
    const onSelect = vi.fn()
    render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} onSelectTab={onSelect} />)
    fireEvent.click(screen.getByText('cld001'))
    expect(onSelect).toHaveBeenCalledWith('t2')
  })

  it('calls onCloseTab on close button click', () => {
    const onClose = vi.fn()
    render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} onCloseTab={onClose} />)
    const closeButtons = screen.getAllByTitle('Close tab')
    fireEvent.click(closeButtons[0])
    expect(onClose).toHaveBeenCalledWith('t1')
  })

  it('renders pinned tabs as icon-only with title', () => {
    render(<TabBar tabs={pinnedTabs} activeTabId="t1" {...defaultHandlers} />)
    // Pinned tab shows label as title attribute (sessionCode fallback)
    const pinnedBtn = screen.getByTitle('aaa001')
    expect(pinnedBtn).toBeInTheDocument()
    // Pinned tab should not render label text in the button content
    expect(pinnedBtn.textContent).not.toContain('aaa001')
  })

  it('renders normal tabs with label', () => {
    render(<TabBar tabs={pinnedTabs} activeTabId="t1" {...defaultHandlers} />)
    expect(screen.getByText('bbb001')).toBeInTheDocument()
    expect(screen.getByText('ccc001')).toBeInTheDocument()
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
    expect(screen.getByText('xxx001')).toBeInTheDocument()
    // Lock icon rendered — verify SVG with Lock's presence
    const tabBtn = screen.getByText('xxx001').closest('[role="tab"]')!
    const svgs = tabBtn.querySelectorAll('svg')
    // Should have at least 2 SVGs: tab icon + lock icon
    expect(svgs.length).toBeGreaterThanOrEqual(2)
  })

  it('activates tab on Enter key', () => {
    const onSelect = vi.fn()
    render(<TabBar tabs={mockTabs} activeTabId="t1" {...defaultHandlers} onSelectTab={onSelect} />)
    const tab = screen.getByText('cld001').closest('[role="tab"]')!
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
})
