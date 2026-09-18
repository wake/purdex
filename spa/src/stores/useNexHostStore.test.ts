// spa/src/stores/useNexHostStore.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  NEX_HOST_TTL_MS,
  selectHandoffReady,
  selectReady,
  startNexHostInvalidation,
  useNexHostStore,
  type NexHostEntry,
} from './useNexHostStore'
import { useHostStore } from './useHostStore'
import * as hostApi from '../lib/host-api'
import * as nexApi from '../lib/nex/nex-api'
import { NexApiError, type NexCapabilities } from '../lib/nex/types'
import type { NexInfo } from '../lib/host-api'

vi.mock('../lib/host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/host-api')>()),
  fetchInfo: vi.fn(),
}))
vi.mock('../lib/nex/nex-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/nex/nex-api')>()),
  fetchNexCapabilities: vi.fn(),
}))

const H = 'h1'
const OTHER = 'h2'
const T0 = new Date('2026-09-18T00:00:00Z').getTime()

const info = (over: Partial<NexInfo> = {}): NexInfo =>
  ({ configured: true, mounted: true, ready: true, init_error: '', effective: null, ...over })

const caps = (over: Partial<NexCapabilities> = {}): NexCapabilities =>
  ({
    phase: 'ga', host_id: H, verbs: [], providers: ['claude'], events: [], provider_events: [], transient_events: [],
    sandbox_profiles: ['default', 'handoff'], sandbox_default_profile: 'default', sandbox_max_profile: 'handoff',
    roots: [], lease: { ttl_seconds: 30, scope: 'x', renew: { method: 'POST', path: '/r' }, release: { method: 'DELETE', path: '/r' } },
    send: { delivery: ['text'], max_text_bytes: 1 },
    delegate: { resume_session_id: true },
    ...over,
  })

function infoResponse(nex: NexInfo | undefined, ok = true, status = 200): Response {
  return { ok, status, json: () => Promise.resolve({ nex }) } as Response
}

function registerHost(hostId = H, ip = '1.2.3.4', port = 7860) {
  useHostStore.setState((s) => ({
    hosts: { ...s.hosts, [hostId]: { id: hostId, name: hostId, ip, port, token: 't', order: 0 } },
    hostOrder: [...s.hostOrder.filter((id) => id !== hostId), hostId],
    runtime: { ...s.runtime, [hostId]: { status: 'connected' } },
  }))
}

function unregisterHost(hostId: string) {
  useHostStore.setState((s) => {
    const hosts = { ...s.hosts }
    delete hosts[hostId]
    return { hosts, hostOrder: s.hostOrder.filter((id) => id !== hostId) }
  })
}

/** A `/api/info` nobody has answered yet, plus the switch that answers it. */
function pendingInfo() {
  let settle!: (r: Response) => void
  vi.mocked(hostApi.fetchInfo).mockReturnValueOnce(new Promise<Response>((resolve) => { settle = resolve }))
  return { settle: (nex: NexInfo = info()) => settle(infoResponse(nex)) }
}

function pendingCaps() {
  let settle!: (c: NexCapabilities) => void
  vi.mocked(nexApi.fetchNexCapabilities).mockReturnValueOnce(new Promise<NexCapabilities>((resolve) => { settle = resolve }))
  return { settle: (c: NexCapabilities = caps()) => settle(c) }
}

const flush = () => new Promise<void>((r) => { queueMicrotask(() => queueMicrotask(() => queueMicrotask(r))) })

