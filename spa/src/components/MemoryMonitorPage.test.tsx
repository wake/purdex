import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryMonitorPage } from './MemoryMonitorPage'
import { useHostStore } from '../stores/useHostStore'
import {
  fetchMonitorConfig,
  fetchMonitorSnapshot,
  type MonitorConfig,
  type MonitorSnapshot,
} from '../lib/host-api'

vi.mock('../lib/host-api', () => ({
  fetchMonitorConfig: vi.fn(),
  fetchMonitorSnapshot: vi.fn(),
}))

const HOST_ID = 'host-a'

const monitorConfig: MonitorConfig = {
  refresh_interval_ms: 5000,
  top_process_limit: 10,
  bounds: {
    refresh_interval_ms: { min: 1000, max: 60000 },
    top_process_limit: { min: 1, max: 50 },
  },
}

const monitorSnapshot: MonitorSnapshot = {
  sampled_at: Date.UTC(2026, 3, 29, 12, 30, 0),
  host: {
    cpu: { percent: null, unavailable_reason: 'pending' },
    memory: { total_bytes: 1024, used_bytes: 256, used_percent: 25, unavailable_reason: null },
    disk: { total_bytes: 4096, used_bytes: 1024, used_percent: 25, unavailable_reason: null },
  },
  sessions: [],
  config: monitorConfig,
}

describe('MemoryMonitorPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.1', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      activeHostId: HOST_ID,
      runtime: {},
    })
    vi.mocked(fetchMonitorConfig).mockResolvedValue(monitorConfig)
    vi.mocked(fetchMonitorSnapshot).mockResolvedValue(monitorSnapshot)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('loads daemon monitor data for the active host without Electron metrics', async () => {
    render(<MemoryMonitorPage />)

    await waitFor(() => expect(fetchMonitorConfig).toHaveBeenCalledWith(HOST_ID))
    await waitFor(() => expect(fetchMonitorSnapshot).toHaveBeenCalledWith(HOST_ID))
    expect(screen.queryByText('Requires desktop app')).not.toBeInTheDocument()
  })

  it('renders host summary, sample time, and effective refresh config', async () => {
    render(<MemoryMonitorPage />)

    expect(await screen.findByRole('heading', { name: 'Performance Monitor' })).toBeInTheDocument()
    expect(screen.getByText('Host A')).toBeInTheDocument()
    expect(screen.getByText('Host Summary')).toBeInTheDocument()
    expect(screen.getByText('Pending')).toBeInTheDocument()
    expect(screen.getByText('256 B / 1 KB')).toBeInTheDocument()
    expect(screen.getAllByText('25.0%')).toHaveLength(2)
    expect(screen.getByText('1 KB / 4 KB')).toBeInTheDocument()
    expect(screen.getByText('Refresh: 5s')).toBeInTheDocument()
    expect(screen.getByText(/Sampled:/)).toBeInTheDocument()
  })

  it('shows stable unavailable reasons when host metrics are unavailable', async () => {
    vi.mocked(fetchMonitorSnapshot).mockResolvedValue({
      ...monitorSnapshot,
      host: {
        cpu: { percent: null, unavailable_reason: 'host_cpu_unavailable' },
        memory: { total_bytes: null, used_bytes: null, used_percent: null, unavailable_reason: 'host_memory_unavailable' },
        disk: { total_bytes: null, used_bytes: null, used_percent: null, unavailable_reason: 'host_disk_unavailable' },
      },
    })

    render(<MemoryMonitorPage />)

    expect(await screen.findByText('CPU unavailable')).toBeInTheDocument()
    expect(screen.getByText('Memory unavailable')).toBeInTheDocument()
    expect(screen.getByText('Disk unavailable')).toBeInTheDocument()
  })

  it('refreshes host snapshot at the effective monitor interval', async () => {
    const fastConfig = { ...monitorConfig, refresh_interval_ms: 10 }
    vi.mocked(fetchMonitorConfig).mockResolvedValue(fastConfig)
    vi.mocked(fetchMonitorSnapshot)
      .mockResolvedValueOnce(monitorSnapshot)
      .mockResolvedValue({
        ...monitorSnapshot,
        sampled_at: monitorSnapshot.sampled_at + 10,
        host: {
          ...monitorSnapshot.host,
          cpu: { percent: 80, unavailable_reason: null },
        },
      })

    render(<MemoryMonitorPage />)

    expect(await screen.findByText('Pending')).toBeInTheDocument()
    await waitFor(() => expect(vi.mocked(fetchMonitorSnapshot).mock.calls.length).toBeGreaterThanOrEqual(2))
    expect(await screen.findByText('80.0%')).toBeInTheDocument()
  })

  it('keeps retrying after a transient refresh failure while preserving the last summary', async () => {
    const fastConfig = { ...monitorConfig, refresh_interval_ms: 10 }
    vi.mocked(fetchMonitorConfig).mockResolvedValue(fastConfig)
    vi.mocked(fetchMonitorSnapshot)
      .mockResolvedValueOnce(monitorSnapshot)
      .mockRejectedValueOnce(new Error('temporary failure'))
      .mockResolvedValue({
        ...monitorSnapshot,
        sampled_at: monitorSnapshot.sampled_at + 20,
        host: {
          ...monitorSnapshot.host,
          cpu: { percent: 80, unavailable_reason: null },
        },
      })

    render(<MemoryMonitorPage />)

    expect(await screen.findByText('Pending')).toBeInTheDocument()
    expect(await screen.findByText('Unable to load monitor data: temporary failure')).toBeInTheDocument()
    expect(screen.getByText('Pending')).toBeInTheDocument()
    await waitFor(() => expect(vi.mocked(fetchMonitorSnapshot).mock.calls.length).toBeGreaterThanOrEqual(3))
    expect(await screen.findByText('80.0%')).toBeInTheDocument()
  })

  it('uses fetched config interval when retrying after an initial snapshot failure', async () => {
    const fastConfig = { ...monitorConfig, refresh_interval_ms: 10 }
    vi.mocked(fetchMonitorConfig).mockResolvedValue(fastConfig)
    vi.mocked(fetchMonitorSnapshot)
      .mockRejectedValueOnce(new Error('initial snapshot failed'))
      .mockResolvedValue({
        ...monitorSnapshot,
        host: {
          ...monitorSnapshot.host,
          cpu: { percent: 80, unavailable_reason: null },
        },
      })

    render(<MemoryMonitorPage />)

    expect(await screen.findByText('Unable to load monitor data: initial snapshot failed')).toBeInTheDocument()
    await waitFor(() => expect(vi.mocked(fetchMonitorSnapshot).mock.calls.length).toBeGreaterThanOrEqual(2))
    expect(await screen.findByText('80.0%')).toBeInTheDocument()
  })

  it('does not fetch when the active host record is missing', async () => {
    useHostStore.setState({ hosts: {}, hostOrder: [], activeHostId: HOST_ID })

    render(<MemoryMonitorPage />)

    expect(screen.getByText('Select a host to view performance metrics.')).toBeInTheDocument()
    expect(fetchMonitorConfig).not.toHaveBeenCalled()
    expect(fetchMonitorSnapshot).not.toHaveBeenCalled()
  })

  it('shows an error when monitor data cannot be loaded', async () => {
    vi.mocked(fetchMonitorSnapshot).mockRejectedValue(new Error('snapshot failed'))

    render(<MemoryMonitorPage />)

    expect(await screen.findByText('Unable to load monitor data: snapshot failed')).toBeInTheDocument()
  })
})
