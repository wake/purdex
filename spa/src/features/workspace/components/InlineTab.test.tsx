import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { useAgentStore } from '../../../stores/useAgentStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { useSessionStore } from '../../../stores/useSessionStore'
import type { Tab } from '../../../types/tab'

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

const { InlineTab } = await import('./InlineTab')

const baseTab: Tab = {
  id: 't1',
  kind: 'tmux-session',
  locked: false,
  layout: {
    type: 'leaf',
    pane: {
      id: 't1-pane',
      content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'S1', terminated: false },
    },
  },
} as never

beforeEach(() => {
  cleanup()
  mockOnPointerDown.mockClear()
  useAgentStore.setState({
    statuses: {},
    unread: {},
    subagents: {},
    agentTypes: {},
    tabIndicatorStyle: 'badge',
    ccIconVariant: 'bot',
  })
  useSessionStore.setState({
    sessions: { h1: [{ code: 'S1', name: 'work' }] as never },
    activeHostId: null,
    activeCode: null,
  })
  useHostStore.setState({ runtime: {} })
  useLayoutStore.setState(useLayoutStore.getInitialState())
})

describe('InlineTab — indicator styles', () => {
  it("renders dot only when tabIndicatorStyle='dot'", () => {
    useAgentStore.setState({ tabIndicatorStyle: 'dot', statuses: { 'h1:S1': 'running' } })
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.getByTestId('inline-tab-dot')).toBeInTheDocument()
  })

  it("renders icon + overlay dot when tabIndicatorStyle='badge'", () => {
    useAgentStore.setState({ tabIndicatorStyle: 'badge', statuses: { 'h1:S1': 'running' } })
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.getByTestId('inline-tab-dot-overlay')).toBeInTheDocument()
  })

  it('renders close button visible (DOM present, opacity controlled by hover class)', () => {
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.getByLabelText(/close work/i)).toBeInTheDocument()
  })

  it('calls onClose when close button clicked', () => {
    const onClose = vi.fn()
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={onClose}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    fireEvent.click(screen.getByLabelText(/close work/i))
    expect(onClose).toHaveBeenCalledWith('t1')
  })
})

describe('InlineTab — close button visibility', () => {
  it('does NOT render close button when tab.locked is true', () => {
    const locked: Tab = { ...baseTab, locked: true } as never
    render(
      <InlineTab
        tab={locked}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.queryByLabelText(/close work/i)).not.toBeInTheDocument()
  })

  it('renders lock icon when tab.locked', () => {
    const locked: Tab = { ...baseTab, locked: true } as never
    render(
      <InlineTab
        tab={locked}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.getByTestId('inline-tab-lock')).toBeInTheDocument()
  })
})

describe('InlineTab — unread dot', () => {
  it('shows unread dot when unread and not active', () => {
    useAgentStore.setState({ unread: { 'h1:S1': true } })
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.getByTestId('inline-tab-unread')).toBeInTheDocument()
  })

  it('hides unread dot when active', () => {
    useAgentStore.setState({ unread: { 'h1:S1': true } })
    render(
      <InlineTab
        tab={baseTab}
        isActive={true}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.queryByTestId('inline-tab-unread')).not.toBeInTheDocument()
  })
})

describe('InlineTab — host offline', () => {
  it('renders WifiSlash when host is disconnected and tab is not terminated', () => {
    useHostStore.setState({
      runtime: { h1: { status: 'disconnected' } },
    } as never)
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.getByTestId('inline-tab-host-offline')).toBeInTheDocument()
  })

  it('does NOT render WifiSlash when tab is terminated (session tombstoned)', () => {
    useHostStore.setState({
      runtime: { h1: { status: 'disconnected' } },
    } as never)
    const terminated: Tab = {
      ...baseTab,
      layout: {
        type: 'leaf',
        pane: {
          id: 't1-pane',
          content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'S1', terminated: true },
        },
      },
    } as never
    render(
      <InlineTab
        tab={terminated}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    expect(screen.queryByTestId('inline-tab-host-offline')).not.toBeInTheDocument()
  })
})

describe('InlineTab — pointer down (dnd-kit integration)', () => {
  it('forwards pointerdown to dnd-kit handler', () => {
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    fireEvent.pointerDown(screen.getByTestId('inline-tab-row'))
    expect(mockOnPointerDown).toHaveBeenCalled()
  })

  it('calls preventDefault on active tab pointerdown', () => {
    render(
      <InlineTab
        tab={baseTab}
        isActive
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    const el = screen.getByTestId('inline-tab-row')
    const event = new PointerEvent('pointerdown', { bubbles: true, cancelable: true })
    el.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(true)
  })

  it('does not call preventDefault on inactive tab pointerdown', () => {
    render(
      <InlineTab
        tab={baseTab}
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    const el = screen.getByTestId('inline-tab-row')
    const event = new PointerEvent('pointerdown', { bubbles: true, cancelable: true })
    el.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(false)
  })
})