const entry = () => useNexHostStore.getState().byHost[H]
const ensure = (hostId = H) => useNexHostStore.getState().ensure(hostId)

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(T0)
  vi.mocked(hostApi.fetchInfo).mockReset().mockImplementation(() => Promise.resolve(infoResponse(info())))
  vi.mocked(nexApi.fetchNexCapabilities).mockReset().mockResolvedValue(caps())
  useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {}, activeHostId: null })
  useNexHostStore.setState({ byHost: {} })
  registerHost()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('phase matrix', () => {
  it('configured=false → disabled, no capabilities call', async () => {
    vi.mocked(hostApi.fetchInfo).mockResolvedValue(infoResponse(info({ configured: false, mounted: false, ready: false })))
    await ensure()
    expect(entry().phase).toBe('disabled')
    expect(entry().capabilities).toBeNull()
    expect(nexApi.fetchNexCapabilities).not.toHaveBeenCalled()
  })

  it('mounted=false → disabled', async () => {
    vi.mocked(hostApi.fetchInfo).mockResolvedValue(infoResponse(info({ mounted: false, ready: false })))
    await ensure()
    expect(entry().phase).toBe('disabled')
    expect(nexApi.fetchNexCapabilities).not.toHaveBeenCalled()
  })

  it('info.ready true but capabilities 503 → unavailable with the message', async () => {
    vi.mocked(nexApi.fetchNexCapabilities).mockRejectedValue(new NexApiError(503, 'nex_unavailable', 'engine down'))
    await ensure()
    expect(entry().phase).toBe('unavailable')
    expect(entry().error).toBe('engine down')
    expect(entry().info).toEqual(info())
    expect(entry().capabilities).toBeNull()
  })

  it('both ok → ready with info and capabilities, fetchedAt stamped', async () => {
    await ensure()
    expect(entry().phase).toBe('ready')
    expect(entry().info).toEqual(info())
    expect(entry().capabilities).toEqual(caps())
    expect(entry().error).toBeNull()
    expect(entry().fetchedAt).toBe(T0)
  })

  it('init_error → unavailable with the message, no capabilities call', async () => {
    vi.mocked(hostApi.fetchInfo).mockResolvedValue(infoResponse(info({ ready: false, init_error: 'bad config' })))
    await ensure()
    expect(entry().phase).toBe('unavailable')
    expect(entry().error).toBe('bad config')
    expect(nexApi.fetchNexCapabilities).not.toHaveBeenCalled()
  })

  it('network failure on /api/info → unavailable with the message', async () => {
    vi.mocked(hostApi.fetchInfo).mockRejectedValue(new TypeError('Failed to fetch'))
    await ensure()
    expect(entry().phase).toBe('unavailable')
    expect(entry().error).toBe('Failed to fetch')
  })

  it('non-2xx /api/info → unavailable', async () => {
    vi.mocked(hostApi.fetchInfo).mockResolvedValue(infoResponse(undefined, false, 502))
    await ensure()
    expect(entry().phase).toBe('unavailable')
    expect(entry().error).toContain('502')
  })

  it('configured+mounted but not ready and no init_error → unavailable', async () => {
    vi.mocked(hostApi.fetchInfo).mockResolvedValue(infoResponse(info({ ready: false })))
    await ensure()
    expect(entry().phase).toBe('unavailable')
    expect(nexApi.fetchNexCapabilities).not.toHaveBeenCalled()
  })
})

describe('invariant: ready ⇔ isNexReady(info) && capabilities !== null', () => {
  it('first load with info ready and capabilities pending is loading, not ready', async () => {
    const c = pendingCaps()
    const p = ensure()
    await flush()
    expect(entry().phase).toBe('loading')
    expect(entry().info).toBeNull()
    expect(selectReady(H)(useNexHostStore.getState())).toBe(false)
    c.settle()
    await p
    expect(entry().phase).toBe('ready')
    expect(selectReady(H)(useNexHostStore.getState())).toBe(true)
  })

  it('a refresh whose capabilities call fails flips ready → unavailable', async () => {
    await ensure()
    expect(entry().phase).toBe('ready')
    vi.mocked(nexApi.fetchNexCapabilities).mockRejectedValue(new NexApiError(0, 'network', 'offline'))
    await useNexHostStore.getState().invalidate(H)
    expect(entry().phase).toBe('unavailable')
    expect(entry().capabilities).toBeNull()
    expect(entry().error).toBe('offline')
  })

  it('a refresh that returns info.ready=false leaves ready', async () => {
    await ensure()
    vi.mocked(hostApi.fetchInfo).mockResolvedValue(infoResponse(info({ ready: false })))
    await useNexHostStore.getState().invalidate(H)
    expect(entry().phase).toBe('unavailable')
    expect(entry().capabilities).toBeNull()
  })

  it('keeps the previous data (no loading flip) while a refresh is in flight', async () => {
    await ensure()
    const i = pendingInfo()
    const p = useNexHostStore.getState().invalidate(H)
    await flush()
    expect(entry().phase).toBe('ready')
    i.settle()
    await p
    expect(entry().phase).toBe('ready')
  })
})

