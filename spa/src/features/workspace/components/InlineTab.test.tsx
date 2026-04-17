import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { InlineTab } from './InlineTab'
import { useAgentStore } from '../../../stores/useAgentStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import type { Tab } from '../../../types/tab'

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
  useAgentStore.setState({
    statuses: {},
    unread: {},
    subagents: {},
    agentTypes: {},
    tabIndicatorStyle: 'badge',
    ccIconVariant: 'bot',
  })
  useLayoutStore.setState(useLayoutStore.getInitialState())
})

describe('InlineTab — indicator styles', () => {
  it("renders dot only when tabIndicatorStyle='dot'", () => {
    useAgentStore.setState({ tabIndicatorStyle: 'dot', statuses: { 'h1:S1': 'running' } })
    render(
      <InlineTab
        tab={baseTab}
        title="work"
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
        title="work"
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
        title="work"
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
        title="work"
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
        title="work"
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
        title="work"
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
        title="work"
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
        title="work"
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
        title="work"
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
        title="work"
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

describe('InlineTab — drag transform', () => {
  it('omits horizontal translate from style attribute (vertical-only drag)', () => {
    render(
      <InlineTab
        tab={baseTab}
        title="work"
        isActive={false}
        onSelect={() => {}}
        onClose={() => {}}
        onMiddleClick={() => {}}
        onContextMenu={() => {}}
      />,
    )
    const row = screen.getByTestId('inline-tab-row')
    const styleAttr = row.getAttribute('style') ?? ''
    // If a translate3d is present, its first argument must be 0 (vertical-only).
    // Match any translate3d(first-arg, ...) and assert first-arg is "0" when present.
    const match = styleAttr.match(/translate3d\(([^,]+),/)
    if (match) {
      expect(match[1].trim()).toBe('0')
    }
  })
})
