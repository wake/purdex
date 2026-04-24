import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ActivityBarNarrow } from './ActivityBarNarrow'

describe('ActivityBarNarrow', () => {
  it('renders Home button', () => {
    render(
      <ActivityBarNarrow
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
    expect(screen.getByTitle(/home/i)).toBeInTheDocument()
  })

  it('keeps Home outside the workspace scroll region', () => {
    const { container } = render(
      <ActivityBarNarrow
        workspaces={[
          { id: 'w1', name: 'Purdex', tabs: [], activeTabId: null },
          { id: 'w2', name: 'Client A', tabs: [], activeTabId: null },
        ]}
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

    const workspaceScroll = screen.getByTestId('activity-bar-workspace-scroll')
    expect(workspaceScroll).toHaveClass('overflow-y-auto')
    expect(screen.getByTitle(/home/i).closest('[data-testid="activity-bar-workspace-scroll"]')).toBeNull()
    expect(container.querySelector('[data-testid="activity-bar-workspace-separator"]')).not.toBeNull()
    expect(container.querySelector('[data-testid="activity-bar-workspace-separator"]')?.closest('[data-testid="activity-bar-workspace-scroll"]')).toBeNull()
  })
})
