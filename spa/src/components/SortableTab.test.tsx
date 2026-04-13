import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, fireEvent, cleanup } from '@testing-library/react'
import type { Tab } from '../types/tab'
import { clearModuleRegistry, registerModule } from '../lib/module-registry'
import { useSessionStore } from '../stores/useSessionStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'

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
  return {
    id,
    pinned: opts?.pinned ?? false,
    locked: false,
    createdAt: 0,
    layout: {
      type: 'leaf',
      pane: {
        id: `pane-${id}`,
        content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'sc1', mode: 'terminal' as const, cachedName: '', tmuxInstance: '' },
      },
    },
  }
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
})
