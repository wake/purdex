import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ActivityBarWide } from './ActivityBarWide'
import type { Workspace } from '../../../types/tab'

const ws = (id: string, name: string): Workspace => ({
  id, name, tabs: [], activeTabId: null,
})

describe('ActivityBarWide', () => {
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
