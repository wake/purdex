import { describe, it, expect, vi, beforeEach, afterEach, type Mock } from 'vitest'
import { renderHook, waitFor, act } from '@testing-library/react'
import { useNexHostData } from './useNexHostData'
import { useHostStore } from '../../../stores/useHostStore'
import { useNexHostStore, type NexHostEntry } from '../../../stores/useNexHostStore'
import type { NexConfig, NexInfo } from '../../../lib/host-api'
import type { NexCapabilities } from '../../../lib/nex/types'

vi.mock('../../../lib/host-api', async (importOriginal) => {
  const actual = await importOriginal() as Record<string, unknown>
  return { ...actual, hostFetch: vi.fn(), fetchInfo: vi.fn() }
})

import { hostFetch, fetchInfo } from '../../../lib/host-api'

const mockHostFetch = vi.mocked(hostFetch)
const mockFetchInfo = vi.mocked(fetchInfo)
const HOST_ID = 'h1'

const info = (over: Partial<NexInfo> = {}): NexInfo => ({ configured: true, mounted: true, ready: true, init_error: '', effective: null, ...over })

const caps: NexCapabilities = {
  phase: 'P1a', host_id: 'h', verbs: [], providers: [], events: [], provider_events: [], transient_events: [],
  sandbox_profiles: [], sandbox_default_profile: '', sandbox_max_profile: '', roots: [],
  lease: { ttl_seconds: 0, scope: '', renew: { method: '', path: '' }, release: { method: '', path: '' } },
  send: { delivery: [], max_text_bytes: 0 },
}

/** A settled store entry, as `ensure` would have committed it. */
function entry(over: Partial<NexHostEntry> = {}): NexHostEntry {
  return { info: info(), capabilities: caps, phase: 'ready', error: null, fetchedAt: Date.now(), generation: 1, fingerprint: '', ...over }
}

function seed(e: NexHostEntry | undefined, hostId = HOST_ID) {
  act(() => {
    useNexHostStore.setState((s) => {
      const byHost = { ...s.byHost }
      if (e) byHost[hostId] = e
      else delete byHost[hostId]
      return { byHost }
    })
  })
}

function configResponse(nex: NexConfig | { enabled: boolean } | undefined): Response {
  return { ok: true, json: () => Promise.resolve({ nex }) } as Response
}

const configCalls = () => mockHostFetch.mock.calls.filter((c) => c[1] === '/api/config').length

let ensureSpy: Mock<(hostId: string) => Promise<void>>
let invalidateSpy: Mock<(hostId: string) => Promise<void>>

beforeEach(() => {
  vi.clearAllMocks()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'H', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } },
  })
  // The store's own fetching is covered by useNexHostStore.test.ts; here it
  // is stubbed so every `fetchInfo` call would be the hook's — and there
  // must be none.
  ensureSpy = vi.fn<(hostId: string) => Promise<void>>().mockResolvedValue(undefined)
  invalidateSpy = vi.fn<(hostId: string) => Promise<void>>().mockResolvedValue(undefined)
  useNexHostStore.setState({ byHost: {}, ensure: ensureSpy, invalidate: invalidateSpy })
  mockHostFetch.mockImplementation(() => Promise.resolve(configResponse({ enabled: true })))
})

afterEach(() => {
  useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {} })
})

