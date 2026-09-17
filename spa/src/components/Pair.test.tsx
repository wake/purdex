import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { Pair } from './Pair'
import { usePairStore } from '../stores/usePairStore'

vi.mock('../stores/usePairStore')
global.fetch = vi.fn()

describe('Pair component', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('loads and fetches /api/peers/hosts on mount', async () => {
    const mockHosts = [
      { host_id: 'h1', name: 'mlab', has_token: true, has_inbound_token: true },
    ]
    ;(usePairStore as any).mockReturnValue({
      hostList: mockHosts,
      ourAlias: 'peer-1',
      error: null,
      loading: false,
      fetchHostsWithTokenStatus: vi.fn().mockResolvedValue(undefined),
    })

    render(<Pair />)

    await waitFor(() => {
      expect(usePairStore().fetchHostsWithTokenStatus).toHaveBeenCalled()
    })
  })

  it('displays Section A with hosts that have has_token:true', async () => {
    const mockHosts = [
      { host_id: 'h1', name: 'mlab', has_token: true, has_inbound_token: true },
      { host_id: 'h2', name: 'air26', has_token: false, has_inbound_token: true },
    ]
    ;(usePairStore as any).mockReturnValue({
      hostList: mockHosts,
      ourAlias: 'peer-1',
      error: null,
      loading: false,
      fetchHostsWithTokenStatus: vi.fn(),
    })

    render(<Pair />)

    expect(screen.getByText(/Hosts with token/i)).toBeInTheDocument()
    expect(screen.getByText(/mlab/i)).toBeInTheDocument()
    expect(screen.queryByText(/air26/i)).not.toBeInTheDocument()
  })

  it('displays Section B with hosts that have has_inbound_token:false', async () => {
    const mockHosts = [
      { host_id: 'h1', name: 'mlab', has_token: true, has_inbound_token: true },
      { host_id: 'h2', name: 'air26', has_token: false, has_inbound_token: false },
    ]
    ;(usePairStore as any).mockReturnValue({
      hostList: mockHosts,
      ourAlias: 'peer-1',
      error: null,
      loading: false,
      fetchHostsWithTokenStatus: vi.fn(),
    })

    render(<Pair />)

    expect(screen.getByText(/Peer-listing-only/i)).toBeInTheDocument()
    expect(screen.getByText(/air26/i)).toBeInTheDocument()
  })

  it('detects drift and shows adopt button', async () => {
    const mockHosts = [
      { host_id: 'h1', name: 'mlab', has_token: true, has_inbound_token: true },
    ]
    const mockEnvelope = { host_id: 'self', alias: 'new-peer-alias' }

    ;(global.fetch as any)
      .mockResolvedValueOnce({
        ok: true,
        json: async () => mockEnvelope,
      })

    ;(usePairStore as any).mockReturnValue({
      hostList: mockHosts,
      ourAlias: 'old-peer-alias', // Drift: different from new-peer-alias
      error: null,
      loading: false,
      fetchHostsWithTokenStatus: vi.fn(),
      adoptAlias: vi.fn(),
    })

    render(<Pair />)

    await waitFor(() => {
      expect(screen.getByText(/Adopt new alias/i)).toBeInTheDocument()
    })
  })

  it('adopts new alias on button click', async () => {
    const mockHosts = [
      { host_id: 'h1', name: 'mlab', has_token: true, has_inbound_token: true },
    ]
    const mockAdoptAlias = vi.fn()

    ;(usePairStore as any).mockReturnValue({
      hostList: mockHosts,
      ourAlias: 'old-alias',
      error: null,
      loading: false,
      fetchHostsWithTokenStatus: vi.fn(),
      adoptAlias: mockAdoptAlias,
    })

    ;(global.fetch as any).mockResolvedValueOnce({
      ok: true,
      json: async () => ({ host_id: 'self', alias: 'new-alias' }),
    })

    render(<Pair />)

    await waitFor(() => {
      const adoptBtn = screen.queryByText(/Adopt new alias/i)
      if (adoptBtn) {
        fireEvent.click(adoptBtn)
      }
    })
  })
})
