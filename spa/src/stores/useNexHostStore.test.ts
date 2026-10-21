// spa/src/stores/useNexHostStore.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  NEX_HOST_TTL_MS,
  selectHandoffReady,
  selectPermissionAskReady,
  selectPermissionTimeoutMax,
  selectConversationsScope,
  selectReady,
  selectRollupCostShown,
  selectSessionTitleSupported,
  selectSessionFilter,
  selectWorkerRollup,
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

  it('feeds the answered host_id to the host store (spec 2026-09-23 D4.3)', async () => {
    vi.mocked(hostApi.fetchInfo).mockResolvedValueOnce(
      { ok: true, status: 200, json: () => Promise.resolve({ host_id: 'mini-lab:abc123', nex: info() }) } as Response,
    )
    await ensure()
    expect(useHostStore.getState().hosts[H].daemonId).toBe('mini-lab:abc123')
  })

  it('a host_id answered for the old endpoint after a re-point is dropped (D4.3)', async () => {
    let settle!: (r: Response) => void
    vi.mocked(hostApi.fetchInfo).mockReturnValueOnce(new Promise<Response>((resolve) => { settle = resolve }))
    const p = ensure()
    await flush()
    useHostStore.getState().updateHost(H, { port: 7861 })
    settle({ ok: true, status: 200, json: () => Promise.resolve({ host_id: 'mini-lab:old', nex: info() }) } as Response)
    await p
    expect('daemonId' in useHostStore.getState().hosts[H]).toBe(false)
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

describe('host identity (ip:port:token) is part of the cache key', () => {
  it('ready entry, host re-pointed to another ip:port within TTL → next ensure refetches', async () => {
    await ensure()
    expect(entry().phase).toBe('ready')
    useHostStore.getState().updateHost(H, { ip: '5.6.7.8', port: 7861 })
    vi.setSystemTime(T0 + 1000)
    await ensure()
    expect(hostApi.fetchInfo).toHaveBeenCalledTimes(2)
    expect(entry().phase).toBe('ready')
    expect(entry().fetchedAt).toBe(T0 + 1000)
  })

  it('token rotated during an in-flight ensure → the resolve is dropped; the entry stays loading until a fresh fetch', async () => {
    const stale = pendingInfo()
    const p = ensure()
    await flush()
    useHostStore.getState().updateHost(H, { token: 'rotated' })
    stale.settle()
    await p
    expect(entry().phase).toBe('loading')
    expect(entry().info).toBeNull()
    await ensure()
    expect(hostApi.fetchInfo).toHaveBeenCalledTimes(2)
    expect(entry().phase).toBe('ready')
  })

  it('TTL reuse after a token rotation is refused', async () => {
    await ensure()
    useHostStore.getState().updateHost(H, { token: 'rotated' })
    vi.setSystemTime(T0 + 1000)
    await ensure()
    expect(hostApi.fetchInfo).toHaveBeenCalledTimes(2)
  })

  it('an unrelated host edit (name only) keeps the cached entry', async () => {
    await ensure()
    useHostStore.getState().updateHost(H, { name: 'renamed' })
    vi.setSystemTime(T0 + 1000)
    await ensure()
    expect(hostApi.fetchInfo).toHaveBeenCalledTimes(1)
    expect(entry().phase).toBe('ready')
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

  it('a host whose ip, port or token changes has its entry dropped (capabilities belong to the old daemon)', async () => {
    stop = startNexHostInvalidation()
    registerHost(OTHER)
    await ensure()
    await ensure(OTHER)
    useHostStore.getState().updateHost(H, { token: 'rotated' })
    expect(entry()).toBeUndefined()
    expect(useNexHostStore.getState().byHost[OTHER]?.phase).toBe('ready')
    useHostStore.getState().updateHost(OTHER, { port: 7999 })
    expect(useNexHostStore.getState().byHost[OTHER]).toBeUndefined()
  })

  it('a token rotation during an in-flight ensure leaves no entry behind; the next ensure starts fresh', async () => {
    stop = startNexHostInvalidation()
    const stale = pendingInfo()
    const p = ensure()
    await flush()
    useHostStore.getState().updateHost(H, { token: 'rotated' })
    expect(entry()).toBeUndefined()
    stale.settle()
    await p
    expect(entry()).toBeUndefined()
    await ensure()
    expect(hostApi.fetchInfo).toHaveBeenCalledTimes(2)
    expect(entry().phase).toBe('ready')
  })

  it('a name-only host edit keeps the entry', async () => {
    stop = startNexHostInvalidation()
    await ensure()
    useHostStore.getState().updateHost(H, { name: 'renamed' })
    expect(entry().phase).toBe('ready')
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
      byHost: { [H]: { info: info(), capabilities: caps(), phase: 'ready', error: null, fetchedAt: T0, generation: 1, fingerprint: '1.2.3.4:7860:t', ...over } },
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

  it('selectSessionFilter goes by key presence', () => {
    const st = () => useNexHostStore.getState()
    seed({ capabilities: caps({}) })
    expect(selectSessionFilter(H)(st())).toBe(false)
    seed({ capabilities: caps({ list: { session_filter: true } }) })
    expect(selectSessionFilter(H)(st())).toBe(true)
    seed({ capabilities: caps({ list: { session_filter: false } }) })
    expect(selectSessionFilter(H)(st())).toBe(false)
    seed({ phase: 'unavailable', capabilities: caps({ list: { session_filter: true } }) })
    expect(selectSessionFilter(H)(st())).toBe(false)
    expect(selectSessionFilter('ghost')(st())).toBe(false)
  })

  it('selectWorkerRollup returns the capability object when present and ready, else null', () => {
    const rollup = { task_kinds: ['shell', 'subagent', 'other'], task_statuses: ['running'], activity_phases: ['model'], cost_basis: 'session_cumulative', subagent_cost: false }
    seed({ capabilities: caps({ worker_rollup: rollup }) })
    const got = selectWorkerRollup(H)(useNexHostStore.getState())
    expect(got).toEqual(rollup)
    // Stable reference: the selector hands back the cached object, not a copy.
    expect(selectWorkerRollup(H)(useNexHostStore.getState())).toBe(got)
    seed({})
    expect(selectWorkerRollup(H)(useNexHostStore.getState())).toBeNull()
    seed({ phase: 'unavailable', capabilities: caps({ worker_rollup: rollup }) })
    expect(selectWorkerRollup(H)(useNexHostStore.getState())).toBeNull()
    seed({ capabilities: caps({ worker_rollup: 'yes' as unknown as undefined }) })
    expect(selectWorkerRollup(H)(useNexHostStore.getState())).toBeNull()
    expect(selectWorkerRollup('ghost')(useNexHostStore.getState())).toBeNull()
  })

  // Permission channel (nexen contract §1.14, consumer-guide §9.8): the mode is offered only when BOTH the build has the
  // channel (`capabilities.permissions`) AND this host's max_profile lets `handoff_ask` through (`sandbox_profiles`).
  describe('permission channel (permission channel plan Task 6)', () => {
    const PERMS = {
      profiles: ['handoff_ask'],
      answer: { method: 'POST', path: '/api/nex/v1/executions/{id}/permissions/{request_id}' },
      timeout: { max_s: 86400 },
    }
    const ASK = ['readonly', 'standard', 'trusted', 'handoff_ask', 'handoff']
    const NO_ASK = ['readonly', 'standard', 'trusted', 'handoff']

    it.each([
      ['channel + handoff_ask usable → true', PERMS, ASK, true],
      ['channel, but max_profile below handoff_ask → false', PERMS, NO_ASK, false],
      ['handoff_ask listed, but no channel (older build) → false', undefined, ASK, false],
      ['neither → false', undefined, NO_ASK, false],
    ] as const)('selectPermissionAskReady: %s', (_label, permissions, profiles, expected) => {
      seed({ capabilities: caps({ permissions: permissions as never, sandbox_profiles: [...profiles] }) })
      expect(selectPermissionAskReady(H)(useNexHostStore.getState())).toBe(expected)
    })

    it('selectPermissionAskReady is false for a host that is not ready, or unknown', () => {
      seed({ phase: 'unavailable', capabilities: caps({ permissions: PERMS, sandbox_profiles: [...ASK] }) })
      expect(selectPermissionAskReady(H)(useNexHostStore.getState())).toBe(false)
      seed({ phase: 'loading', capabilities: null })
      expect(selectPermissionAskReady(H)(useNexHostStore.getState())).toBe(false)
      expect(selectPermissionAskReady('ghost')(useNexHostStore.getState())).toBe(false)
    })

    it('selectPermissionTimeoutMax is permissions.timeout.max_s of a ready host, else null', () => {
      const st = () => useNexHostStore.getState()
      seed({ capabilities: caps({ permissions: PERMS, sandbox_profiles: [...ASK] }) })
      expect(selectPermissionTimeoutMax(H)(st())).toBe(86400)
      // PR-B build: the channel without the timeout object — `permission_timeout_s` must not be sent.
      seed({ capabilities: caps({ permissions: { profiles: PERMS.profiles, answer: PERMS.answer }, sandbox_profiles: [...ASK] }) })
      expect(selectPermissionTimeoutMax(H)(st())).toBeNull()
      seed({ capabilities: caps({ sandbox_profiles: [...ASK] }) })
      expect(selectPermissionTimeoutMax(H)(st())).toBeNull()
      seed({ capabilities: caps({ permissions: { ...PERMS, timeout: { max_s: 'x' } } as never }) })
      expect(selectPermissionTimeoutMax(H)(st())).toBeNull()
      seed({ phase: 'unavailable', capabilities: caps({ permissions: PERMS }) })
      expect(selectPermissionTimeoutMax(H)(st())).toBeNull()
      expect(selectPermissionTimeoutMax('ghost')(st())).toBeNull()
    })
  })

  it('selectRollupCostShown: only cost_basis "result_evidence" shows the rollup cost (v0.13.1 session_cumulative under-counts)', () => {
    const rollup = (cost_basis?: string) => ({ task_kinds: [], task_statuses: [], activity_phases: [], cost_basis, subagent_cost: false })
    seed({ capabilities: caps({ worker_rollup: rollup('result_evidence') }) })
    expect(selectRollupCostShown(H)(useNexHostStore.getState())).toBe(true)
    for (const basis of ['session_cumulative', 'something_new', undefined]) {
      seed({ capabilities: caps({ worker_rollup: rollup(basis) }) })
      expect(selectRollupCostShown(H)(useNexHostStore.getState())).toBe(false)
    }
    seed({})
    expect(selectRollupCostShown(H)(useNexHostStore.getState())).toBe(false)
    seed({ phase: 'unavailable', capabilities: caps({ worker_rollup: rollup('result_evidence') }) })
    expect(selectRollupCostShown(H)(useNexHostStore.getState())).toBe(false)
    expect(selectRollupCostShown('ghost')(useNexHostStore.getState())).toBe(false)
  })

  it('selectHandoffReady is false for an unknown host', () => {
    expect(selectHandoffReady('ghost')(useNexHostStore.getState())).toBe(false)
  })

  describe('selectSessionTitleSupported (E1)', () => {
    it('v0.13.2-shaped caps (no session_title) → false', () => {
      seed({ capabilities: caps({ session_title: undefined }) })
      expect(selectSessionTitleSupported(H)(useNexHostStore.getState())).toBe(false)
    })

    it('session_title object present → true', () => {
      seed({ capabilities: caps({ session_title: { sources: ['custom', 'agent_name', 'ai'], max_bytes: 200 } }) })
      expect(selectSessionTitleSupported(H)(useNexHostStore.getState())).toBe(true)
    })

    it('not ready or unknown host → false', () => {
      seed({ phase: 'unavailable', capabilities: caps({ session_title: { sources: [], max_bytes: 200 } }) })
      expect(selectSessionTitleSupported(H)(useNexHostStore.getState())).toBe(false)
      expect(selectSessionTitleSupported('ghost')(useNexHostStore.getState())).toBe(false)
    })
  })
})

describe('wwd identity of /api/info (monitoring tabs MON-1a)', () => {
  const infoWithWwd = (wwd: unknown): Response =>
    ({ ok: true, status: 200, json: () => Promise.resolve({ nex: info(), wwd }) }) as Response
  const load = async (wwd: unknown) => {
    useNexHostStore.setState({ byHost: {} })
    vi.mocked(hostApi.fetchInfo).mockResolvedValue(infoWithWwd(wwd))
    await ensure()
    return entry().wwd
  }

  it('keeps host_id and alias', async () => {
    expect(await load({ host_id: '82b11c521df16c12', alias: 'mlab' })).toEqual({ hostId: '82b11c521df16c12', alias: 'mlab' })
  })
  it('missing key → null', async () => {
    expect(await load(undefined)).toBeNull()
  })
  it('empty or non-string host_id → null', async () => {
    expect(await load({ host_id: '', alias: 'mlab' })).toBeNull()
    expect(await load({ host_id: 7, alias: 'mlab' })).toBeNull()
    expect(await load('mlab')).toBeNull()
  })
  it('a non-string alias reads as empty', async () => {
    expect(await load({ host_id: '82b11c521df16c12', alias: 3 })).toEqual({ hostId: '82b11c521df16c12', alias: '' })
  })
  it('an answer from before a clearHost does not write it', async () => {
    let settle!: (r: Response) => void
    vi.mocked(hostApi.fetchInfo).mockReturnValueOnce(new Promise<Response>((resolve) => { settle = resolve }))
    const p = ensure()
    await flush()
    useNexHostStore.getState().clearHost(H)
    settle(infoWithWwd({ host_id: '82b11c521df16c12', alias: 'mlab' }))
    await p
    expect(entry()).toBeUndefined()
  })
})

describe('wwdalerts of /api/info (monitoring tabs MON-9a-2b: who delivers the push)', () => {
  const load = async (wwdalerts: unknown) => {
    useNexHostStore.setState({ byHost: {} })
    vi.mocked(hostApi.fetchInfo).mockResolvedValue(({ ok: true, status: 200, json: () => Promise.resolve({ nex: info(), wwdalerts }) }) as Response)
    await ensure()
    return entry().wwdAlerts
  }

  it('keeps the switch word and whether this daemon is the deliverer', async () => {
    expect(await load({ push: 'on', deliverer: true, suppressed: false })).toEqual({ push: 'on', deliverer: true })
    expect(await load({ push: 'off', deliverer: false })).toEqual({ push: 'off', deliverer: false })
  })
  it('missing, null or a module that is not mounted → null', async () => {
    expect(await load(undefined)).toBeNull()
    expect(await load(null)).toBeNull()
  })
  it('a word or a deliverer it does not know → null, not a guess', async () => {
    expect(await load({ push: 'maybe', deliverer: true })).toBeNull()
    expect(await load({ push: 'on', deliverer: 'yes' })).toBeNull()
    expect(await load({ push: 'on' })).toBeNull()
    expect(await load('on')).toBeNull()
  })
})

describe('daemonCapabilities (worker test tab S2)', () => {
  const infoWith = (capabilities: unknown): Response =>
    ({ ok: true, status: 200, json: () => Promise.resolve({ nex: info(), capabilities }) }) as Response
  const load = async (capabilities: unknown) => {
    vi.mocked(hostApi.fetchInfo).mockResolvedValue(infoWith(capabilities))
    await ensure()
    return entry().daemonCapabilities
  }

  it('keeps the strings of /api/info.capabilities', async () => {
    expect(await load(['transcript.v1', 'conversations.scope.v1'])).toEqual(['transcript.v1', 'conversations.scope.v1'])
  })
  it('missing → []', async () => {
    expect(await load(undefined)).toEqual([])
  })
  it('non-array → []', async () => {
    expect(await load({ a: 1 })).toEqual([])
  })
  it('drops non-string elements', async () => {
    expect(await load(['x', 3, null, 'conversations.scope.v1'])).toEqual(['x', 'conversations.scope.v1'])
  })

  describe('selectConversationsScope', () => {
    const seed = (over: Partial<NexHostEntry>) =>
      useNexHostStore.setState({
        byHost: { [H]: { info: info(), capabilities: caps(), phase: 'ready', error: null, fetchedAt: T0, generation: 1, fingerprint: 'x', ...over } },
      })
    it('true only when ready and the capability is listed', () => {
      seed({ daemonCapabilities: ['conversations.scope.v1'] })
      expect(selectConversationsScope(H)(useNexHostStore.getState())).toBe(true)
      seed({ daemonCapabilities: ['transcript.v1'] })
      expect(selectConversationsScope(H)(useNexHostStore.getState())).toBe(false)
      seed({})
      expect(selectConversationsScope(H)(useNexHostStore.getState())).toBe(false)
    })
    it('false when not ready or unknown host', () => {
      seed({ phase: 'unavailable', daemonCapabilities: ['conversations.scope.v1'] })
      expect(selectConversationsScope(H)(useNexHostStore.getState())).toBe(false)
      expect(selectConversationsScope('ghost')(useNexHostStore.getState())).toBe(false)
    })
  })
})
