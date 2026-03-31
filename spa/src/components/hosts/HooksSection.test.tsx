// spa/src/components/hosts/HooksSection.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import { HooksSection } from './HooksSection'
import { useHostStore } from '../../stores/useHostStore'
import { fetchHooksStatus, installHooks, removeHooks } from '../../lib/host-api'

const mockHooksStatus = {
  tmux_hooks: {
    'session-created': true,
    'session-closed': true,
    'session-renamed': true,
  },
  agent_hooks: false,
}

vi.mock('../../lib/host-api', () => ({
  fetchHooksStatus: vi.fn(() =>
    Promise.resolve({ ok: true, json: () => Promise.resolve(mockHooksStatus) }),
  ),
  installHooks: vi.fn(() =>
    Promise.resolve({ ok: true, json: () => Promise.resolve({}) }),
  ),
  removeHooks: vi.fn(() =>
    Promise.resolve({ ok: true, json: () => Promise.resolve({}) }),
  ),
}))

const HOST_ID = 'test-host'

beforeEach(() => {
  cleanup()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } },
  })
})

describe('HooksSection', () => {
  it('shows loading state initially', () => {
    // Mock a fetch that never resolves to keep loading state
    vi.mocked(fetchHooksStatus).mockReturnValueOnce(new Promise(() => {}))

    render(<HooksSection hostId={HOST_ID} />)
    expect(screen.getByText('Loading...')).toBeInTheDocument()
  })

  it('shows hooks status after fetch resolves', async () => {
    render(<HooksSection hostId={HOST_ID} />)
    await waitFor(() => {
      // tmux hooks badge + 3 event rows all show "Installed"
      expect(screen.getAllByText('Installed').length).toBeGreaterThanOrEqual(1)
    })
    // agent_hooks is false → "Not Installed" badge
    expect(screen.getByText('Not Installed')).toBeInTheDocument()
    // Hook events displayed from tmux_hooks map
    expect(screen.getByText('session-created')).toBeInTheDocument()
    expect(screen.getByText('session-closed')).toBeInTheDocument()
    expect(screen.getByText('session-renamed')).toBeInTheDocument()
  })

  it('Install button disabled when tmux hooks already installed', async () => {
    render(<HooksSection hostId={HOST_ID} />)
    await waitFor(() => {
      expect(screen.getAllByText('Installed').length).toBeGreaterThanOrEqual(1)
    })
    const installBtn = screen.getByRole('button', { name: /Install/i })
    expect(installBtn).toBeDisabled()
  })

  it('Remove button enabled when tmux hooks are installed', async () => {
    render(<HooksSection hostId={HOST_ID} />)
    await waitFor(() => {
      expect(screen.getAllByText('Installed').length).toBeGreaterThanOrEqual(1)
    })
    const removeBtn = screen.getByRole('button', { name: /Remove/i })
    expect(removeBtn).not.toBeDisabled()
  })

  it('offline host disables all buttons', async () => {
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      runtime: { [HOST_ID]: { status: 'disconnected' } },
    })
    render(<HooksSection hostId={HOST_ID} />)
    await waitFor(() => {
      expect(screen.getAllByText('Installed').length).toBeGreaterThanOrEqual(1)
    })
    const installBtn = screen.getByRole('button', { name: /Install/i })
    const removeBtn = screen.getByRole('button', { name: /Remove/i })
    const checkBtn = screen.getByRole('button', { name: /Check Status/i })
    expect(installBtn).toBeDisabled()
    expect(removeBtn).toBeDisabled()
    expect(checkBtn).toBeDisabled()
  })
})
