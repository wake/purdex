import { StrictMode } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { NexHostSection } from './NexHostSection'
import { useHostStore } from '../../../stores/useHostStore'
import type { ConfigData, NexConfig, NexInfo } from '../../../lib/host-api'

vi.mock('../../../lib/host-api', async (importOriginal) => {
  const actual = await importOriginal() as Record<string, unknown>
  return { ...actual, hostFetch: vi.fn(), fetchInfo: vi.fn() }
})

vi.mock('../../../lib/nex/nex-api', () => ({
  fetchNexHost: vi.fn(),
  fetchNexCapabilities: vi.fn(),
}))

// NexExecutionsTable (Task 6) is being fixed concurrently on its own files;
// stub it here so this suite only exercises NexHostSection's own
// orchestration (fetch/refresh/reconnect) and does not race that work.
vi.mock('./NexExecutionsTable', () => ({
  default: (props: { hostId: string; enabled: boolean }) => (
    <div data-testid="executions-stub" data-host={props.hostId} data-enabled={String(props.enabled)} />
  ),
}))

import { hostFetch, fetchInfo } from '../../../lib/host-api'
import * as nexApi from '../../../lib/nex/nex-api'

const mockHostFetch = vi.mocked(hostFetch)
const mockFetchInfo = vi.mocked(fetchInfo)
const HOST_ID = 'test-host'

const readyInfo: NexInfo = {
  configured: true,
  mounted: true,
  ready: true,
  init_error: '',
  effective: {
    data_dir: '/d/nex',
    claude_bin: '',
    cswap_bin: '',
    max_profile: 'standard',
    default_profile: 'standard',
    repo_roots: ['/a'],
    service_roots: [],
    path_prefix: '',
    lease_ttl: '2m0s',
    interrupt: '10s',
    turn: '5m0s',
  },
}

const savedConfig: NexConfig = {
  enabled: true,
  repo_roots: ['/a'],
  service_roots: [],
  claude_bin: '',
  cswap_bin: '',
  path_prepend: [],
  sandbox: { max_profile: 'standard', default_profile: 'standard' },
  timeouts: { lease_ttl: '', interrupt: '', turn: '' },
}

function infoResponse(nex: NexInfo | null): Response {
  return {
    ok: true,
    json: () => Promise.resolve({
      host_id: 'h', tmux_instance: '', purdex_version: '', tmux_version: '', os: '', arch: '', nex,
    }),
  } as Response
}

function configResponse(nex: NexConfig | undefined): Response {
  const body: ConfigData = { bind: '', port: 0, stream: { presets: [] }, detect: { cc_commands: [], poll_interval: 0 }, nex }
  return { ok: true, json: () => Promise.resolve(body) } as Response
}

function mockSuccess() {
  mockFetchInfo.mockImplementation(() => Promise.resolve(infoResponse(readyInfo)))
  mockHostFetch.mockImplementation((_hostId, path) => {
    if (path === '/api/config') return Promise.resolve(configResponse(savedConfig))
    return Promise.resolve({ ok: true, json: () => Promise.resolve({}) } as Response)
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'TestHost', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } },
  })
  vi.mocked(nexApi.fetchNexHost).mockResolvedValue({ active_account: '', quota: null })
  vi.mocked(nexApi.fetchNexCapabilities).mockResolvedValue({
    phase: 'P1a', host_id: 'h', verbs: [], providers: [], events: [], provider_events: [], transient_events: [],
    sandbox_profiles: [], sandbox_default_profile: '', sandbox_max_profile: '', roots: [],
    lease: { ttl_seconds: 0, scope: '', renew: { method: '', path: '' }, release: { method: '', path: '' } },
    send: { delivery: [], max_text_bytes: 0 },
  })
  mockSuccess()
})

afterEach(() => {
  useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {} })
})

