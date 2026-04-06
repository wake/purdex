import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
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
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test', ip: '1.2.3.4', port: 7860, order: 0, token: 'purdex_testtoken' } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } },
  })

  // Default mocks: info + config fetches on mount
  mockFetchInfo.mockResolvedValue({
    ok: true,
    json: () => Promise.resolve({
      purdex_version: '1.0.0',
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
        [HOST_ID]: { id: HOST_ID, name: 'Test', ip: '1.2.3.4', port: 7860, order: 0, token: 'purdex_testtoken' },
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

/* ─── TokenField tests ─── */

describe('TokenField', () => {
  let fetchSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    fetchSpy = vi.spyOn(globalThis, 'fetch')
  })

  afterEach(() => {
    fetchSpy.mockRestore()
  })

  it('shows masked token when token exists', async () => {
    render(<OverviewSection hostId={HOST_ID} />)

    expect(screen.getByText('••••••••')).toBeInTheDocument()
  })

  it('shows — when no token', async () => {
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test', ip: '1.2.3.4', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      runtime: { [HOST_ID]: { status: 'connected' } },
    })

    render(<OverviewSection hostId={HOST_ID} />)

    expect(screen.getByText('—')).toBeInTheDocument()
  })

  it('Edit button opens editing mode with input and action buttons', async () => {
    render(<OverviewSection hostId={HOST_ID} />)

    // Find the Edit button inside the Token field row
    const editButtons = screen.getAllByText('Edit')
    // TokenField's Edit button — click the last one which belongs to TokenField
    // (other EditableFields also have Edit buttons)
    const tokenEditBtn = editButtons[editButtons.length - 1]
    fireEvent.click(tokenEditBtn)

    // Should show a password input with the current token value
    const input = screen.getByPlaceholderText('purdex_...')
    expect(input).toBeInTheDocument()
    expect(input).toHaveAttribute('type', 'password')
    expect(input).toHaveValue('purdex_testtoken')

    // Should show the token hint text
    expect(screen.getByText("Token is auto-generated during pairing or startup")).toBeInTheDocument()
  })

  it('eye toggle switches input between password and text', async () => {
    render(<OverviewSection hostId={HOST_ID} />)

    // Enter editing mode
    const editButtons = screen.getAllByText('Edit')
    fireEvent.click(editButtons[editButtons.length - 1])

    const input = screen.getByPlaceholderText('purdex_...')
    expect(input).toHaveAttribute('type', 'password')

    // Click the eye toggle button (in editing mode, it's a sibling of input)
    // The eye toggle in editing mode is next to the input
    const buttons = input.parentElement!.querySelectorAll('button')
    const eyeToggle = buttons[0] // first button after input is eye toggle
    fireEvent.click(eyeToggle)

    expect(input).toHaveAttribute('type', 'text')

    // Toggle back
    fireEvent.click(eyeToggle)
    expect(input).toHaveAttribute('type', 'password')
  })

  it('save validates token via fetch and calls onSave on success', async () => {
    fetchSpy.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve([]),
    } as Response)

    render(<OverviewSection hostId={HOST_ID} />)

    // Enter editing mode
    const editButtons = screen.getAllByText('Edit')
    fireEvent.click(editButtons[editButtons.length - 1])

    // Change the token value
    const input = screen.getByPlaceholderText('purdex_...')
    fireEvent.change(input, { target: { value: 'purdex_newtoken' } })

    // Click save (check mark button)
    const actionButtons = input.parentElement!.querySelectorAll('button')
    const saveBtn = actionButtons[1] // eye=0, save=1, cancel=2
    fireEvent.click(saveBtn)

    // Should show validating message
    expect(screen.getByText('Validating token...')).toBeInTheDocument()

    await waitFor(() => {
      // fetch was called with correct URL and auth header
      expect(fetchSpy).toHaveBeenCalledWith(
        'http://1.2.3.4:7860/api/sessions',
        { headers: { Authorization: 'Bearer purdex_newtoken' } },
      )
    })

    // After successful validation, editing mode should close
    await waitFor(() => {
      expect(screen.getByText('••••••••')).toBeInTheDocument()
    })

    // Store should be updated with new token
    const updatedHost = useHostStore.getState().hosts[HOST_ID]
    expect(updatedHost.token).toBe('purdex_newtoken')
  })

  it('401 response shows "Invalid token" error', async () => {
    fetchSpy.mockResolvedValueOnce({
      ok: false,
      status: 401,
      json: () => Promise.resolve({ error: 'unauthorized' }),
    } as Response)

    render(<OverviewSection hostId={HOST_ID} />)

    // Enter editing mode
    const editButtons = screen.getAllByText('Edit')
    fireEvent.click(editButtons[editButtons.length - 1])

    // Change the token
    const input = screen.getByPlaceholderText('purdex_...')
    fireEvent.change(input, { target: { value: 'purdex_bad' } })

    // Click save
    const actionButtons = input.parentElement!.querySelectorAll('button')
    const saveBtn = actionButtons[1]
    fireEvent.click(saveBtn)

    await waitFor(() => {
      expect(screen.getByText('Invalid token')).toBeInTheDocument()
    })

    // Should still be in editing mode
    expect(screen.getByPlaceholderText('purdex_...')).toBeInTheDocument()
  })

  it('cancel resets to original value and exits editing mode', async () => {
    render(<OverviewSection hostId={HOST_ID} />)

    // Enter editing mode
    const editButtons = screen.getAllByText('Edit')
    fireEvent.click(editButtons[editButtons.length - 1])

    // Change the token
    const input = screen.getByPlaceholderText('purdex_...')
    fireEvent.change(input, { target: { value: 'purdex_changed' } })
    expect(input).toHaveValue('purdex_changed')

    // Click cancel (X button)
    const actionButtons = input.parentElement!.querySelectorAll('button')
    const cancelBtn = actionButtons[2] // eye=0, save=1, cancel=2
    fireEvent.click(cancelBtn)

    // Should exit editing mode and show masked token
    expect(screen.getByText('••••••••')).toBeInTheDocument()
    expect(screen.queryByPlaceholderText('purdex_...')).not.toBeInTheDocument()

    // Store token should be unchanged
    expect(useHostStore.getState().hosts[HOST_ID].token).toBe('purdex_testtoken')
  })

  it('save with unchanged token closes editing without fetch', async () => {
    render(<OverviewSection hostId={HOST_ID} />)

    // Enter editing mode
    const editButtons = screen.getAllByText('Edit')
    fireEvent.click(editButtons[editButtons.length - 1])

    // Don't change the value — just save
    const input = screen.getByPlaceholderText('purdex_...')
    const actionButtons = input.parentElement!.querySelectorAll('button')
    const saveBtn = actionButtons[1]
    fireEvent.click(saveBtn)

    // Should exit editing immediately without calling fetch
    await waitFor(() => {
      expect(screen.getByText('••••••••')).toBeInTheDocument()
    })
    expect(fetchSpy).not.toHaveBeenCalled()
  })

  it('network error shows error message', async () => {
    fetchSpy.mockRejectedValueOnce(new Error('Network error'))

    render(<OverviewSection hostId={HOST_ID} />)

    // Enter editing mode
    const editButtons = screen.getAllByText('Edit')
    fireEvent.click(editButtons[editButtons.length - 1])

    // Change token and save
    const input = screen.getByPlaceholderText('purdex_...')
    fireEvent.change(input, { target: { value: 'purdex_fail' } })

    const actionButtons = input.parentElement!.querySelectorAll('button')
    const saveBtn = actionButtons[1]
    fireEvent.click(saveBtn)

    await waitFor(() => {
      expect(screen.getByText('Network error')).toBeInTheDocument()
    })

    // Should still be in editing mode
    expect(screen.getByPlaceholderText('purdex_...')).toBeInTheDocument()
  })

  it('non-401 error shows HTTP status', async () => {
    fetchSpy.mockResolvedValueOnce({
      ok: false,
      status: 500,
      json: () => Promise.resolve({}),
    } as Response)

    render(<OverviewSection hostId={HOST_ID} />)

    // Enter editing mode
    const editButtons = screen.getAllByText('Edit')
    fireEvent.click(editButtons[editButtons.length - 1])

    // Change token and save
    const input = screen.getByPlaceholderText('purdex_...')
    fireEvent.change(input, { target: { value: 'purdex_server_err' } })

    const actionButtons = input.parentElement!.querySelectorAll('button')
    const saveBtn = actionButtons[1]
    fireEvent.click(saveBtn)

    await waitFor(() => {
      expect(screen.getByText('HTTP 500')).toBeInTheDocument()
    })
  })

  it('Escape key cancels editing', async () => {
    render(<OverviewSection hostId={HOST_ID} />)

    // Enter editing mode
    const editButtons = screen.getAllByText('Edit')
    fireEvent.click(editButtons[editButtons.length - 1])

    const input = screen.getByPlaceholderText('purdex_...')
    fireEvent.change(input, { target: { value: 'purdex_changed' } })

    // Press Escape
    fireEvent.keyDown(input, { key: 'Escape' })

    // Should exit editing mode
    expect(screen.getByText('••••••••')).toBeInTheDocument()
    expect(screen.queryByPlaceholderText('purdex_...')).not.toBeInTheDocument()
  })

  it('Enter key triggers save', async () => {
    fetchSpy.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve([]),
    } as Response)

    render(<OverviewSection hostId={HOST_ID} />)

    // Enter editing mode
    const editButtons = screen.getAllByText('Edit')
    fireEvent.click(editButtons[editButtons.length - 1])

    const input = screen.getByPlaceholderText('purdex_...')
    fireEvent.change(input, { target: { value: 'purdex_enter' } })

    // Press Enter
    fireEvent.keyDown(input, { key: 'Enter' })

    // Should trigger validation fetch
    await waitFor(() => {
      expect(fetchSpy).toHaveBeenCalledWith(
        'http://1.2.3.4:7860/api/sessions',
        { headers: { Authorization: 'Bearer purdex_enter' } },
      )
    })
  })
})
