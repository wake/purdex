vi.mock('../lib/icon-path-cache', () => ({
  getIconPath: () => 'M0,0',
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { WorkspaceSettingsPage } from './WorkspaceSettingsPage'
import { useWorkspaceStore } from '../store'

describe('WorkspaceSettingsPage', () => {
  let wsId: string

  beforeEach(() => {
    cleanup()
    useWorkspaceStore.getState().reset()
    const ws = useWorkspaceStore.getState().addWorkspace('Test WS')
    wsId = ws.id
  })

  it('renders workspace name in editable input', () => {
    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    const input = screen.getByDisplayValue('Test WS')
    expect(input).toBeInTheDocument()
  })

  it('updates workspace name only when Save is clicked', () => {
    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    const input = screen.getByDisplayValue('Test WS')
    fireEvent.change(input, { target: { value: 'Renamed' } })
    fireEvent.blur(input)
    expect(useWorkspaceStore.getState().workspaces[0].name).toBe('Test WS')
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    expect(useWorkspaceStore.getState().workspaces[0].name).toBe('Renamed')
  })

  it('cancels a pending workspace name edit', () => {
    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    const input = screen.getByDisplayValue('Test WS') as HTMLInputElement
    fireEvent.change(input, { target: { value: 'Draft' } })
    fireEvent.click(screen.getByRole('button', { name: /cancel/i }))
    expect(input.value).toBe('Test WS')
    expect(useWorkspaceStore.getState().workspaces[0].name).toBe('Test WS')
  })

  it('has maxLength on name input to prevent excessively long names', () => {
    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    const input = screen.getByDisplayValue('Test WS') as HTMLInputElement
    expect(input.maxLength).toBe(64)
  })

  it('renders delete button and shows confirm dialog', () => {
    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    fireEvent.click(screen.getByTestId('delete-workspace-btn'))
    // WorkspaceDeleteDialog renders a delete confirm dialog with the workspace name
    expect(screen.getByText(/Delete Test WS/i)).toBeInTheDocument()
  })

  it('shows "not found" when workspace does not exist', () => {
    render(<WorkspaceSettingsPage workspaceId="nonexistent" />)
    expect(screen.getByText(/not found/i)).toBeInTheDocument()
  })
})
