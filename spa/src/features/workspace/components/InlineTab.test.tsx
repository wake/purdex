import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { InlineTab } from './InlineTab'
import { useAgentStore } from '../../../stores/useAgentStore'
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