describe('NexHostSection', () => {
  it('renders all three cards and fetches info + config once each on mount', async () => {
    render(<NexHostSection hostId={HOST_ID} />)

    expect(await screen.findByText('Engine')).toBeInTheDocument()
    expect(screen.getByText('Configuration')).toBeInTheDocument()
    expect(screen.getByTestId('executions-stub')).toHaveAttribute('data-host', HOST_ID)
    expect(screen.getByTestId('executions-stub')).toHaveAttribute('data-enabled', 'true')

    expect(mockFetchInfo).toHaveBeenCalledTimes(1)
    expect(mockFetchInfo).toHaveBeenCalledWith(HOST_ID)
    expect(mockHostFetch.mock.calls.filter((c) => c[1] === '/api/config')).toHaveLength(1)
  })

  it('shows the offline message instead of the three cards when there is no data yet', async () => {
    useHostStore.setState({ runtime: { [HOST_ID]: { status: 'disconnected' } } })
    mockFetchInfo.mockRejectedValue(new Error('offline'))
    mockHostFetch.mockRejectedValue(new Error('offline'))

    render(<NexHostSection hostId={HOST_ID} />)

    await waitFor(() => expect(mockFetchInfo).toHaveBeenCalledTimes(1))
    expect(screen.getByText('Failed to load')).toBeInTheDocument()
    expect(screen.queryByText('Engine')).not.toBeInTheDocument()
    expect(screen.queryByTestId('executions-stub')).not.toBeInTheDocument()
  })

  it('Refresh on the status card refetches /api/info but not /api/config again', async () => {
    render(<NexHostSection hostId={HOST_ID} />)
    await screen.findByText('Engine')
    await waitFor(() => expect(mockFetchInfo).toHaveBeenCalledTimes(1))
    const configCallsBefore = mockHostFetch.mock.calls.filter((c) => c[1] === '/api/config').length

    fireEvent.click(screen.getByText('Refresh'))

    await waitFor(() => expect(mockFetchInfo).toHaveBeenCalledTimes(2))
    expect(mockHostFetch.mock.calls.filter((c) => c[1] === '/api/config').length).toBe(configCallsBefore)
  })

  it('saving through the config form persists and updates the restart-required notice', async () => {
    render(<NexHostSection hostId={HOST_ID} />)
    // Wait for the fetched config (not just the empty-draft default) to land
    // before asserting on restartRequired() — its repo-roots input shows the
    // fetched value once `config` has arrived.
    await screen.findByDisplayValue('/a')
    // savedConfig matches readyInfo.effective exactly — no restart notice yet.
    expect(screen.queryByTestId('nex-restart-required')).not.toBeInTheDocument()

    const changed: NexConfig = { ...savedConfig, sandbox: { max_profile: 'standard', default_profile: 'readonly' } }
    mockHostFetch.mockImplementationOnce((_hostId, path) => {
      expect(path).toBe('/api/config')
      return Promise.resolve(configResponse(changed))
    })

    fireEvent.change(screen.getByLabelText(/default profile/i), { target: { value: 'readonly' } })
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(screen.getByTestId('nex-restart-required')).toBeInTheDocument())
  })

  it('on a disconnected→connected transition, refetches info/config and remounts the status card', async () => {
    useHostStore.setState({ runtime: { [HOST_ID]: { status: 'disconnected' } } })
    mockFetchInfo.mockRejectedValueOnce(new Error('offline'))
    mockHostFetch.mockRejectedValue(new Error('offline'))

    render(<NexHostSection hostId={HOST_ID} />)
    await waitFor(() => expect(mockFetchInfo).toHaveBeenCalledTimes(1))
    expect(screen.getByText('Failed to load')).toBeInTheDocument()
    expect(nexApi.fetchNexHost).not.toHaveBeenCalled()

    mockSuccess()
    useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected' } } })

    await waitFor(() => expect(mockFetchInfo).toHaveBeenCalledTimes(2))
    // One failed attempt from the initial (disconnected) mount, one
    // successful attempt from the reconnect refetch.
    expect(mockHostFetch.mock.calls.filter((c) => c[1] === '/api/config')).toHaveLength(2)
    await screen.findByText('Engine')
    await screen.findByDisplayValue('/a')
    // The card only fetches Nexen host/capabilities once it sees `ready`
    // data — the remount via key={generation} is what makes it fetch again
    // rather than reusing the badge state from the failed initial mount.
    await waitFor(() => expect(nexApi.fetchNexHost).toHaveBeenCalledTimes(1))
  })

  it('loads data correctly under StrictMode (mount→cleanup→mount safe)', async () => {
    render(
      <StrictMode>
        <NexHostSection hostId={HOST_ID} />
      </StrictMode>,
    )

    expect(await screen.findByText('Engine')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('executions-stub')).toHaveAttribute('data-enabled', 'true'))
    expect(screen.getByText('Configuration')).toBeInTheDocument()
  })
})