describe('TTL', () => {
  it('second ensure within 60 s makes no fetch', async () => {
    await ensure()
    vi.setSystemTime(T0 + NEX_HOST_TTL_MS - 1)
    await ensure()
    expect(hostApi.fetchInfo).toHaveBeenCalledTimes(1)
  })

  it('ensure after 60 s refetches', async () => {
    await ensure()
    vi.setSystemTime(T0 + NEX_HOST_TTL_MS)
    await ensure()
    expect(hostApi.fetchInfo).toHaveBeenCalledTimes(2)
  })

  it('unavailable is never cached: the next ensure refetches within the TTL', async () => {
    vi.mocked(nexApi.fetchNexCapabilities).mockRejectedValueOnce(new NexApiError(503, 'nex_unavailable', 'down'))
    await ensure()
    expect(entry().phase).toBe('unavailable')
    await ensure()
    expect(hostApi.fetchInfo).toHaveBeenCalledTimes(2)
    expect(entry().phase).toBe('ready')
  })
})

describe('in-flight dedup', () => {
  it('two concurrent ensure calls make one fetch', async () => {
    const i = pendingInfo()
    const a = ensure()
    const b = ensure()
    await flush()
    expect(hostApi.fetchInfo).toHaveBeenCalledTimes(1)
    i.settle()
    await Promise.all([a, b])
    expect(entry().phase).toBe('ready')
    expect(hostApi.fetchInfo).toHaveBeenCalledTimes(1)
  })
})

describe('invalidate', () => {
  it('forces a fetch inside the TTL', async () => {
    await ensure()
    await useNexHostStore.getState().invalidate(H)
    expect(hostApi.fetchInfo).toHaveBeenCalledTimes(2)
  })

  it('bumps the generation', async () => {
    await ensure()
    const before = entry().generation
    await useNexHostStore.getState().invalidate(H)
    expect(entry().generation).toBeGreaterThan(before)
  })

  it('during an in-flight fetch drops that fetch result and the fresh one wins', async () => {
    const stale = pendingInfo()
    const p1 = ensure()
    await flush()
    const fresh = pendingInfo()
    const p2 = useNexHostStore.getState().invalidate(H)
    await flush()
    expect(hostApi.fetchInfo).toHaveBeenCalledTimes(2)
    fresh.settle(info({ effective: { default_profile: 'fresh' } as NexInfo['effective'] }))
    await p2
    expect(entry().info?.effective?.default_profile).toBe('fresh')
    stale.settle(info({ effective: { default_profile: 'stale' } as NexInfo['effective'] }))
    await p1
    expect(entry().info?.effective?.default_profile).toBe('fresh')
    expect(entry().phase).toBe('ready')
  })
})

describe('stale resolves', () => {
  it('after clearHost are ignored and never resurrect the entry', async () => {
    const i = pendingInfo()
    const p = ensure()
    await flush()
    useNexHostStore.getState().clearHost(H)
    expect(entry()).toBeUndefined()
    i.settle()
    await p
    expect(entry()).toBeUndefined()
  })

  it('after the same host id is re-added are ignored', async () => {
    const i = pendingInfo()
    const p = ensure()
    await flush()
    unregisterHost(H)
    useNexHostStore.getState().clearHost(H)
    registerHost(H)
    i.settle()
    await p
    expect(entry()).toBeUndefined()
  })

  it('after the host endpoint changed are ignored', async () => {
    const i = pendingInfo()
    const p = ensure()
    await flush()
    useHostStore.getState().updateHost(H, { port: 7861 })
    i.settle()
    await p
    expect(entry().phase).toBe('loading')
    expect(entry().info).toBeNull()
  })

  it('after the host was removed without clearHost are ignored', async () => {
    const i = pendingInfo()
    const p = ensure()
    await flush()
    unregisterHost(H)
    i.settle()
    await p
    expect(entry()?.phase ?? 'loading').toBe('loading')
    expect(entry()?.info ?? null).toBeNull()
  })
})

