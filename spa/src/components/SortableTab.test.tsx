import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import type { Tab } from '../types/tab'
import { createTab } from '../types/tab'
import { clearModuleRegistry, registerModule } from '../lib/module-registry'
import { useSessionStore } from '../stores/useSessionStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useI18nStore } from '../stores/useI18nStore'

const mockOnPointerDown = vi.fn()

vi.mock('@dnd-kit/sortable', () => ({
  useSortable: () => ({
    attributes: {},
    listeners: { onPointerDown: mockOnPointerDown },
    setNodeRef: vi.fn(),
    transform: null,
    transition: null,
    isDragging: false,
  }),
}))

vi.mock('../features/workspace/generated/icon-loader', () => ({
  iconLoaders: {},
}))

// Lazy import so mocks are applied before module resolution
const { SortableTab } = await import('./SortableTab')

function makeTestTab(id: string, opts?: { pinned?: boolean }): Tab {
  const tab = createTab(
    { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sc1', mode: 'terminal' as const, cachedName: '', tmuxInstance: '' },
    { pinned: opts?.pinned },
  )
  return { ...tab, id }
}

const defaultProps = {
  tab: makeTestTab('t1'),
  isActive: false,
  onSelect: vi.fn(),
  onClose: vi.fn(),
  onMiddleClick: vi.fn(),
  onContextMenu: vi.fn(),
  iconMap: {} as Record<string, React.ComponentType<{ size: number; className?: string }>>,
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  clearModuleRegistry()
  registerModule({ id: 'session', name: 'Session', pane: { kind: 'tmux-session', component: () => null } })
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  useHostStore.setState({ runtime: {} })
  useAgentStore.setState({ unread: {}, statuses: {}, subagents: {}, tabIndicatorStyle: 'overlay' })
  useI18nStore.setState({ t: (k: string) => k })
})

describe('SortableTab', () => {
  it('renders data-tab-id on normal tab', () => {
    const { container } = render(<SortableTab {...defaultProps} />)
    const el = container.querySelector('[data-tab-id="t1"]')
    expect(el).toBeTruthy()
  })

  it('renders data-tab-id on pinned tab', () => {
    const pinnedTab = makeTestTab('t1', { pinned: true })
    const { container } = render(<SortableTab {...defaultProps} tab={pinnedTab} pinned />)
    const el = container.querySelector('[data-tab-id="t1"]')
    expect(el).toBeTruthy()
  })

  it('calls dnd-kit onPointerDown handler', () => {
    const { container } = render(<SortableTab {...defaultProps} />)
    const el = container.querySelector('[data-tab-id="t1"]')!
    fireEvent.pointerDown(el)
    expect(mockOnPointerDown).toHaveBeenCalled()
  })

  it('calls preventDefault on active tab pointerDown', () => {
    const { container } = render(<SortableTab {...defaultProps} isActive />)
    const el = container.querySelector('[data-tab-id="t1"]')!
    const event = new PointerEvent('pointerdown', { bubbles: true, cancelable: true })
    el.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(true)
  })

  it('does not call preventDefault on inactive tab pointerDown', () => {
    const { container } = render(<SortableTab {...defaultProps} isActive={false} />)
    const el = container.querySelector('[data-tab-id="t1"]')!
    const event = new PointerEvent('pointerdown', { bubbles: true, cancelable: true })
    el.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(false)
  })

  it('calls onSelect when tab is clicked', () => {
    const onSelect = vi.fn()
    const { container } = render(<SortableTab {...defaultProps} onSelect={onSelect} />)
    const el = container.querySelector('[data-tab-id="t1"]')!
    fireEvent.click(el)
    expect(onSelect).toHaveBeenCalledWith('t1')
  })

  it('calls onClose without triggering onSelect when close button is clicked', () => {
    const onSelect = vi.fn()
    const onClose = vi.fn()
    render(<SortableTab {...defaultProps} onSelect={onSelect} onClose={onClose} />)
    const closeBtn = screen.getByTitle('tab.close')
    fireEvent.click(closeBtn)
    expect(onClose).toHaveBeenCalledWith('t1')
    expect(onSelect).not.toHaveBeenCalled()
  })
})
