import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { AgentsSection } from './AgentsSection'
import { hostFetch } from '../../lib/host-api'
import { useHostStore } from '../../stores/useHostStore'

vi.mock('../../lib/host-api', () => ({ hostFetch: vi.fn() }))

const mockFetch = hostFetch as unknown as ReturnType<typeof vi.fn>

beforeEach(() => {
  mockFetch.mockReset()
  // Set runtime as connected so the component isn't offline
  useHostStore.setState({
    runtime: { h1: { status: 'connected' } },
  })
})

function mockDetect(
  payload: Record<string, { installed: boolean; version?: string; path?: string }>,
  titleStatus = {
    allow_set_title: true,
    installed: true,
    runtime_applied: true,
    managed_config_path: '/Users/test/.tmux.conf',
    error: '',
  },
) {
  mockFetch.mockImplementation((_hostId: string, path: string) => {
    if (path === '/api/agents/detect') {
      return Promise.resolve({ ok: true, json: () => Promise.resolve(payload) })
    }
    if (path === '/api/agent/title/status') {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve(titleStatus),
      })
    }
    if (path === '/api/agent/title/setup') {
      return Promise.resolve({
        ok: true,
        json: () => Promise.resolve({
          allow_set_title: true,
          installed: false,
          runtime_applied: true,
          managed_config_path: '/Users/test/.tmux.conf',
          error: '',
        }),
      })
    }
    if (path === '/api/agent/cc/statusline/status') {
      return Promise.resolve({ ok: true, json: () => Promise.resolve({ mode: 'none', installed: false, settingsPath: '/x' }) })
    }
    return Promise.resolve({ ok: false, status: 404 })
  })
}

describe('AgentsSection agent title integration', () => {
  it('renders the Agent title block before agent cards', async () => {
    mockDetect({ cc: { installed: true, version: '0.1.0' } })
    render(<AgentsSection hostId="h1" />)

    await waitFor(() => expect(screen.getByText('Agent title')).toBeInTheDocument())
    const titleBlock = screen.getByTestId('agent-title-block')
    const ccCard = screen.getByTestId('agent-card-cc')
    expect(titleBlock.compareDocumentPosition(ccCard) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(screen.getByText('allow-set-title')).toBeInTheDocument()
  })

  it('calls title setup endpoint when removing installed integration', async () => {
    mockDetect({ cc: { installed: true, version: '0.1.0' } })
    render(<AgentsSection hostId="h1" />)

    const removeButton = await screen.findByRole('button', { name: 'Remove' })
    fireEvent.click(removeButton)

    await waitFor(() => expect(mockFetch).toHaveBeenCalledWith('h1', '/api/agent/title/setup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action: 'remove' }),
    }))
  })

  it('calls title setup endpoint when installing missing integration', async () => {
    mockDetect({ cc: { installed: true, version: '0.1.0' } }, {
      allow_set_title: false,
      installed: false,
      runtime_applied: false,
      managed_config_path: '/Users/test/.tmux.conf',
      error: '',
    })
    render(<AgentsSection hostId="h1" />)

    const installButton = await screen.findByRole('button', { name: 'Install' })
    fireEvent.click(installButton)

    await waitFor(() => expect(mockFetch).toHaveBeenCalledWith('h1', '/api/agent/title/setup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action: 'install' }),
    }))
  })

  it('hides the statusline extension UI for installed Claude Code', async () => {
    mockDetect({ cc: { installed: true, version: '0.1.0' } })
    render(<AgentsSection hostId="h1" />)

    await waitFor(() =>
      expect(screen.getByText(/claude code/i)).toBeInTheDocument()
    )
    expect(screen.queryByText('Extensions')).not.toBeInTheDocument()
    expect(screen.queryByText('Pipeline test')).not.toBeInTheDocument()
    expect(mockFetch).not.toHaveBeenCalledWith('h1', '/api/agent/cc/statusline/status')
  })

  it('shows dynamic title capability rows for installed Claude, Codex, and OpenCode cards', async () => {
    mockDetect({
      cc: { installed: true, version: '0.1.0' },
      codex: { installed: true, version: '1.0.0' },
      opencode: { installed: true, version: '0.9.0' },
    })
    render(<AgentsSection hostId="h1" />)

    await waitFor(() => expect(screen.getAllByText('Dynamic title')).toHaveLength(3))
    expect(screen.getByText(/Claude terminal titles are likely enabled/i)).toBeInTheDocument()
    expect(screen.getByText(/Codex terminal title uses its default behavior/i)).toBeInTheDocument()
    expect(screen.getByText(/OpenCode has no documented persistent title toggle/i)).toBeInTheDocument()
  })

  it('does not show dynamic title rows for uninstalled cards', async () => {
    mockDetect({
      cc: { installed: false },
      codex: { installed: false },
      opencode: { installed: false },
    })
    render(<AgentsSection hostId="h1" />)

    await waitFor(() => expect(screen.getAllByText(/not found/i)).toHaveLength(3))
    expect(screen.queryByText('Dynamic title')).not.toBeInTheDocument()
  })
})
