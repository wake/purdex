import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { OverviewSection } from './OverviewSection'
import { useHostStore } from '../../stores/useHostStore'

// Mock the host-api module
vi.mock('../../lib/host-api', () => ({
  hostFetch: vi.fn(),
  fetchInfo: vi.fn(),
  fetchHealth: vi.fn(),
}))

import { hostFetch, fetchInfo, fetchHealth } from '../../lib/host-api'

const mockHostFetch = vi.mocked(hostFetch)
const mockFetchInfo = vi.mocked(fetchInfo)
const mockFetchHealth = vi.mocked(fetchHealth)

const HOST_ID = 'test-host'

beforeEach(() => {
  vi.clearAllMocks()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test', ip: '1.2.3.4', port: 7860, order: 0, token: 'tbox_test' } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } },
  })

  // Default mocks: info + config fetches on mount
  mockFetchInfo.mockResolvedValue({
    ok: true,
    json: () => Promise.resolve({
      tbox_version: '1.0.0',
      tmux_version: '3.6',
      os: 'darwin',
      arch: 'arm64',
    }),
  } as Response)

  mockHostFetch.mockResolvedValue({
    ok: true,
    json: () => Promise.resolve({
      bind: '0.0.0.0',
      port: 7860,
      terminal: { sizing_mode: 'auto' },
      stream: { presets: [] },
      jsonl: { presets: [] },
      detect: { cc_commands: ['claude'], poll_interval: 5 },
    }),
  } as Response)
})

describe('OverviewSection', () => {
  it('returns null when host does not exist', () => {
    const { container } = render(<OverviewSection hostId="nonexistent" />)
    expect(container.innerHTML).toBe('')
  })

  it('renders host name heading', async () => {
    render(<OverviewSection hostId={HOST_ID} />)
    expect(screen.getByRole('heading', { level: 2, name: 'Test' })).toBeInTheDocument()
  })

  it('shows connection, daemon config, system info sections', async () => {
    render(<OverviewSection hostId={HOST_ID} />)

    // Connection section
    expect(screen.getByText('Connection')).toBeInTheDocument()

    // Daemon Config section
    expect(screen.getByText('Daemon Config')).toBeInTheDocument()

    // System Info section
    expect(screen.getByText('System Info')).toBeInTheDocument()
  })

  it('Test Connection button works', async () => {
    mockFetchHealth.mockResolvedValue({ ok: true } as Response)

    render(<OverviewSection hostId={HOST_ID} />)

    fireEvent.click(screen.getByText('Test Connection'))

    await waitFor(() => {
      expect(mockFetchHealth).toHaveBeenCalledWith(HOST_ID)
    })
  })

  it('shows success after successful test connection', async () => {
    mockFetchHealth.mockResolvedValue({ ok: true } as Response)

    render(<OverviewSection hostId={HOST_ID} />)

    fireEvent.click(screen.getByText('Test Connection'))

    await waitFor(() => {
      expect(screen.getByText(/Connected/)).toBeInTheDocument()
    })
  })

  it('Delete Host button shows confirmation', async () => {
    // Need more than 1 host for delete button to appear
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Test', ip: '1.2.3.4', port: 7860, order: 0, token: 'tbox_test' },
        'other-host': { id: 'other-host', name: 'Other', ip: '5.6.7.8', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, 'other-host'],
    })

    render(<OverviewSection hostId={HOST_ID} />)

    fireEvent.click(screen.getByText('Delete Host'))

    expect(screen.getByText('Are you sure you want to delete this host? All tabs connected to this host will be affected.')).toBeInTheDocument()
  })

  it('sizing mode dropdown has correct options', async () => {
    render(<OverviewSection hostId={HOST_ID} />)

    await waitFor(() => {
      const select = screen.getByDisplayValue('auto')
      expect(select).toBeInTheDocument()
    })

    const select = screen.getByDisplayValue('auto') as HTMLSelectElement
    const options = Array.from(select.options).map((o) => o.value)
    expect(options).toEqual(['auto', 'terminal-first', 'minimal-first'])
  })

  it('displays system info after fetch', async () => {
    render(<OverviewSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText('1.0.0')).toBeInTheDocument()
      expect(screen.getByText('3.6')).toBeInTheDocument()
      expect(screen.getByText('darwin / arm64')).toBeInTheDocument()
    })
  })

  it('displays daemon config after fetch', async () => {
    render(<OverviewSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText('claude')).toBeInTheDocument()
      expect(screen.getByText('0 preset(s)')).toBeInTheDocument()
    })
  })
})
