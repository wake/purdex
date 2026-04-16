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
})
