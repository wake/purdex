import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { LogsSection } from './LogsSection'
import { useHostStore } from '../../stores/useHostStore'

vi.mock('../../lib/host-api', () => ({
  hostFetch: vi.fn(),
}))

import { hostFetch } from '../../lib/host-api'

const mockHostFetch = vi.mocked(hostFetch)
const HOST_ID = 'test-host'

beforeEach(() => {
  vi.clearAllMocks()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'TestHost', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } },
  })
})

describe('LogsSection', () => {
  it('returns null when host does not exist', () => {
    const { container } = render(<LogsSection hostId="nonexistent" />)
    expect(container.innerHTML).toBe('')
  })

  it('renders host name and both blocks', async () => {
    mockHostFetch.mockResolvedValue({ ok: true, status: 204 } as Response)

    render(<LogsSection hostId={HOST_ID} />)

    expect(screen.getByRole('heading', { level: 2, name: 'TestHost' })).toBeInTheDocument()
    expect(screen.getByText('Daemon Log')).toBeInTheDocument()
    expect(screen.getByText('Crash Logs')).toBeInTheDocument()
  })
})

describe('DaemonLogBlock', () => {
  it('shows daemon log content', async () => {
    mockHostFetch.mockImplementation(async (_hostId, path) => {
      if (String(path).includes('/api/logs/daemon')) {
        return { ok: true, status: 200, text: () => Promise.resolve('log line 1\nlog line 2') } as Response
      }
      return { ok: true, status: 204 } as Response
    })

    render(<LogsSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText(/log line 1/)).toBeInTheDocument()
    })
  })

  it('shows offline message when host disconnected', () => {
    useHostStore.setState({
      runtime: { [HOST_ID]: { status: 'reconnecting' } },
    })

    render(<LogsSection hostId={HOST_ID} />)

    const offlineMessages = screen.getAllByText('Host is offline')
    expect(offlineMessages.length).toBeGreaterThanOrEqual(1)
  })

  it('refresh button fetches again', async () => {
    mockHostFetch.mockResolvedValue({
      ok: true, status: 200, text: () => Promise.resolve('initial log'),
    } as Response)

    render(<LogsSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getAllByText(/initial log/).length).toBeGreaterThanOrEqual(1)
    })

    mockHostFetch.mockResolvedValue({
      ok: true, status: 200, text: () => Promise.resolve('refreshed log'),
    } as Response)

    const refreshButtons = screen.getAllByText('Refresh')
    fireEvent.click(refreshButtons[0])

    await waitFor(() => {
      expect(screen.getAllByText(/refreshed log/).length).toBeGreaterThanOrEqual(1)
    })
  })
})

describe('CrashLogsBlock', () => {
  it('shows no crashes message when 204', async () => {
    mockHostFetch.mockResolvedValue({ ok: true, status: 204 } as Response)

    render(<LogsSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText('No crashes recorded')).toBeInTheDocument()
    })
  })

  it('shows crash content when available', async () => {
    mockHostFetch.mockImplementation(async (_hostId, path) => {
      if (String(path).includes('/api/logs/crash')) {
        return { ok: true, status: 200, text: () => Promise.resolve('Panic: test panic\nStack: ...') } as Response
      }
      return { ok: true, status: 204 } as Response
    })

    render(<LogsSection hostId={HOST_ID} />)

    await waitFor(() => {
      expect(screen.getByText(/Panic: test panic/)).toBeInTheDocument()
    })
  })
})