describe('unknown host', () => {
  it('ensure makes no fetch and leaves no entry', async () => {
    await ensure('ghost')
    expect(hostApi.fetchInfo).not.toHaveBeenCalled()
    expect(useNexHostStore.getState().byHost.ghost).toBeUndefined()
  })

  it('ensure for a host that vanished drops its stale entry', async () => {
    await ensure()
    unregisterHost(H)
    await ensure()
    expect(entry()).toBeUndefined()
    expect(hostApi.fetchInfo).toHaveBeenCalledTimes(1)
  })
})

describe('clearHost', () => {
  it('drops only that host', async () => {
    registerHost(OTHER)
    await ensure(H)
    await ensure(OTHER)
    useNexHostStore.getState().clearHost(H)
    expect(entry()).toBeUndefined()
    expect(useNexHostStore.getState().byHost[OTHER]?.phase).toBe('ready')
  })
})

describe('startNexHostInvalidation', () => {
  let stop: (() => void) | undefined
  afterEach(() => { stop?.(); stop = undefined })

  it('reconnect transition → next ensure refetches', async () => {
    stop = startNexHostInvalidation()
    await ensure()
    expect(hostApi.fetchInfo).toHaveBeenCalledTimes(1)
    useHostStore.getState().setRuntime(H, { status: 'disconnected' })
    useHostStore.getState().setRuntime(H, { status: 'connected' })
    await ensure()
    expect(hostApi.fetchInfo).toHaveBeenCalledTimes(2)
  })

  it('a runtime change that is not a transition into connected does nothing', async () => {
    stop = startNexHostInvalidation()
    await ensure()
    useHostStore.getState().setRuntime(H, { status: 'connected', latency: 5 })
    useHostStore.getState().setRuntime(H, { status: 'disconnected' })
    await flush()
    expect(hostApi.fetchInfo).toHaveBeenCalledTimes(1)
  })

  it('a host nobody asked about is not fetched on reconnect', async () => {
    stop = startNexHostInvalidation()
    useHostStore.getState().setRuntime(H, { status: 'disconnected' })
    useHostStore.getState().setRuntime(H, { status: 'connected' })
    await flush()
    expect(hostApi.fetchInfo).not.toHaveBeenCalled()
    expect(entry()).toBeUndefined()
  })
})

describe('selectors', () => {
  const seed = (over: Partial<NexHostEntry>) =>
    useNexHostStore.setState({
      byHost: { [H]: { info: info(), capabilities: caps(), phase: 'ready', error: null, fetchedAt: T0, generation: 1, ...over } },
    })

  it('selectReady is phase === ready', () => {
    seed({})
    expect(selectReady(H)(useNexHostStore.getState())).toBe(true)
    seed({ phase: 'unavailable' })
    expect(selectReady(H)(useNexHostStore.getState())).toBe(false)
    expect(selectReady('ghost')(useNexHostStore.getState())).toBe(false)
  })

  it.each([
    ['ready, resume supported, handoff profile', 'ready', true, ['default', 'handoff'], true],
    ['not ready', 'unavailable', true, ['default', 'handoff'], false],
    ['resume unsupported', 'ready', false, ['default', 'handoff'], false],
    ['resume flag absent', 'ready', undefined, ['default', 'handoff'], false],
    ['no handoff profile', 'ready', true, ['default'], false],
  ] as const)('selectHandoffReady: %s → %s', (_label, phase, resume, profiles, expected) => {
    seed({
      phase,
      capabilities: caps({
        sandbox_profiles: [...profiles],
        delegate: resume === undefined ? undefined : { resume_session_id: resume },
      }),
    })
    expect(selectHandoffReady(H)(useNexHostStore.getState())).toBe(expected)
  })

  it('selectHandoffReady is false for an unknown host', () => {
    expect(selectHandoffReady('ghost')(useNexHostStore.getState())).toBe(false)
  })
})
