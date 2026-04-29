import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import { MemoryMonitorPage } from './MemoryMonitorPage'
import { useWorkspaceStore } from '../features/workspace/store'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import {
  fetchMonitorConfig,
  fetchMonitorSnapshot,
  type MonitorConfig,
  type MonitorSnapshot,
} from '../lib/host-api'
import type { PaneContent, Tab } from '../types/tab'

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
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
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