describe('useNexHostData', () => {
  it('reads info from the store, loads a normalised config, and never fetches /api/info itself', async () => {
    seed(entry())
    const { result } = renderHook(() => useNexHostData(HOST_ID))
    expect(result.current.phase).toBe('loading')
    await waitFor(() => expect(result.current.phase).toBe('ready'))
    expect(result.current.info?.ready).toBe(true)
    expect(result.current.config?.repo_roots).toEqual([])
    expect(mockFetchInfo).not.toHaveBeenCalled()
    expect(ensureSpy).toHaveBeenCalledWith(HOST_ID)
  })

  it('reports offline (and nothing else) while the host is not connected', () => {
    seed(entry())
    useHostStore.setState({ runtime: { [HOST_ID]: { status: 'disconnected' } } })
    const { result } = renderHook(() => useNexHostData(HOST_ID))
    expect(result.current.phase).toBe('offline')
  })

  it('stays loading until the store has info for the host, then re-renders on the store change', async () => {
    const { result } = renderHook(() => useNexHostData(HOST_ID))
    await waitFor(() => expect(configCalls()).toBe(1))
    expect(result.current.phase).toBe('loading')
    expect(result.current.info).toBeNull()

    seed(entry({ info: info({ init_error: 'from store' }) }))
    await waitFor(() => expect(result.current.phase).toBe('ready'))
    expect(result.current.info?.init_error).toBe('from store')
  })

  it('a config load failure yields failed even though the store has ready info', async () => {
    seed(entry())
    mockHostFetch.mockImplementation(() => Promise.reject(new Error('config unreachable')))
    const { result } = renderHook(() => useNexHostData(HOST_ID))
    await waitFor(() => expect(result.current.phase).toBe('failed'))
  })


  it('capabilities 503 (store unavailable) with info.ready true keeps the page ready', async () => {
    seed(entry({ capabilities: null, phase: 'unavailable', error: 'engine down' }))
    const { result } = renderHook(() => useNexHostData(HOST_ID))
    await waitFor(() => expect(result.current.phase).toBe('ready'))
    expect(result.current.info?.ready).toBe(true)
    expect(result.current.refreshError).toBe(false)
  })

  it('onConfigSaved applies the saved config and invalidates the host', async () => {
    seed(entry())
    const { result } = renderHook(() => useNexHostData(HOST_ID))
    await waitFor(() => expect(result.current.phase).toBe('ready'))
    const before = configCalls()

    act(() => result.current.onConfigSaved({
      bind: '', port: 0, detect: { cc_commands: [], poll_interval: 0 },
      nex: { enabled: false, repo_roots: ['/x'], service_roots: [], claude_bin: '', path_prepend: [], sandbox: { max_profile: '', default_profile: '' }, timeouts: { lease_ttl: '', interrupt: '', turn: '' } },
    }))

    expect(invalidateSpy).toHaveBeenCalledWith(HOST_ID)
    expect(result.current.config?.repo_roots).toEqual(['/x'])
    expect(result.current.phase).toBe('ready')
    expect(configCalls()).toBe(before)
  })

  it('refresh invalidates the host without refetching /api/config', async () => {
    seed(entry())
    const { result } = renderHook(() => useNexHostData(HOST_ID))
    await waitFor(() => expect(result.current.phase).toBe('ready'))
    const before = configCalls()
    act(() => result.current.refresh())
    expect(invalidateSpy).toHaveBeenCalledWith(HOST_ID)
    expect(configCalls()).toBe(before)
  })

  // Pre-migration contract (origin/main useNexHostData.ts, before the store):
  // `refresh()` ended in `.catch(() => { setRefreshError(true) })` and its
  // doc comment read "A failure sets `refreshError` and leaves the loaded
  // data and `infoStatus` alone" — so a failed manual Refresh kept `info`
  // and the page `ready`, while a failed *first* load (`infoStatus ===
  // 'failed'`) was a page-level failure. The store-backed hook keeps both.
  describe('refresh-failure semantics (pinned pre-migration contract)', () => {
    it('a failed refresh keeps the cards on the last info with refreshError and the page ready; the next success clears it', async () => {
      seed(entry())
      const { result } = renderHook(() => useNexHostData(HOST_ID))
      await waitFor(() => expect(result.current.phase).toBe('ready'))

      // What the store commits when the refetch behind `invalidate` fails.
      seed(entry({ info: null, capabilities: null, phase: 'unavailable', error: '/api/info: 502' }))
      expect(result.current.phase).toBe('ready')
      expect(result.current.refreshError).toBe(true)
      expect(result.current.info?.ready).toBe(true)

      seed(entry({ info: info({ init_error: 'back' }) }))
      expect(result.current.refreshError).toBe(false)
      expect(result.current.info?.init_error).toBe('back')
    })

    it('a first load whose /api/info fetch failed (no prior info) is a page-level failure, not a refreshError', async () => {
      seed(entry({ info: null, capabilities: null, phase: 'unavailable', error: '/api/info: 500' }))
      const { result } = renderHook(() => useNexHostData(HOST_ID))
      await waitFor(() => expect(result.current.phase).toBe('failed'))
      expect(result.current.info).toBeNull()
      expect(result.current.refreshError).toBe(false)
    })

    it('the same failed store entry is a page failure on a fresh mount even though a previous mount had seen info', async () => {
      seed(entry())
      const first = renderHook(() => useNexHostData(HOST_ID))
      await waitFor(() => expect(first.result.current.phase).toBe('ready'))
      first.unmount()

      seed(entry({ info: null, capabilities: null, phase: 'unavailable', error: '/api/info: 502' }))
      const { result } = renderHook(() => useNexHostData(HOST_ID))
      await waitFor(() => expect(result.current.phase).toBe('failed'))
      expect(result.current.refreshError).toBe(false)
    })
  })

  it('retry invalidates the host and reloads /api/config', async () => {
    seed(entry())
    mockHostFetch.mockImplementationOnce(() => Promise.reject(new Error('config unreachable')))
    const { result } = renderHook(() => useNexHostData(HOST_ID))
    await waitFor(() => expect(result.current.phase).toBe('failed'))
    const before = configCalls()

    act(() => result.current.retry())
    expect(invalidateSpy).toHaveBeenCalledWith(HOST_ID)
    await waitFor(() => expect(result.current.phase).toBe('ready'))
    expect(configCalls()).toBe(before + 1)
  })

  it('a reconnect reloads /api/config and re-ensures the host (the store watcher owns the invalidate)', async () => {
    seed(entry())
    const { result } = renderHook(() => useNexHostData(HOST_ID))
    await waitFor(() => expect(result.current.phase).toBe('ready'))
    const before = configCalls()
    const ensures = ensureSpy.mock.calls.length

    act(() => { useHostStore.setState({ runtime: { [HOST_ID]: { status: 'disconnected' } } }) })
    expect(result.current.phase).toBe('offline')
    act(() => { useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected' } } }) })

    await waitFor(() => expect(configCalls()).toBe(before + 1))
    expect(ensureSpy.mock.calls.length).toBe(ensures + 1)
    expect(invalidateSpy).not.toHaveBeenCalled()
    await waitFor(() => expect(result.current.phase).toBe('ready'))
  })

  it('switching host drops the previous host\'s info and loads the new one', async () => {
    const OTHER = 'h2'
    useHostStore.setState((s) => ({
      hosts: { ...s.hosts, [OTHER]: { id: OTHER, name: 'O', ip: '1.2.3.5', port: 7860, order: 1 } },
      runtime: { ...s.runtime, [OTHER]: { status: 'connected' } },
    }))
    seed(entry({ info: info({ init_error: 'host one' }) }))
    const { result, rerender } = renderHook(({ id }) => useNexHostData(id), { initialProps: { id: HOST_ID } })
    await waitFor(() => expect(result.current.phase).toBe('ready'))

    rerender({ id: OTHER })
    expect(result.current.phase).toBe('loading')
    expect(result.current.info).toBeNull()
    expect(ensureSpy).toHaveBeenCalledWith(OTHER)

    seed(entry({ info: info({ init_error: 'host two' }) }), OTHER)
    await waitFor(() => expect(result.current.phase).toBe('ready'))
    expect(result.current.info?.init_error).toBe('host two')
  })
})
