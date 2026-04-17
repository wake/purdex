import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { ActivityBarWide } from './ActivityBarWide'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import type { Workspace } from '../../../types/tab'

const ws = (id: string, name: string): Workspace => ({
  id, name, tabs: [], activeTabId: null,
})

describe('ActivityBarWide', () => {
  beforeEach(() => {
    cleanup()
    useLayoutStore.setState(useLayoutStore.getInitialState())
  })

  it('renders Home label + workspace names', () => {
    render(
      <ActivityBarWide
        workspaces={[ws('w1', 'Purdex'), ws('w2', 'Client A')]}
        activeWorkspaceId="w1"
        activeStandaloneTabId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        standaloneTabIds={[]}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    expect(screen.getByText('Purdex')).toBeInTheDocument()
    expect(screen.getByText('Client A')).toBeInTheDocument()
  })

  it('clicking a workspace row calls onSelectWorkspace', () => {
    const onSelect = vi.fn()
    render(
      <ActivityBarWide
        workspaces={[ws('w1', 'Purdex')]}
        activeWorkspaceId={null}
        activeStandaloneTabId={null}
        onSelectWorkspace={onSelect}
        onSelectHome={() => {}}
        standaloneTabIds={[]}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    fireEvent.click(screen.getByText('Purdex'))
    expect(onSelect).toHaveBeenCalledWith('w1')
  })

  it('renders a resize handle', () => {
    render(
      <ActivityBarWide
        workspaces={[]}
        activeWorkspaceId={null}
        activeStandaloneTabId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        standaloneTabIds={[]}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    const handle = document.querySelector('[data-testid="activity-bar-resize"]')
    expect(handle).toBeInTheDocument()
  })
})

describe('ActivityBarWide Phase 2 — inline tabs', () => {
  beforeEach(() => {
    cleanup()
    useLayoutStore.setState(useLayoutStore.getInitialState())
  })

  it('renders WorkspaceRow per workspace', () => {
    render(
      <ActivityBarWide
        workspaces={[
          { id: 'w1', name: 'Alpha', tabs: [], activeTabId: null },
          { id: 'w2', name: 'Beta', tabs: [], activeTabId: null },
        ]}
        activeWorkspaceId="w1"
        activeStandaloneTabId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        standaloneTabIds={[]}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
        tabsById={{}}
        activeTabId={null}
        onSelectTab={() => {}}
        onCloseTab={() => {}}
        onMiddleClickTab={() => {}}
        onContextMenuTab={() => {}}
        onReorderWorkspaceTabs={() => {}}
        onReorderStandaloneTabs={() => {}}
        onAddTabToWorkspace={() => {}}
      />,
    )
    expect(screen.getByText('Alpha')).toBeInTheDocument()
    expect(screen.getByText('Beta')).toBeInTheDocument()
  })

  it('shows expanded inline tabs when workspaceExpanded set', () => {
    useLayoutStore.setState({ workspaceExpanded: { w1: true } })
    render(
      <ActivityBarWide
        workspaces={[{ id: 'w1', name: 'Alpha', tabs: ['t1'], activeTabId: 't1' }]}
        activeWorkspaceId="w1"
        activeStandaloneTabId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        standaloneTabIds={[]}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
        tabsById={{
          t1: {
            id: 't1',
            kind: 'new-tab',
            locked: false,
            layout: {
              type: 'leaf',
              pane: {
                id: 't1-pane',
                content: { kind: 'browser', url: 'https://example.test/' },
              },
            },
          } as never,
        }}
        activeTabId="t1"
        onSelectTab={() => {}}
        onCloseTab={() => {}}
        onMiddleClickTab={() => {}}
        onContextMenuTab={() => {}}
        onReorderWorkspaceTabs={() => {}}
        onReorderStandaloneTabs={() => {}}
        onAddTabToWorkspace={() => {}}
      />,
    )
    // getPaneLabel for browser returns hostname
    expect(screen.getByText('example.test')).toBeInTheDocument()
  })

  it('registers home-header and ws-header-<id> as droppable testids', () => {
    render(
      <ActivityBarWide
        workspaces={[ws('w1', 'Alpha'), ws('w2', 'Beta')]}
        activeWorkspaceId={null}
        activeStandaloneTabId={null}
        onSelectWorkspace={() => {}}
        onSelectHome={() => {}}
        standaloneTabIds={[]}
        onAddWorkspace={() => {}}
        onOpenHosts={() => {}}
        onOpenSettings={() => {}}
      />,
    )
    expect(screen.getByTestId('home-header')).toBeInTheDocument()
    expect(screen.getByTestId('ws-header-w1')).toBeInTheDocument()
    expect(screen.getByTestId('ws-header-w2')).toBeInTheDocument()
  })
})
