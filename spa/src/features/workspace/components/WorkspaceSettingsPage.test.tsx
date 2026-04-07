vi.mock('../generated/icon-loader', () => ({
  ALL_ICON_NAMES: ['House', 'Star'],
  iconLoaders: {
    House: () => new Promise(() => {}),
    Star: () => new Promise(() => {}),
  },
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

  it('updates workspace name on input change + blur', () => {
    render(<WorkspaceSettingsPage workspaceId={wsId} />)
    const input = screen.getByDisplayValue('Test WS')
    fireEvent.change(input, { target: { value: 'Renamed' } })
    fireEvent.blur(input)
    expect(useWorkspaceStore.getState().workspaces[0].name).toBe('Renamed')
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
