import { StrictMode } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { NexHostSection } from './NexHostSection'
import { useHostStore } from '../../../stores/useHostStore'
import { startNexHostInvalidation, useNexHostStore } from '../../../stores/useNexHostStore'
import type { ConfigData, NexConfig, NexInfo } from '../../../lib/host-api'

vi.mock('../../../lib/host-api', async (importOriginal) => {
  const actual = await importOriginal() as Record<string, unknown>
  return { ...actual, hostFetch: vi.fn(), fetchInfo: vi.fn() }
})

vi.mock('../../../lib/nex/nex-api', () => ({
  fetchNexHost: vi.fn(),
  fetchNexCapabilities: vi.fn(),
}))

// NexExecutionsTable has its own suite; stub it here so this one only
// exercises NexHostSection's orchestration (fetch/refresh/reconnect).
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
  const body: ConfigData = { bind: '', port: 0, detect: { cc_commands: [], poll_interval: 0 }, nex }
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

// `/api/info` now reaches the page through useNexHostStore (spec §4.1): the
// `fetchInfo` mock feeds the store's fetch, and the reconnect refetch is the
// store watcher's — started here as main.tsx does.
let stopNexHostInvalidation: () => void

beforeEach(() => {
  vi.clearAllMocks()
  for (const id of ['test-host', 'host-b']) useNexHostStore.getState().clearHost(id)
  stopNexHostInvalidation = startNexHostInvalidation()
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
  stopNexHostInvalidation()
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

  it('enables the executions table for an older daemon whose info has no ready field', async () => {
    const legacy = { configured: true, mounted: true, init_error: '', effective: null } as unknown as NexInfo
    mockFetchInfo.mockImplementation(() => Promise.resolve(infoResponse(legacy)))
    render(<NexHostSection hostId={HOST_ID} />)
    await screen.findByText('Engine')
    expect(screen.getByTestId('nex-status-badge')).toHaveTextContent(/ready/i)
    expect(screen.getByTestId('executions-stub')).toHaveAttribute('data-enabled', 'true')
  })

  describe('responses that belong to a previous host', () => {
    const HOST_B = 'host-b'

    function deferredInfo() {
      let resolve!: (r: Response) => void
      const promise = new Promise<Response>((r) => { resolve = r })
      return { promise, resolve }
    }

    beforeEach(() => {
      useHostStore.setState({
        hosts: {
          [HOST_ID]: { id: HOST_ID, name: 'TestHost', ip: '1.2.3.4', port: 7860, order: 0 },
          [HOST_B]: { id: HOST_B, name: 'HostB', ip: '1.2.3.5', port: 7860, order: 1 },
        },
        hostOrder: [HOST_ID, HOST_B],
        runtime: { [HOST_ID]: { status: 'connected' }, [HOST_B]: { status: 'connected' } },
      })
    })

    const staleInfo: NexInfo = { ...readyInfo, ready: false, init_error: 'stale host A', restart_required: true }

    it('a Refresh for host A that resolves after switching to host B is ignored', async () => {
      const { rerender } = render(<NexHostSection hostId={HOST_ID} />)
      await screen.findByDisplayValue('/a')

      const pending = deferredInfo()
      mockFetchInfo.mockImplementationOnce(() => pending.promise)
      fireEvent.click(screen.getByText('Refresh'))
      await waitFor(() => expect(mockFetchInfo).toHaveBeenCalledTimes(2))

      rerender(<NexHostSection hostId={HOST_B} />)
      await waitFor(() => expect(mockFetchInfo).toHaveBeenCalledWith(HOST_B))
      await screen.findByText('Engine')

      await act(async () => {
        pending.resolve(infoResponse(staleInfo))
        await pending.promise
      })

      expect(screen.queryByTestId('nex-restart-required')).not.toBeInTheDocument()
      expect(screen.queryByText('stale host A')).not.toBeInTheDocument()
      expect(screen.getByTestId('nex-status-badge')).toHaveTextContent(/ready/i)
      expect(screen.getByTestId('executions-stub')).toHaveAttribute('data-host', HOST_B)
      expect(screen.getByTestId('executions-stub')).toHaveAttribute('data-enabled', 'true')
    })

    it('the /api/info refetch after a save on host A that resolves after switching to host B is ignored', async () => {
      const { rerender } = render(<NexHostSection hostId={HOST_ID} />)
      await screen.findByDisplayValue('/a')

      const pending = deferredInfo()
      mockFetchInfo.mockImplementationOnce(() => pending.promise)
      fireEvent.click(screen.getByRole('button', { name: /^save$/i }))
      await waitFor(() => expect(mockFetchInfo).toHaveBeenCalledTimes(2))

      rerender(<NexHostSection hostId={HOST_B} />)
      await waitFor(() => expect(mockFetchInfo).toHaveBeenCalledWith(HOST_B))
      await screen.findByText('Engine')

      await act(async () => {
        pending.resolve(infoResponse(staleInfo))
        await pending.promise
      })

      expect(screen.queryByTestId('nex-restart-required')).not.toBeInTheDocument()
      expect(screen.getByTestId('nex-status-badge')).toHaveTextContent(/ready/i)
      expect(screen.getByTestId('executions-stub')).toHaveAttribute('data-enabled', 'true')
    })
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

  // A failed Refresh must not collapse the whole section — only an
  // initial-load failure does that.
  it('a failed Refresh keeps the cards and shows an inline error; a later successful Refresh clears it', async () => {
    render(<NexHostSection hostId={HOST_ID} />)
    await screen.findByText('Engine')

    mockFetchInfo.mockRejectedValueOnce(new Error('blip'))
    fireEvent.click(screen.getByText('Refresh'))

    await waitFor(() => expect(screen.getByTestId('nex-refresh-error')).toBeInTheDocument())
    // The cards themselves must still be there — this is not the
    // initial-load failure gate.
    expect(screen.getByText('Engine')).toBeInTheDocument()
    expect(screen.getByText('Configuration')).toBeInTheDocument()
    expect(screen.getByTestId('executions-stub')).toBeInTheDocument()
    expect(screen.queryByTestId('nex-retry')).not.toBeInTheDocument()

    // mockSuccess() (still in effect for the next call) resolves normally.
    fireEvent.click(screen.getByText('Refresh'))
    await waitFor(() => expect(screen.queryByTestId('nex-refresh-error')).not.toBeInTheDocument())
    expect(screen.getByText('Engine')).toBeInTheDocument()
  })

  // The initial-load failure gate gets a way back.
  it('Retry after an initial /api/config failure reloads and renders the cards', async () => {
    mockFetchInfo.mockImplementation(() => Promise.resolve(infoResponse(readyInfo)))
    mockHostFetch.mockImplementation((_hostId, path) => {
      if (path === '/api/config') return Promise.reject(new Error('config unreachable'))
      return Promise.resolve({ ok: true, json: () => Promise.resolve({}) } as Response)
    })

    render(<NexHostSection hostId={HOST_ID} />)
    await waitFor(() => expect(screen.getByTestId('nex-retry')).toBeInTheDocument())
    expect(screen.queryByText('Engine')).not.toBeInTheDocument()

    const infoCallsBeforeRetry = mockFetchInfo.mock.calls.length
    const configCallsBeforeRetry = configCallCount()
    mockSuccess()

    fireEvent.click(screen.getByTestId('nex-retry'))

    await screen.findByText('Engine')
    await screen.findByDisplayValue('/a')
    expect(mockFetchInfo.mock.calls.length).toBe(infoCallsBeforeRetry + 1)
    expect(configCallCount()).toBe(configCallsBeforeRetry + 1)
    // The status card had never mounted before Retry succeeded (the failed
    // gate rendered a different subtree entirely), so this is its first
    // Nexen host/capabilities fetch, not a `key`-forced *re*fetch.
    await waitFor(() => expect(nexApi.fetchNexHost).toHaveBeenCalledTimes(1))
  })

  it('saving through the config form persists and updates the restart-required notice', async () => {
    render(<NexHostSection hostId={HOST_ID} />)
    // Wait for the fetched config (not just the empty-draft default) to land
    // before asserting on restartRequired() — its repo-roots input shows the
    // fetched value once `config` has arrived.
    await screen.findByDisplayValue('/a')
    // readyInfo carries no restart_required — no notice yet.
    expect(screen.queryByTestId('nex-restart-required')).not.toBeInTheDocument()

    const changed: NexConfig = { ...savedConfig, sandbox: { max_profile: 'standard', default_profile: 'readonly' } }
    // The daemon now reports the saved section differs from its boot snapshot.
    mockFetchInfo.mockImplementation(() => Promise.resolve(infoResponse({ ...readyInfo, restart_required: true })))
    const infoCallsBeforeSave = mockFetchInfo.mock.calls.length
    mockHostFetch.mockImplementationOnce((_hostId, path) => {
      expect(path).toBe('/api/config')
      return Promise.resolve(configResponse(changed))
    })

    fireEvent.change(screen.getByLabelText(/default profile/i), { target: { value: 'readonly' } })
    fireEvent.click(screen.getByRole('button', { name: /^save$/i }))

    await waitFor(() => expect(screen.getByTestId('nex-restart-required')).toBeInTheDocument())
    expect(mockFetchInfo.mock.calls.length).toBe(infoCallsBeforeSave + 1)
    // The refetch keeps the cards mounted (no loading gate) and "Saved" stays.
    expect(screen.getByText(/^saved/i)).toBeInTheDocument()
  })

  it('renders the form when /api/config carries null nex lists and no sandbox (older or unset config)', async () => {
    mockHostFetch.mockImplementation((_hostId, path) => {
      if (path !== '/api/config') return Promise.resolve({ ok: true, json: () => Promise.resolve({}) } as Response)
      const nex = { enabled: false, repo_roots: null, service_roots: null, path_prepend: null, claude_bin: '', timeouts: { lease_ttl: '', interrupt: '', turn: '' } }
      return Promise.resolve({ ok: true, json: () => Promise.resolve({ bind: '', port: 0, detect: { cc_commands: [], poll_interval: 0 }, nex }) } as Response)
    })
    render(<NexHostSection hostId={HOST_ID} />)
    await screen.findByRole('button', { name: /^save$/i })
    expect((screen.getByLabelText(/max profile/i) as HTMLSelectElement).value).toBe('')
  })

  it('renders an empty form when /api/config has no nex key at all', async () => {
    mockHostFetch.mockImplementation((_hostId, path) => {
      if (path !== '/api/config') return Promise.resolve({ ok: true, json: () => Promise.resolve({}) } as Response)
      return Promise.resolve({ ok: true, json: () => Promise.resolve({ bind: '', port: 0, detect: { cc_commands: [], poll_interval: 0 } }) } as Response)
    })
    render(<NexHostSection hostId={HOST_ID} />)
    await screen.findByRole('button', { name: /^save$/i })
    expect((screen.getByLabelText(/enabled/i) as HTMLInputElement).checked).toBe(false)
  })

  // Exercised end-to-end with a real reconnect: offline always hides the
  // cards — even ones that had already loaded — and going back online
  // reloads both endpoints and re-renders them. The status card fetching
  // Nexen host/capabilities data a second time is a fresh
  // `<NexEngineStatus>` mounting for the first time since step 3 tore down
  // the whole card subtree (the offline branch renders a completely
  // different element, not the cards with different props).
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
    // must disappear (no stale cards while offline).
    setRuntimeStatus('disconnected')
    await waitFor(() => expect(screen.getByText('Failed to load')).toBeInTheDocument())
    expect(screen.queryByText('Engine')).not.toBeInTheDocument()
    // Going offline alone must not itself trigger a new fetch attempt.
    expect(mockFetchInfo.mock.calls.length).toBe(infoCallsAfterFirstConnect)
    expect(configCallCount()).toBe(configCallsAfterFirstConnect)

    // 4) Reconnect a second time: /api/info and /api/config are refetched,
    // and a freshly-mounted <NexEngineStatus> (step 3 unmounted the old one
    // when it hid the cards) fetches Nexen host/capabilities again rather
    // than there being any stale badge state left over from step 2.
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
