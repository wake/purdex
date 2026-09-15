import { StrictMode } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
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

function configCallCount() {
  return mockHostFetch.mock.calls.filter((c) => c[1] === '/api/config').length
}

// Two `useHostStore.setState` calls fired back-to-back with no yield between
// them (as a bare `disconnected` then `connected` write would be) can land
// in the same React batch, so the component only ever observes the final
// value and never renders the intermediate transition — silently defeating
// the reconnect detector. `act()` forces a synchronous flush after each
// write so every transition is actually rendered, matching how a real
// WebSocket status change (one event at a time) drives this in production.
function setRuntimeStatus(status: 'connected' | 'disconnected' | 'reconnecting' | 'auth-error') {
  act(() => {
    useHostStore.setState({ runtime: { [HOST_ID]: { status } } })
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
  it('renders all three cards and fetches info + config once each once both have loaded', async () => {
    render(<NexHostSection hostId={HOST_ID} />)

    expect(await screen.findByText('Engine')).toBeInTheDocument()
    expect(screen.getByText('Configuration')).toBeInTheDocument()
    expect(screen.getByTestId('executions-stub')).toHaveAttribute('data-host', HOST_ID)
    expect(screen.getByTestId('executions-stub')).toHaveAttribute('data-enabled', 'true')

    expect(mockFetchInfo).toHaveBeenCalledTimes(1)
    expect(mockFetchInfo).toHaveBeenCalledWith(HOST_ID)
    expect(configCallCount()).toBe(1)
  })

  it('shows a loading line before /api/info and /api/config resolve, with no misleading badge', () => {
    // Both requests hang — never resolve during this test.
    mockFetchInfo.mockImplementation(() => new Promise(() => {}))
    mockHostFetch.mockImplementation(() => new Promise(() => {}))

    render(<NexHostSection hostId={HOST_ID} />)

    expect(screen.getByText('Loading...')).toBeInTheDocument()
    expect(screen.queryByTestId('nex-status-badge')).not.toBeInTheDocument()
    expect(screen.queryByText(/disabled/i)).not.toBeInTheDocument()
    expect(screen.queryByText('Configuration')).not.toBeInTheDocument()
    expect(screen.queryByTestId('executions-stub')).not.toBeInTheDocument()
  })

  it('shows hosts.load_failed (never the form) when /api/config rejects even though /api/info resolves', async () => {
    mockFetchInfo.mockImplementation(() => Promise.resolve(infoResponse(readyInfo)))
    mockHostFetch.mockImplementation((_hostId, path) => {
      if (path === '/api/config') return Promise.reject(new Error('config unreachable'))
      return Promise.resolve({ ok: true, json: () => Promise.resolve({}) } as Response)
    })

    render(<NexHostSection hostId={HOST_ID} />)

    await waitFor(() => expect(screen.getByText('Failed to load')).toBeInTheDocument())
    // No form (and therefore no way to trigger a destructive PUT) and no
    // other cards either — a failed config load hides all three.
    expect(screen.queryByText('Configuration')).not.toBeInTheDocument()
    expect(screen.queryByText('Engine')).not.toBeInTheDocument()
    expect(screen.queryByTestId('executions-stub')).not.toBeInTheDocument()
    expect(mockHostFetch.mock.calls.some((c) => (c[2] as RequestInit | undefined)?.method === 'PUT')).toBe(false)
  })

  it('a non-OK /api/config response is also treated as a load failure (not silently swallowed)', async () => {
    mockFetchInfo.mockImplementation(() => Promise.resolve(infoResponse(readyInfo)))
    mockHostFetch.mockImplementation((_hostId, path) => {
      if (path === '/api/config') return Promise.resolve({ ok: false, status: 500, json: () => Promise.resolve({}) } as Response)
      return Promise.resolve({ ok: true, json: () => Promise.resolve({}) } as Response)
    })

    render(<NexHostSection hostId={HOST_ID} />)

    await waitFor(() => expect(screen.getByText('Failed to load')).toBeInTheDocument())
    expect(screen.queryByText('Configuration')).not.toBeInTheDocument()
  })

  it('Refresh on the status card refetches /api/info but not /api/config again', async () => {
    render(<NexHostSection hostId={HOST_ID} />)
    await screen.findByText('Engine')
    await waitFor(() => expect(mockFetchInfo).toHaveBeenCalledTimes(1))
    const configCallsBefore = configCallCount()

    fireEvent.click(screen.getByText('Refresh'))

    await waitFor(() => expect(mockFetchInfo).toHaveBeenCalledTimes(2))
    expect(configCallCount()).toBe(configCallsBefore)
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

  // Controller ruling I + fix-round-1 item 2, exercised together: offline
  // always hides the cards — even ones that had already loaded — and a
  // reconnect reloads both endpoints and remounts the status card (proving
  // ruling G: the card refetches its own Nexen host/capabilities data a
  // second time, not just re-rendering with the same badge state).
  it('offline hides previously-loaded cards; reconnecting reloads /api/info + /api/config and refetches the status card', async () => {
    // 1) Start offline with nothing ever loaded.
    useHostStore.setState({ runtime: { [HOST_ID]: { status: 'disconnected' } } })
    mockFetchInfo.mockRejectedValue(new Error('offline'))
    mockHostFetch.mockRejectedValue(new Error('offline'))

    render(<NexHostSection hostId={HOST_ID} />)
    expect(screen.getByText('Failed to load')).toBeInTheDocument()
    expect(nexApi.fetchNexHost).not.toHaveBeenCalled()

    // 2) Reconnect — first successful load. The status card sees `ready`
    // for the first time and fetches Nexen host/capabilities once.
    mockSuccess()
    setRuntimeStatus('connected')

    await screen.findByText('Engine')
    await screen.findByDisplayValue('/a')
    await waitFor(() => expect(nexApi.fetchNexHost).toHaveBeenCalledTimes(1))
    const infoCallsAfterFirstConnect = mockFetchInfo.mock.calls.length
    const configCallsAfterFirstConnect = configCallCount()

    // 3) Disconnect again — even though data is already loaded, the cards
    // must disappear (ruling I: no stale cards while offline).
    setRuntimeStatus('disconnected')
    await waitFor(() => expect(screen.getByText('Failed to load')).toBeInTheDocument())
    expect(screen.queryByText('Engine')).not.toBeInTheDocument()
    // Going offline alone must not itself trigger a new fetch attempt.
    expect(mockFetchInfo.mock.calls.length).toBe(infoCallsAfterFirstConnect)
    expect(configCallCount()).toBe(configCallsAfterFirstConnect)

    // 4) Reconnect a second time — proves ruling G end-to-end: /api/info and
    // /api/config are refetched, and NexEngineStatus (remounted via
    // key={generation}) fetches Nexen host/capabilities a *second* time
    // rather than reusing its already-fetched state from step 2.
    setRuntimeStatus('connected')

    await waitFor(() => expect(mockFetchInfo.mock.calls.length).toBe(infoCallsAfterFirstConnect + 1))
    await waitFor(() => expect(configCallCount()).toBe(configCallsAfterFirstConnect + 1))
    await screen.findByText('Engine')
    await waitFor(() => expect(nexApi.fetchNexHost).toHaveBeenCalledTimes(2))
  })

  it('under StrictMode, a reconnect triggers exactly one extra /api/info + /api/config fetch pair', async () => {
    render(
      <StrictMode>
        <NexHostSection hostId={HOST_ID} />
      </StrictMode>,
    )
    await screen.findByText('Engine')
    await screen.findByDisplayValue('/a')

    const infoCallsBefore = mockFetchInfo.mock.calls.length
    const configCallsBefore = configCallCount()

    setRuntimeStatus('disconnected')
    setRuntimeStatus('connected')

    await waitFor(() => expect(mockFetchInfo.mock.calls.length).toBe(infoCallsBefore + 1))
    await waitFor(() => expect(configCallCount()).toBe(configCallsBefore + 1))
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
