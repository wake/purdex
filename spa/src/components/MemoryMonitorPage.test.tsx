import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryMonitorPage } from './MemoryMonitorPage'
import { useWorkspaceStore } from '../features/workspace/store'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import {
  fetchMonitorConfig,
  fetchMonitorSnapshot,
  updateMonitorConfig,
  type MonitorConfig,
  type MonitorSnapshot,
} from '../lib/host-api'
import type { PaneContent, Tab } from '../types/tab'

vi.mock('../lib/host-api', () => ({
  fetchMonitorConfig: vi.fn(),
  fetchMonitorSnapshot: vi.fn(),
  updateMonitorConfig: vi.fn(),
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
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
    vi.mocked(fetchMonitorConfig).mockResolvedValue(monitorConfig)
    vi.mocked(fetchMonitorSnapshot).mockResolvedValue(monitorSnapshot)
    vi.mocked(updateMonitorConfig).mockResolvedValue(monitorConfig)
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

  it('renders monitor settings controls from daemon config bounds', async () => {
    render(<MemoryMonitorPage />)

    expect(await screen.findByRole('group', { name: 'Monitor Settings' })).toBeInTheDocument()
    const refreshInput = screen.getByLabelText('Refresh interval seconds')
    const topLimitInput = screen.getByLabelText('Top process limit')
    expect(refreshInput).toHaveValue(5)
    expect(refreshInput).toHaveAttribute('min', '1')
    expect(refreshInput).toHaveAttribute('max', '60')
    expect(topLimitInput).toHaveValue(10)
    expect(topLimitInput).toHaveAttribute('min', '1')
    expect(topLimitInput).toHaveAttribute('max', '50')
  })

  it('updates monitor config controls through the active host', async () => {
    vi.mocked(updateMonitorConfig).mockResolvedValue({
      ...monitorConfig,
      refresh_interval_ms: 12000,
      top_process_limit: 20,
    })
    render(<MemoryMonitorPage />)

    const refreshInput = await screen.findByLabelText('Refresh interval seconds')
    fireEvent.change(refreshInput, { target: { value: '12' } })
    fireEvent.change(screen.getByLabelText('Top process limit'), { target: { value: '20' } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply monitor settings' }))

    await waitFor(() => expect(updateMonitorConfig).toHaveBeenCalledWith(HOST_ID, {
      refresh_interval_ms: 12000,
      top_process_limit: 20,
    }))
    expect(await screen.findByText('Refresh: 12s')).toBeInTheDocument()
    expect(screen.getByLabelText('Top process limit')).toHaveValue(20)
  })

  it('does not overwrite dirty monitor setting drafts during polling refreshes', async () => {
    const fastConfig = { ...monitorConfig, refresh_interval_ms: 10 }
    vi.mocked(fetchMonitorConfig).mockResolvedValue(fastConfig)
    render(<MemoryMonitorPage />)

    const refreshInput = await screen.findByLabelText('Refresh interval seconds')
    fireEvent.change(refreshInput, { target: { value: '12' } })

    await waitFor(() => expect(vi.mocked(fetchMonitorSnapshot).mock.calls.length).toBeGreaterThanOrEqual(2))
    expect(refreshInput).toHaveValue(12)
  })

  it('reloads monitor data immediately after applying a new refresh interval', async () => {
    const slowConfig = { ...monitorConfig, refresh_interval_ms: 60000 }
    vi.mocked(fetchMonitorConfig).mockResolvedValue(slowConfig)
    vi.mocked(updateMonitorConfig).mockResolvedValue({ ...slowConfig, refresh_interval_ms: 12000 })
    render(<MemoryMonitorPage />)

    const refreshInput = await screen.findByLabelText('Refresh interval seconds')
    fireEvent.change(refreshInput, { target: { value: '12' } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply monitor settings' }))

    await waitFor(() => expect(updateMonitorConfig).toHaveBeenCalled())
    await waitFor(() => expect(fetchMonitorConfig).toHaveBeenCalledTimes(2))
  })

  it('does not submit blank monitor setting drafts', async () => {
    render(<MemoryMonitorPage />)

    const refreshInput = await screen.findByLabelText('Refresh interval seconds')
    fireEvent.change(refreshInput, { target: { value: '' } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply monitor settings' }))

    expect(updateMonitorConfig).not.toHaveBeenCalled()
    expect(refreshInput).toHaveValue(5)
  })

  it('hides monitor settings while the same active host id reloads for a changed endpoint', async () => {
    vi.mocked(fetchMonitorSnapshot).mockImplementation((hostId) => {
      const currentHost = useHostStore.getState().hosts[hostId]
      if (currentHost?.ip === '100.64.0.22') return new Promise<MonitorSnapshot>(() => {})
      return Promise.resolve(monitorSnapshot)
    })
    render(<MemoryMonitorPage />)

    expect(await screen.findByRole('group', { name: 'Monitor Settings' })).toBeInTheDocument()
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.22', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      activeHostId: HOST_ID,
      runtime: {},
    })

    await waitFor(() => expect(screen.queryByRole('group', { name: 'Monitor Settings' })).not.toBeInTheDocument())
  })

  it('ignores a pending config update result after the active host endpoint changes', async () => {
    let resolveUpdate: (config: MonitorConfig) => void = () => {}
    vi.mocked(updateMonitorConfig).mockReturnValue(new Promise((resolve) => {
      resolveUpdate = resolve
    }))
    vi.mocked(fetchMonitorSnapshot).mockImplementation((hostId) => {
      const currentHost = useHostStore.getState().hosts[hostId]
      if (currentHost?.ip === '100.64.0.22') return new Promise<MonitorSnapshot>(() => {})
      return Promise.resolve(monitorSnapshot)
    })
    render(<MemoryMonitorPage />)

    const refreshInput = await screen.findByLabelText('Refresh interval seconds')
    fireEvent.change(refreshInput, { target: { value: '12' } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply monitor settings' }))
    await waitFor(() => expect(updateMonitorConfig).toHaveBeenCalled())

    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.22', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      activeHostId: HOST_ID,
      runtime: {},
    })
    resolveUpdate({ ...monitorConfig, refresh_interval_ms: 12000 })

    await waitFor(() => expect(screen.queryByText('Refresh: 12s')).not.toBeInTheDocument())
  })

  it('ignores a pending config update rejection after the active host endpoint changes', async () => {
    let rejectUpdate: (error: Error) => void = () => {}
    vi.mocked(updateMonitorConfig).mockReturnValue(new Promise((_, reject) => {
      rejectUpdate = reject
    }))
    vi.mocked(fetchMonitorSnapshot).mockImplementation((hostId) => {
      const currentHost = useHostStore.getState().hosts[hostId]
      if (currentHost?.ip === '100.64.0.22') return new Promise<MonitorSnapshot>(() => {})
      return Promise.resolve(monitorSnapshot)
    })
    render(<MemoryMonitorPage />)

    const refreshInput = await screen.findByLabelText('Refresh interval seconds')
    fireEvent.change(refreshInput, { target: { value: '12' } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply monitor settings' }))
    await waitFor(() => expect(updateMonitorConfig).toHaveBeenCalled())

    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.22', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      activeHostId: HOST_ID,
      runtime: {},
    })
    rejectUpdate(new Error('old endpoint failed'))

    await waitFor(() => expect(screen.queryByText('Unable to load monitor data: old endpoint failed')).not.toBeInTheDocument())
  })

  it('ignores a pending config update result after switching active hosts', async () => {
    let resolveUpdate: (config: MonitorConfig) => void = () => {}
    vi.mocked(updateMonitorConfig).mockReturnValue(new Promise((resolve) => {
      resolveUpdate = resolve
    }))
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.1', port: 7860, order: 0 },
        'host-b': { id: 'host-b', name: 'Host B', ip: '100.64.0.2', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, 'host-b'],
      activeHostId: HOST_ID,
      runtime: {},
    })
    render(<MemoryMonitorPage />)

    const refreshInput = await screen.findByLabelText('Refresh interval seconds')
    fireEvent.change(refreshInput, { target: { value: '12' } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply monitor settings' }))
    await waitFor(() => expect(updateMonitorConfig).toHaveBeenCalled())

    useHostStore.setState({ activeHostId: 'host-b' })
    resolveUpdate({ ...monitorConfig, refresh_interval_ms: 12000 })

    await waitFor(() => expect(screen.queryByText('Refresh: 12s')).not.toBeInTheDocument())
  })

  it('does not disable the new active host settings while another host update is pending', async () => {
    vi.mocked(updateMonitorConfig).mockReturnValue(new Promise(() => {}))
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.1', port: 7860, order: 0 },
        'host-b': { id: 'host-b', name: 'Host B', ip: '100.64.0.2', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, 'host-b'],
      activeHostId: HOST_ID,
      runtime: {},
    })
    render(<MemoryMonitorPage />)

    const refreshInput = await screen.findByLabelText('Refresh interval seconds')
    fireEvent.change(refreshInput, { target: { value: '12' } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply monitor settings' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Applying...' })).toBeDisabled())

    useHostStore.setState({ activeHostId: 'host-b' })

    expect(await screen.findByRole('button', { name: 'Apply monitor settings' })).not.toBeDisabled()
  })

  it('keeps each host endpoint disabled while its own update is pending', async () => {
    vi.mocked(updateMonitorConfig).mockReturnValue(new Promise(() => {}))
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.1', port: 7860, order: 0 },
        'host-b': { id: 'host-b', name: 'Host B', ip: '100.64.0.2', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, 'host-b'],
      activeHostId: HOST_ID,
      runtime: {},
    })
    render(<MemoryMonitorPage />)

    fireEvent.change(await screen.findByLabelText('Refresh interval seconds'), { target: { value: '12' } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply monitor settings' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Applying...' })).toBeDisabled())

    useHostStore.setState({ activeHostId: 'host-b' })
    fireEvent.change(await screen.findByLabelText('Refresh interval seconds'), { target: { value: '15' } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply monitor settings' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Applying...' })).toBeDisabled())

    useHostStore.setState({ activeHostId: HOST_ID })

    expect(await screen.findByRole('button', { name: 'Applying...' })).toBeDisabled()
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

  it('renders one monitor row for each open leaf pane', async () => {
    useTabStore.setState({
      tabs: {
        'tab-dashboard': tabWithLeaf('tab-dashboard', 'pane-dashboard', { kind: 'dashboard' }),
        'tab-split': {
          id: 'tab-split',
          pinned: false,
          locked: false,
          createdAt: 2,
          layout: {
            type: 'split',
            id: 'split-root',
            direction: 'h',
            sizes: [50, 50],
            children: [
              { type: 'leaf', pane: { id: 'pane-session', content: { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'abc123', mode: 'terminal', cachedName: 'Work', tmuxInstance: 'main' } } },
              { type: 'leaf', pane: { id: 'pane-browser', content: { kind: 'browser', url: 'https://example.com' } } },
            ],
          },
        },
      },
      tabOrder: ['tab-dashboard', 'tab-split'],
      activeTabId: 'tab-split',
      visitHistory: [],
    })

    render(<MemoryMonitorPage />)

    expect(await screen.findByText('Open Panes')).toBeInTheDocument()

    expect(screen.getAllByTestId(/^monitor-row-/).map((row) => row.getAttribute('data-testid'))).toEqual([
      'monitor-row-tab-dashboard-pane-dashboard',
      'monitor-row-tab-split-pane-session',
      'monitor-row-tab-split-pane-browser',
    ])

    const dashboardRow = screen.getByTestId('monitor-row-tab-dashboard-pane-dashboard')
    expect(within(dashboardRow).getByText('tab-dashboard')).toBeInTheDocument()
    expect(within(dashboardRow).getByText('pane-dashboard')).toBeInTheDocument()
    expect(within(dashboardRow).getByText('dashboard')).toBeInTheDocument()

    const sessionRow = screen.getByTestId('monitor-row-tab-split-pane-session')
    expect(within(sessionRow).getByText('tab-split')).toBeInTheDocument()
    expect(within(sessionRow).getByText('pane-session')).toBeInTheDocument()
    expect(within(sessionRow).getByText('tmux-session')).toBeInTheDocument()

    const browserRow = screen.getByTestId('monitor-row-tab-split-pane-browser')
    expect(within(browserRow).getByText('tab-split')).toBeInTheDocument()
    expect(within(browserRow).getByText('pane-browser')).toBeInTheDocument()
    expect(within(browserRow).getByText('browser')).toBeInTheDocument()

    for (const row of [dashboardRow, sessionRow, browserRow]) {
      expect(within(row).getAllByText('Not wired')).toHaveLength(2)
    }
  })

  it('renders daemon metrics for matching active-host session panes', async () => {
    useTabStore.setState({
      tabs: {
        'tab-session': tabWithLeaf('tab-session', 'pane-session', {
          kind: 'tmux-session',
          hostId: HOST_ID,
          sessionCode: 'abc123',
          mode: 'terminal',
          cachedName: 'Work',
          tmuxInstance: 'main',
        }),
      },
      tabOrder: ['tab-session'],
      activeTabId: 'tab-session',
      visitHistory: [],
    })
    vi.mocked(fetchMonitorSnapshot).mockResolvedValue({
      ...monitorSnapshot,
      sessions: [{
        session_code: 'abc123',
        tmux_session: { id: '$1', name: 'pdx-abc123' },
        daemon: {
          cpu_percent: 12.3,
          memory_bytes: 2048,
          process_count: 3,
          top_processes: [],
          unavailable_reason: null,
        },
      }],
    })

    render(<MemoryMonitorPage />)

    const sessionRow = await screen.findByTestId('monitor-row-tab-session-pane-session')
    expect(within(sessionRow).getByText('CPU 12.3%')).toBeInTheDocument()
    expect(within(sessionRow).getByText('Memory 2 KB')).toBeInTheDocument()
    expect(within(sessionRow).getByText('3 processes')).toBeInTheDocument()
    expect(within(sessionRow).getAllByText('Not wired')).toHaveLength(1)
  })

  it('renders daemon unavailable reasons for matching session panes', async () => {
    useTabStore.setState({
      tabs: {
        'tab-session': tabWithLeaf('tab-session', 'pane-session', {
          kind: 'tmux-session',
          hostId: HOST_ID,
          sessionCode: 'abc123',
          mode: 'terminal',
          cachedName: 'Work',
          tmuxInstance: 'main',
        }),
      },
      tabOrder: ['tab-session'],
      activeTabId: 'tab-session',
      visitHistory: [],
    })
    vi.mocked(fetchMonitorSnapshot).mockResolvedValue({
      ...monitorSnapshot,
      sessions: [{
        session_code: 'abc123',
        tmux_session: { id: '$1', name: 'pdx-abc123' },
        daemon: {
          cpu_percent: null,
          memory_bytes: null,
          process_count: null,
          top_processes: [],
          unavailable_reason: 'process_table_unavailable',
        },
      }],
    })

    render(<MemoryMonitorPage />)

    const sessionRow = await screen.findByTestId('monitor-row-tab-session-pane-session')
    expect(within(sessionRow).getByText('Process table unavailable')).toBeInTheDocument()
    expect(within(sessionRow).getAllByText('Not wired')).toHaveLength(1)
  })

  it('renders daemon metrics for session panes from their own host snapshots', async () => {
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.1', port: 7860, order: 0 },
        'host-b': { id: 'host-b', name: 'Host B', ip: '100.64.0.2', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, 'host-b'],
      activeHostId: HOST_ID,
      runtime: {},
    })
    useTabStore.setState({
      tabs: {
        'tab-a': tabWithLeaf('tab-a', 'pane-a', {
          kind: 'tmux-session',
          hostId: HOST_ID,
          sessionCode: 'same-code',
          mode: 'terminal',
          cachedName: 'A',
          tmuxInstance: 'main',
        }),
        'tab-b': tabWithLeaf('tab-b', 'pane-b', {
          kind: 'tmux-session',
          hostId: 'host-b',
          sessionCode: 'same-code',
          mode: 'terminal',
          cachedName: 'B',
          tmuxInstance: 'main',
        }),
      },
      tabOrder: ['tab-a', 'tab-b'],
      activeTabId: 'tab-a',
      visitHistory: [],
    })
    vi.mocked(fetchMonitorSnapshot).mockImplementation(async (hostId) => ({
      ...monitorSnapshot,
      sessions: [{
        session_code: 'same-code',
        tmux_session: { id: hostId === HOST_ID ? '$1' : '$2', name: `pdx-${hostId}` },
        daemon: {
          cpu_percent: hostId === HOST_ID ? 12.3 : 45.6,
          memory_bytes: hostId === HOST_ID ? 2048 : 4096,
          process_count: hostId === HOST_ID ? 3 : 7,
          top_processes: [],
          unavailable_reason: null,
        },
      }],
    }))

    render(<MemoryMonitorPage />)

    await waitFor(() => expect(fetchMonitorSnapshot).toHaveBeenCalledWith(HOST_ID))
    await waitFor(() => expect(fetchMonitorSnapshot).toHaveBeenCalledWith('host-b'))

    const hostARow = await screen.findByTestId('monitor-row-tab-a-pane-a')
    expect(within(hostARow).getByText('CPU 12.3%')).toBeInTheDocument()
    expect(within(hostARow).getByText('Memory 2 KB')).toBeInTheDocument()
    expect(within(hostARow).getByText('3 processes')).toBeInTheDocument()

    const hostBRow = await screen.findByTestId('monitor-row-tab-b-pane-b')
    expect(within(hostBRow).getByText('CPU 45.6%')).toBeInTheDocument()
    expect(within(hostBRow).getByText('Memory 4 KB')).toBeInTheDocument()
    expect(within(hostBRow).getByText('7 processes')).toBeInTheDocument()
  })

  it('keeps active-host rows fresh when a secondary host snapshot fails', async () => {
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.1', port: 7860, order: 0 },
        'host-b': { id: 'host-b', name: 'Host B', ip: '100.64.0.2', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, 'host-b'],
      activeHostId: HOST_ID,
      runtime: {},
    })
    useTabStore.setState({
      tabs: {
        'tab-a': tabWithLeaf('tab-a', 'pane-a', {
          kind: 'tmux-session',
          hostId: HOST_ID,
          sessionCode: 'active-code',
          mode: 'terminal',
          cachedName: 'A',
          tmuxInstance: 'main',
        }),
        'tab-b': tabWithLeaf('tab-b', 'pane-b', {
          kind: 'tmux-session',
          hostId: 'host-b',
          sessionCode: 'remote-code',
          mode: 'terminal',
          cachedName: 'B',
          tmuxInstance: 'main',
        }),
      },
      tabOrder: ['tab-a', 'tab-b'],
      activeTabId: 'tab-a',
      visitHistory: [],
    })
    vi.mocked(fetchMonitorSnapshot).mockImplementation(async (hostId) => {
      if (hostId === 'host-b') throw new Error('remote snapshot failed')
      return {
        ...monitorSnapshot,
        sessions: [{
          session_code: 'active-code',
          tmux_session: { id: '$1', name: 'pdx-active' },
          daemon: {
            cpu_percent: 12.3,
            memory_bytes: 2048,
            process_count: 3,
            top_processes: [],
            unavailable_reason: null,
          },
        }],
      }
    })

    render(<MemoryMonitorPage />)

    expect(await screen.findByText('Unable to load monitor data: host-b: remote snapshot failed')).toBeInTheDocument()
    const hostARow = await screen.findByTestId('monitor-row-tab-a-pane-a')
    expect(within(hostARow).getByText('CPU 12.3%')).toBeInTheDocument()
    const hostBRow = await screen.findByTestId('monitor-row-tab-b-pane-b')
    expect(within(hostBRow).getAllByText('Not wired')).toHaveLength(2)
  })

  it('renders active-host rows without waiting for a slow secondary host snapshot', async () => {
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.1', port: 7860, order: 0 },
        'host-b': { id: 'host-b', name: 'Host B', ip: '100.64.0.2', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, 'host-b'],
      activeHostId: HOST_ID,
      runtime: {},
    })
    useTabStore.setState({
      tabs: {
        'tab-a': tabWithLeaf('tab-a', 'pane-a', {
          kind: 'tmux-session',
          hostId: HOST_ID,
          sessionCode: 'active-code',
          mode: 'terminal',
          cachedName: 'A',
          tmuxInstance: 'main',
        }),
        'tab-b': tabWithLeaf('tab-b', 'pane-b', {
          kind: 'tmux-session',
          hostId: 'host-b',
          sessionCode: 'remote-code',
          mode: 'terminal',
          cachedName: 'B',
          tmuxInstance: 'main',
        }),
      },
      tabOrder: ['tab-a', 'tab-b'],
      activeTabId: 'tab-a',
      visitHistory: [],
    })
    vi.mocked(fetchMonitorSnapshot).mockImplementation((hostId) => {
      if (hostId === 'host-b') return new Promise<MonitorSnapshot>(() => {})
      return Promise.resolve({
        ...monitorSnapshot,
        sessions: [{
          session_code: 'active-code',
          tmux_session: { id: '$1', name: 'pdx-active' },
          daemon: {
            cpu_percent: 12.3,
            memory_bytes: 2048,
            process_count: 3,
            top_processes: [],
            unavailable_reason: null,
          },
        }],
      })
    })

    render(<MemoryMonitorPage />)

    await waitFor(() => expect(fetchMonitorSnapshot).toHaveBeenCalledWith('host-b'))
    const hostARow = await screen.findByTestId('monitor-row-tab-a-pane-a')
    expect(within(hostARow).getByText('CPU 12.3%')).toBeInTheDocument()
    const hostBRow = await screen.findByTestId('monitor-row-tab-b-pane-b')
    expect(within(hostBRow).getAllByText('Not wired')).toHaveLength(2)
  })

  it('renders a fast secondary host row without waiting for another slow secondary host', async () => {
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.1', port: 7860, order: 0 },
        'host-b': { id: 'host-b', name: 'Host B', ip: '100.64.0.2', port: 7860, order: 1 },
        'host-c': { id: 'host-c', name: 'Host C', ip: '100.64.0.3', port: 7860, order: 2 },
      },
      hostOrder: [HOST_ID, 'host-b', 'host-c'],
      activeHostId: HOST_ID,
      runtime: {},
    })
    useTabStore.setState({
      tabs: {
        'tab-a': tabWithLeaf('tab-a', 'pane-a', {
          kind: 'tmux-session',
          hostId: HOST_ID,
          sessionCode: 'active-code',
          mode: 'terminal',
          cachedName: 'A',
          tmuxInstance: 'main',
        }),
        'tab-b': tabWithLeaf('tab-b', 'pane-b', {
          kind: 'tmux-session',
          hostId: 'host-b',
          sessionCode: 'fast-code',
          mode: 'terminal',
          cachedName: 'B',
          tmuxInstance: 'main',
        }),
        'tab-c': tabWithLeaf('tab-c', 'pane-c', {
          kind: 'tmux-session',
          hostId: 'host-c',
          sessionCode: 'slow-code',
          mode: 'terminal',
          cachedName: 'C',
          tmuxInstance: 'main',
        }),
      },
      tabOrder: ['tab-a', 'tab-b', 'tab-c'],
      activeTabId: 'tab-a',
      visitHistory: [],
    })
    vi.mocked(fetchMonitorSnapshot).mockImplementation((hostId) => {
      if (hostId === 'host-c') return new Promise<MonitorSnapshot>(() => {})
      return Promise.resolve({
        ...monitorSnapshot,
        sessions: hostId === 'host-b' ? [{
          session_code: 'fast-code',
          tmux_session: { id: '$2', name: 'pdx-fast' },
          daemon: {
            cpu_percent: 45.6,
            memory_bytes: 4096,
            process_count: 7,
            top_processes: [],
            unavailable_reason: null,
          },
        }] : [{
          session_code: 'active-code',
          tmux_session: { id: '$1', name: 'pdx-active' },
          daemon: {
            cpu_percent: 12.3,
            memory_bytes: 2048,
            process_count: 3,
            top_processes: [],
            unavailable_reason: null,
          },
        }],
      })
    })

    render(<MemoryMonitorPage />)

    await waitFor(() => expect(fetchMonitorSnapshot).toHaveBeenCalledWith('host-c'))
    const hostBRow = await screen.findByTestId('monitor-row-tab-b-pane-b')
    expect(within(hostBRow).getByText('CPU 45.6%')).toBeInTheDocument()
    expect(within(hostBRow).getByText('Memory 4 KB')).toBeInTheDocument()
    expect(within(hostBRow).getByText('7 processes')).toBeInTheDocument()
    const hostCRow = await screen.findByTestId('monitor-row-tab-c-pane-c')
    expect(within(hostCRow).getAllByText('Not wired')).toHaveLength(2)
  })

  it('preserves secondary host metrics across active-host refreshes while the next secondary request is pending', async () => {
    const fastConfig = { ...monitorConfig, refresh_interval_ms: 10 }
    vi.mocked(fetchMonitorConfig).mockResolvedValue(fastConfig)
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.1', port: 7860, order: 0 },
        'host-b': { id: 'host-b', name: 'Host B', ip: '100.64.0.2', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, 'host-b'],
      activeHostId: HOST_ID,
      runtime: {},
    })
    useTabStore.setState({
      tabs: {
        'tab-a': tabWithLeaf('tab-a', 'pane-a', {
          kind: 'tmux-session',
          hostId: HOST_ID,
          sessionCode: 'active-code',
          mode: 'terminal',
          cachedName: 'A',
          tmuxInstance: 'main',
        }),
        'tab-b': tabWithLeaf('tab-b', 'pane-b', {
          kind: 'tmux-session',
          hostId: 'host-b',
          sessionCode: 'remote-code',
          mode: 'terminal',
          cachedName: 'B',
          tmuxInstance: 'main',
        }),
      },
      tabOrder: ['tab-a', 'tab-b'],
      activeTabId: 'tab-a',
      visitHistory: [],
    })
    let hostBCalls = 0
    vi.mocked(fetchMonitorSnapshot).mockImplementation((hostId) => {
      if (hostId === 'host-b') {
        hostBCalls += 1
        if (hostBCalls > 1) return new Promise<MonitorSnapshot>(() => {})
        return Promise.resolve({
          ...monitorSnapshot,
          sessions: [{
            session_code: 'remote-code',
            tmux_session: { id: '$2', name: 'pdx-remote' },
            daemon: {
              cpu_percent: 45.6,
              memory_bytes: 4096,
              process_count: 7,
              top_processes: [],
              unavailable_reason: null,
            },
          }],
        })
      }
      return Promise.resolve({
        ...monitorSnapshot,
        sessions: [{
          session_code: 'active-code',
          tmux_session: { id: '$1', name: 'pdx-active' },
          daemon: {
            cpu_percent: 12.3,
            memory_bytes: 2048,
            process_count: 3,
            top_processes: [],
            unavailable_reason: null,
          },
        }],
      })
    })

    render(<MemoryMonitorPage />)

    const hostBRow = await screen.findByTestId('monitor-row-tab-b-pane-b')
    expect(within(hostBRow).getByText('CPU 45.6%')).toBeInTheDocument()
    await waitFor(() => {
      const activeHostCalls = vi.mocked(fetchMonitorSnapshot).mock.calls.filter(([hostId]) => hostId === HOST_ID)
      expect(activeHostCalls.length).toBeGreaterThanOrEqual(2)
    })
    expect(within(hostBRow).getByText('CPU 45.6%')).toBeInTheDocument()
  })

  it('does not apply active-host daemon metrics to session panes from another host snapshot', async () => {
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.1', port: 7860, order: 0 },
        'host-b': { id: 'host-b', name: 'Host B', ip: '100.64.0.2', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, 'host-b'],
      activeHostId: HOST_ID,
      runtime: {},
    })
    useTabStore.setState({
      tabs: {
        'tab-session': tabWithLeaf('tab-session', 'pane-session', {
          kind: 'tmux-session',
          hostId: 'host-b',
          sessionCode: 'abc123',
          mode: 'terminal',
          cachedName: 'Remote Work',
          tmuxInstance: 'main',
        }),
      },
      tabOrder: ['tab-session'],
      activeTabId: 'tab-session',
      visitHistory: [],
    })
    vi.mocked(fetchMonitorSnapshot).mockImplementation(async (hostId) => ({
      ...monitorSnapshot,
      sessions: hostId === HOST_ID ? [{
        session_code: 'abc123',
        tmux_session: { id: '$1', name: 'pdx-abc123' },
        daemon: {
          cpu_percent: 12.3,
          memory_bytes: 2048,
          process_count: 3,
          top_processes: [],
          unavailable_reason: null,
        },
      }] : [],
    }))

    render(<MemoryMonitorPage />)

    await waitFor(() => expect(fetchMonitorSnapshot).toHaveBeenCalledWith('host-b'))
    const sessionRow = await screen.findByTestId('monitor-row-tab-session-pane-session')
    expect(within(sessionRow).queryByText('CPU 12.3%')).not.toBeInTheDocument()
    expect(within(sessionRow).getAllByText('Not wired')).toHaveLength(2)
  })

  it('does not fetch a fallback snapshot for panes with missing host records', async () => {
    useTabStore.setState({
      tabs: {
        'tab-session': tabWithLeaf('tab-session', 'pane-session', {
          kind: 'tmux-session',
          hostId: 'missing-host',
          sessionCode: 'abc123',
          mode: 'terminal',
          cachedName: 'Missing Host',
          tmuxInstance: 'main',
        }),
      },
      tabOrder: ['tab-session'],
      activeTabId: 'tab-session',
      visitHistory: [],
    })

    render(<MemoryMonitorPage />)

    await waitFor(() => expect(fetchMonitorSnapshot).toHaveBeenCalledWith(HOST_ID))
    expect(fetchMonitorSnapshot).not.toHaveBeenCalledWith('missing-host')
    const sessionRow = await screen.findByTestId('monitor-row-tab-session-pane-session')
    expect(within(sessionRow).getAllByText('Not wired')).toHaveLength(2)
  })

  it('stops rendering cached daemon metrics after a pane host record is removed', async () => {
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.1', port: 7860, order: 0 },
        'host-b': { id: 'host-b', name: 'Host B', ip: '100.64.0.2', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, 'host-b'],
      activeHostId: HOST_ID,
      runtime: {},
    })
    useTabStore.setState({
      tabs: {
        'tab-session': tabWithLeaf('tab-session', 'pane-session', {
          kind: 'tmux-session',
          hostId: 'host-b',
          sessionCode: 'remote-code',
          mode: 'terminal',
          cachedName: 'Remote Work',
          tmuxInstance: 'main',
        }),
      },
      tabOrder: ['tab-session'],
      activeTabId: 'tab-session',
      visitHistory: [],
    })
    vi.mocked(fetchMonitorSnapshot).mockImplementation(async (hostId) => ({
      ...monitorSnapshot,
      sessions: hostId === 'host-b' ? [{
        session_code: 'remote-code',
        tmux_session: { id: '$2', name: 'pdx-remote' },
        daemon: {
          cpu_percent: 45.6,
          memory_bytes: 4096,
          process_count: 7,
          top_processes: [],
          unavailable_reason: null,
        },
      }] : [],
    }))

    render(<MemoryMonitorPage />)

    const sessionRow = await screen.findByTestId('monitor-row-tab-session-pane-session')
    expect(within(sessionRow).getByText('CPU 45.6%')).toBeInTheDocument()

    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.1', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      activeHostId: HOST_ID,
      runtime: {},
    })

    await waitFor(() => expect(within(sessionRow).queryByText('CPU 45.6%')).not.toBeInTheDocument())
    expect(within(sessionRow).queryByText('Memory 4 KB')).not.toBeInTheDocument()
    expect(within(sessionRow).queryByText('7 processes')).not.toBeInTheDocument()
    expect(within(sessionRow).getAllByText('Not wired').length).toBeGreaterThanOrEqual(1)
  })

  it('stops rendering cached daemon metrics after a pane host endpoint changes', async () => {
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.1', port: 7860, order: 0 },
        'host-b': { id: 'host-b', name: 'Host B', ip: '100.64.0.2', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, 'host-b'],
      activeHostId: HOST_ID,
      runtime: {},
    })
    useTabStore.setState({
      tabs: {
        'tab-session': tabWithLeaf('tab-session', 'pane-session', {
          kind: 'tmux-session',
          hostId: 'host-b',
          sessionCode: 'remote-code',
          mode: 'terminal',
          cachedName: 'Remote Work',
          tmuxInstance: 'main',
        }),
      },
      tabOrder: ['tab-session'],
      activeTabId: 'tab-session',
      visitHistory: [],
    })
    let hostBCalls = 0
    vi.mocked(fetchMonitorSnapshot).mockImplementation((hostId) => {
      if (hostId === 'host-b') {
        hostBCalls += 1
        if (hostBCalls > 1) return new Promise<MonitorSnapshot>(() => {})
        return Promise.resolve({
          ...monitorSnapshot,
          sessions: [{
            session_code: 'remote-code',
            tmux_session: { id: '$2', name: 'pdx-remote' },
            daemon: {
              cpu_percent: 45.6,
              memory_bytes: 4096,
              process_count: 7,
              top_processes: [],
              unavailable_reason: null,
            },
          }],
        })
      }
      return Promise.resolve({ ...monitorSnapshot, sessions: [] })
    })

    render(<MemoryMonitorPage />)

    const sessionRow = await screen.findByTestId('monitor-row-tab-session-pane-session')
    expect(within(sessionRow).getByText('CPU 45.6%')).toBeInTheDocument()

    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.1', port: 7860, order: 0 },
        'host-b': { id: 'host-b', name: 'Host B', ip: '100.64.0.22', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, 'host-b'],
      activeHostId: HOST_ID,
      runtime: {},
    })

    await waitFor(() => expect(within(sessionRow).queryByText('CPU 45.6%')).not.toBeInTheDocument())
    expect(within(sessionRow).queryByText('Memory 4 KB')).not.toBeInTheDocument()
    expect(within(sessionRow).queryByText('7 processes')).not.toBeInTheDocument()
    expect(within(sessionRow).getAllByText('Not wired').length).toBeGreaterThanOrEqual(1)
  })

  it('stops rendering cached active-host metrics after the active host endpoint changes', async () => {
    useTabStore.setState({
      tabs: {
        'tab-session': tabWithLeaf('tab-session', 'pane-session', {
          kind: 'tmux-session',
          hostId: HOST_ID,
          sessionCode: 'active-code',
          mode: 'terminal',
          cachedName: 'Active Work',
          tmuxInstance: 'main',
        }),
      },
      tabOrder: ['tab-session'],
      activeTabId: 'tab-session',
      visitHistory: [],
    })
    let activeCalls = 0
    vi.mocked(fetchMonitorSnapshot).mockImplementation((hostId) => {
      if (hostId === HOST_ID) {
        activeCalls += 1
        if (activeCalls > 1) return new Promise<MonitorSnapshot>(() => {})
        return Promise.resolve({
          ...monitorSnapshot,
          host: {
            ...monitorSnapshot.host,
            cpu: { percent: 12.3, unavailable_reason: null },
          },
          sessions: [{
            session_code: 'active-code',
            tmux_session: { id: '$1', name: 'pdx-active' },
            daemon: {
              cpu_percent: 45.6,
              memory_bytes: 4096,
              process_count: 7,
              top_processes: [],
              unavailable_reason: null,
            },
          }],
        })
      }
      return Promise.resolve({ ...monitorSnapshot, sessions: [] })
    })

    render(<MemoryMonitorPage />)

    const sessionRow = await screen.findByTestId('monitor-row-tab-session-pane-session')
    expect(within(sessionRow).getByText('CPU 45.6%')).toBeInTheDocument()
    expect(screen.getByText('12.3%')).toBeInTheDocument()

    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'Host A', ip: '100.64.0.22', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      activeHostId: HOST_ID,
      runtime: {},
    })

    await waitFor(() => expect(within(sessionRow).queryByText('CPU 45.6%')).not.toBeInTheDocument())
    expect(screen.queryByText('12.3%')).not.toBeInTheDocument()
    expect(within(sessionRow).getAllByText('Not wired')).toHaveLength(2)
  })

  it('keeps daemon metrics unwired when the active-host session is absent from the snapshot', async () => {
    useTabStore.setState({
      tabs: {
        'tab-session': tabWithLeaf('tab-session', 'pane-session', {
          kind: 'tmux-session',
          hostId: HOST_ID,
          sessionCode: 'missing',
          mode: 'terminal',
          cachedName: 'Missing',
          tmuxInstance: 'main',
        }),
      },
      tabOrder: ['tab-session'],
      activeTabId: 'tab-session',
      visitHistory: [],
    })
    vi.mocked(fetchMonitorSnapshot).mockResolvedValue({
      ...monitorSnapshot,
      sessions: [],
    })

    render(<MemoryMonitorPage />)

    const sessionRow = await screen.findByTestId('monitor-row-tab-session-pane-session')
    expect(within(sessionRow).getAllByText('Not wired')).toHaveLength(2)
  })

  it('uses active workspace tab order when workspace order differs from global tab order', async () => {
    useTabStore.setState({
      tabs: {
        'tab-a': tabWithLeaf('tab-a', 'pane-a', { kind: 'dashboard' }),
        'tab-b': tabWithLeaf('tab-b', 'pane-b', { kind: 'history' }),
      },
      tabOrder: ['tab-a', 'tab-b'],
      activeTabId: 'tab-b',
      visitHistory: [],
    })
    useWorkspaceStore.setState({
      activeWorkspaceId: 'workspace-1',
      workspaces: [{
        id: 'workspace-1',
        name: 'Workspace',
        tabs: ['tab-b', 'tab-a'],
        activeTabId: 'tab-b',
        moduleConfig: {},
      }],
    })

    render(<MemoryMonitorPage />)

    await screen.findByText('Open Panes')
    expect(screen.getAllByTestId(/^monitor-row-/).map((row) => row.getAttribute('data-testid'))).toEqual([
      'monitor-row-tab-b-pane-b',
      'monitor-row-tab-a-pane-a',
    ])
  })

  it('shows an empty open panes state when there are no tabs', async () => {
    render(<MemoryMonitorPage />)

    expect(await screen.findByText('No open panes')).toBeInTheDocument()
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

function tabWithLeaf(id: string, paneId: string, content: PaneContent): Tab {
  return {
    id,
    pinned: false,
    locked: false,
    createdAt: 1,
    layout: { type: 'leaf', pane: { id: paneId, content } },
  }
}
