import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react'
import { HostColorField } from './HostColorField'
import { useHostStore } from '../../stores/useHostStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import type { Tab } from '../../types/tab'

// The host badge renders a WorkspaceIcon, which only emits an <svg> once the
// Phosphor weight JSON is cached (a fetch that never resolves in jsdom) — same
// mock as InlineTab.test.tsx.
vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: (name: string, weight: string) => `M ${name} ${weight}`,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

const { InlineTab } = await import('../../features/workspace/components/InlineTab')

const HOST_ID = 'h1'
const badge = () => screen.getByTestId('host-badge')

const baseTab: Tab = {
  id: 't1',
  kind: 'tmux-session',
  locked: false,
  layout: {
    type: 'leaf',
    pane: {
      id: 't1-pane',
      content: { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'S1', terminated: false },
    },
  },
} as never

function InlineTabForH1({ isActive = false }: { isActive?: boolean }) {
  return (
    <InlineTab
      tab={baseTab}
      isActive={isActive}
      onSelect={() => {}}
      onClose={() => {}}
      onMiddleClick={() => {}}
      onContextMenu={() => {}}
    />
  )
}

describe('HostColorField → tab badge (spec §8.2 / §8.3)', () => {
  beforeEach(() => {
    cleanup()
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'H', ip: '1.2.3.4', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      runtime: {},
    })
    useUISettingsStore.setState({
      dynamicTabName: false,
      tabNameTooltipMode: 'both',
      tabIndicatorStyle: 'badge',
      ccIconVariant: 'bot',
      codexIconVariant: 'openai',
      hostBadgeSidebarEnabled: true,
      hostBadgeSidebarLineColor: 'host',
      hostBadgeSidebarBox: 16,
      hostBadgeSidebarInset: 2,
      hostBadgeSidebarRadius: 4,
    })
    useSessionStore.setState({
      sessions: { [HOST_ID]: [{ code: 'S1', name: 'work' }] as never },
      activeHostId: null,
      activeCode: null,
    })
    useAgentStore.setState({
      statuses: {},
      unread: {},
      subagents: {},
      agentTypes: {},
      oscTitles: {},
    })
    useLayoutStore.setState(useLayoutStore.getInitialState())
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', { color: '#3b82f6', alpha: 100 })
  })

  it('dragging Middle alpha updates the inactive row badge immediately', () => {
    render(
      <>
        <HostColorField hostId={HOST_ID} />
        <InlineTabForH1 isActive={false} />
      </>,
    )
    fireEvent.click(screen.getByTestId('host-color-layer-middle'))
    fireEvent.input(screen.getByTestId('host-color-range-a'), { target: { value: '45' } })
    expect(badge().style.getPropertyValue('--hb-middle')).toBe('rgba(59, 130, 246, 0.45)')
  })

  it('a Terminal main color applies to the agent tab and drops back to Console when the agentType clears', () => {
    render(
      <>
        <HostColorField hostId={HOST_ID} />
        <InlineTabForH1 isActive />
      </>,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Terminal' }))
    fireEvent.click(screen.getByTestId('host-color-layer-main'))
    fireEvent.click(screen.getByRole('button', { name: '#ef4444' }))
    act(() => useAgentStore.setState({ agentTypes: { 'h1:S1': 'cc' } }))
    expect(badge().style.getPropertyValue('--hb-main')).toBe('rgba(239, 68, 68, 1)')
    act(() => useAgentStore.setState({ agentTypes: {} }))
    expect(badge().style.getPropertyValue('--hb-main')).toBe('rgba(59, 130, 246, 1)')
  })
})
