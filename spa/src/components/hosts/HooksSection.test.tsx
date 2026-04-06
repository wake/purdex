import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, waitFor, fireEvent } from '@testing-library/react'
import { HooksSection } from './HooksSection'
import { useHostStore } from '../../stores/useHostStore'

const mockFetchStatus = vi.fn(() => Promise.resolve({
  installed: true,
  events: { 'event-a': { installed: true } },
  issues: [] as string[],
}))

const mockSetup = vi.fn(() => Promise.resolve({
  installed: true,
  events: { 'event-a': { installed: true } },
  issues: [] as string[],
}))

vi.mock('../../lib/hook-modules', () => ({
  HOOK_MODULES: [
    {
      id: 'test-mod-1',
      labelKey: 'hosts.tmux_hooks',
      descKey: 'hosts.tmux_hooks_desc',
      fetchStatus: (hostId: string) => mockFetchStatus(hostId),
      setup: (hostId: string, action: string) => mockSetup(hostId, action),
    },
    {
      id: 'test-mod-2',
      labelKey: 'hosts.agent_hooks',
      descKey: 'hosts.agent_hooks_desc',
      fetchStatus: (hostId: string) => mockFetchStatus(hostId),
      setup: (hostId: string, action: string) => mockSetup(hostId, action),
    },
  ],
}))

const HOST_ID = 'test-host'

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } },
  })
})

describe('HooksSection', () => {
  it('renders a card for each hook module', async () => {
    render(<HooksSection hostId={HOST_ID} />)
    await waitFor(() => {
      expect(screen.getAllByText('Installed').length).toBeGreaterThanOrEqual(2)
    })
  })

  it('renders error when fetch fails', async () => {
    mockFetchStatus.mockRejectedValueOnce(new Error('503 Service Unavailable'))
    render(<HooksSection hostId={HOST_ID} />)
    await waitFor(() => {
      expect(screen.getByText(/503/)).toBeInTheDocument()
    })
  })

  it('global refresh re-fetches all cards', async () => {
    render(<HooksSection hostId={HOST_ID} />)
    await waitFor(() => expect(mockFetchStatus).toHaveBeenCalledTimes(2))
    const refreshBtn = screen.getByRole('button', { name: /Check Status/i })
    fireEvent.click(refreshBtn)
    await waitFor(() => expect(mockFetchStatus).toHaveBeenCalledTimes(4))
  })
})
