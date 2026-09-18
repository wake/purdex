// spa/src/stores/useExecutionListStore.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import {
  LIST_REFRESH_DEBOUNCE_MS,
  resetExecutionListForTests,
  startExecutionListInvalidation,
  useExecutionListStore,
} from './useExecutionListStore'
import { startNexHostInvalidation, useNexHostStore, type NexHostEntry } from './useNexHostStore'
import { useHostStore } from './useHostStore'
import { subscriptionSlots, capFor } from '../lib/nex/subscription-slots'
import { NexApiError, type ExecutionSummary, type ExecutionsPage } from '../lib/nex/types'
import type { NexInfo } from '../lib/host-api'
import * as api from '../lib/nex/nex-api'
import * as sse from '../lib/nex/nex-sse'
import type { NexSseOptions } from '../lib/nex/nex-sse'

vi.mock('../lib/nex/nex-api', () => ({ listExecutions: vi.fn() }))
vi.mock('../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))

const A = 'host-a'
const B = 'host-b'

const row = (id: string): ExecutionSummary =>
  ({ id, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false }) as ExecutionSummary
const page = (...ids: string[]): ExecutionsPage => ({ items: ids.map(row), next_cursor: '' })

const nexInfo = (ready: boolean): NexInfo => ({ configured: true, mounted: true, ready, init_error: '', effective: null })
const nexEntry = (ready: boolean, fingerprint = '1:1:'): NexHostEntry =>
  ({ info: nexInfo(ready), capabilities: null, phase: ready ? 'ready' : 'unavailable', error: null, fetchedAt: 1, generation: 1, fingerprint })

const setNexReady = (hostId: string, ready: boolean) =>
  useNexHostStore.setState((s) => ({ byHost: { ...s.byHost, [hostId]: nexEntry(ready) } }))

interface OpenedSse { opts: NexSseOptions; close: ReturnType<typeof vi.fn<() => void>> }
let opened: OpenedSse[]
const sseFor = (hostId: string): OpenedSse => {
  const found = [...opened].reverse().find((o) => o.opts.hostId === hostId)
  if (!found) throw new Error(`no SSE opened for ${hostId}`)
  return found
}
const frame = (id: string | null = null) => ({ id, event: 'execution.updated', data: '{}' })

type Deferred = { resolve: (p: ExecutionsPage) => void; reject: (e: unknown) => void }
const deferList = (): Deferred => {
  const d = {} as Deferred
  vi.mocked(api.listExecutions).mockImplementationOnce(() => new Promise<ExecutionsPage>((resolve, reject) => { d.resolve = resolve; d.reject = reject }))
  return d
}

const flush = () => vi.advanceTimersByTimeAsync(0)
const cache = (hostId: string) => useExecutionListStore.getState().byHost[hostId]
const listCallsFor = (hostId: string) => vi.mocked(api.listExecutions).mock.calls.filter((c) => c[0] === hostId).length

let stopWatchers: () => void

describe('useExecutionListStore', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    opened = []
    subscriptionSlots.resetForTests()
    resetExecutionListForTests()
    useExecutionListStore.setState({ byHost: {} })
    useNexHostStore.setState({ byHost: { [A]: nexEntry(true), [B]: nexEntry(true) } })
    useHostStore.setState({
      hosts: {
        [A]: { id: A, name: 'A', ip: '1', port: 1, token: 't', order: 0 },
        [B]: { id: B, name: 'B', ip: '2', port: 2, token: 't', order: 1 },
      },
      hostOrder: [A, B],
      activeHostId: A,
      runtime: {},
    })
    vi.mocked(sse.openNexSse).mockReset().mockImplementation((o) => {
      const h: OpenedSse = { opts: o, close: vi.fn<() => void>() }
      opened.push(h)
      return { close: h.close }
    })
    vi.mocked(api.listExecutions).mockReset().mockResolvedValue(page('exc_1'))
    vi.spyOn(subscriptionSlots, 'reserve')
    vi.spyOn(subscriptionSlots, 'unreserve')
    const stopNex = startNexHostInvalidation()
    const stopList = startExecutionListInvalidation()
    stopWatchers = () => { stopNex(); stopList() }
  })
  afterEach(() => {
    stopWatchers()
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('two subscribers share one SSE and one reservation; the last unsubscribe closes and unreserves but keeps the rows', async () => {
    const u1 = useExecutionListStore.getState().subscribe(A)
    const u2 = useExecutionListStore.getState().subscribe(A)
    expect(sse.openNexSse).toHaveBeenCalledTimes(1)
    expect(sseFor(A).opts.url).toBe('/api/nex/v1/events')
    expect(subscriptionSlots.reserve).toHaveBeenCalledTimes(1)
    expect(subscriptionSlots.reserve).toHaveBeenCalledWith(A, 'site-wide')
    expect(capFor(A)).toBe(3)
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
    expect(api.listExecutions).toHaveBeenCalledWith(A, { includeArchived: false, limit: 100 })
    expect(cache(A).phase).toBe('loading')

    await flush()
    expect(cache(A).phase).toBe('ready')
    expect(cache(A).items.map((r) => r.id)).toEqual(['exc_1'])

    u1()
    expect(sseFor(A).close).not.toHaveBeenCalled()
    expect(subscriptionSlots.unreserve).not.toHaveBeenCalled()

    u2()
    expect(sseFor(A).close).toHaveBeenCalledTimes(1)
    expect(subscriptionSlots.unreserve).toHaveBeenCalledTimes(1)
    expect(capFor(A)).toBe(4)
    expect(cache(A).phase).toBe('ready')
    expect(cache(A).items.map((r) => r.id)).toEqual(['exc_1'])
  })

  it('unsubscribe is idempotent per token', () => {
    const u1 = useExecutionListStore.getState().subscribe(A)
    const u2 = useExecutionListStore.getState().subscribe(A)
    u1()
    u1()
    u1()
    expect(sseFor(A).close).not.toHaveBeenCalled()
    u2()
    expect(sseFor(A).close).toHaveBeenCalledTimes(1)
    u2()
    expect(sseFor(A).close).toHaveBeenCalledTimes(1)
    expect(subscriptionSlots.unreserve).toHaveBeenCalledTimes(1)
  })

  it('frames are coalesced into one debounced refetch', async () => {
    useExecutionListStore.getState().subscribe(A)
    await flush()
    expect(api.listExecutions).toHaveBeenCalledTimes(1)

    sseFor(A).opts.onFrame(frame())
    sseFor(A).opts.onFrame(frame())
    sseFor(A).opts.onFrame(frame())
    await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS - 1)
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(api.listExecutions).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS)
    expect(api.listExecutions).toHaveBeenCalledTimes(2)
  })

  it('a durable frame id advances lastSeq and is replayed as Last-Event-ID on reconnect', () => {
    useExecutionListStore.getState().subscribe(A)
    const { opts } = sseFor(A)
    expect(opts.getLastEventId()).toBeNull()

    opts.onFrame(frame(null))
    expect(cache(A).lastSeq).toBeNull()
    opts.onFrame(frame('7'))
    opts.onFrame(frame('5'))
    opts.onFrame(frame(null))
    expect(cache(A).lastSeq).toBe(7)
    expect(opts.getLastEventId()).toBe(7)
  })

  it('reconnecting then open refetches', async () => {
    useExecutionListStore.getState().subscribe(A)
    await flush()
    const { opts } = sseFor(A)
    opts.onStatus('connecting')
    opts.onStatus('open')
    await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS)
    expect(api.listExecutions).toHaveBeenCalledTimes(1)

    opts.onStatus('reconnecting')
    opts.onStatus('open')
    await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS)
    expect(api.listExecutions).toHaveBeenCalledTimes(2)
  })

  it('not info-ready: subscribe opens nothing; readiness flipping true opens exactly once', () => {
    setNexReady(A, false)
    useExecutionListStore.getState().subscribe(A)
    useExecutionListStore.getState().subscribe(A)
    expect(sse.openNexSse).not.toHaveBeenCalled()
    expect(api.listExecutions).not.toHaveBeenCalled()
    expect(subscriptionSlots.reserve).not.toHaveBeenCalled()
    expect(cache(A).phase).toBe('idle')

    setNexReady(A, true)
    expect(sse.openNexSse).toHaveBeenCalledTimes(1)
    expect(subscriptionSlots.reserve).toHaveBeenCalledTimes(1)
    expect(api.listExecutions).toHaveBeenCalledTimes(1)

    setNexReady(A, true)
    expect(sse.openNexSse).toHaveBeenCalledTimes(1)
  })

  it('readiness turning false invalidates an in-flight fetch and pending debounce', async () => {
    const first = deferList()
    useExecutionListStore.getState().subscribe(A)
    sseFor(A).opts.onFrame(frame())

    setNexReady(A, false)
    expect(sseFor(A).close).toHaveBeenCalledTimes(1)
    expect(subscriptionSlots.unreserve).toHaveBeenCalledTimes(1)

    first.resolve(page('stale'))
    await flush()
    expect(cache(A).items).toEqual([])
    expect(cache(A).phase).toBe('idle')

    await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS)
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
  })

  it('host fingerprint change drops cached rows and reconnects only after the new host is ready', async () => {
    useExecutionListStore.getState().subscribe(A)
    await flush()
    sseFor(A).opts.onFrame(frame('9'))
    expect(cache(A).items).toHaveLength(1)
    expect(cache(A).lastSeq).toBe(9)

    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [A]: { ...s.hosts[A], ip: '9.9.9.9' } } }))
    expect(sseFor(A).close).toHaveBeenCalledTimes(1)
    expect(subscriptionSlots.unreserve).toHaveBeenCalledTimes(1)
    expect(cache(A).items).toEqual([])
    expect(cache(A).lastSeq).toBeNull()
    expect(cache(A).phase).toBe('idle')
    expect(sse.openNexSse).toHaveBeenCalledTimes(1)

    setNexReady(A, true)
    expect(sse.openNexSse).toHaveBeenCalledTimes(2)
    expect(subscriptionSlots.reserve).toHaveBeenCalledTimes(2)
    expect(sseFor(A).opts.getLastEventId()).toBeNull()
    expect(listCallsFor(A)).toBe(2)
  })

  it('identity change with a live subscriber re-ensures nex readiness and reopens once it is ready', async () => {
    const realEnsure = useNexHostStore.getState().ensure
    const ensure = vi.fn<(hostId: string) => Promise<void>>().mockResolvedValue(undefined)
    useNexHostStore.setState({ ensure })
    try {
      useExecutionListStore.getState().subscribe(A)
      await flush()
      expect(sse.openNexSse).toHaveBeenCalledTimes(1)

      // The nex-host watcher (registered first, as in main.tsx) has already
      // dropped A's entry when ours runs, so `ensure` fetches fresh info.
      useHostStore.setState((s) => ({ hosts: { ...s.hosts, [A]: { ...s.hosts[A], token: 'rotated' } } }))
      expect(useNexHostStore.getState().byHost[A]).toBeUndefined()
      expect(ensure).toHaveBeenCalledTimes(1)
      expect(ensure).toHaveBeenCalledWith(A)
      expect(sse.openNexSse).toHaveBeenCalledTimes(1)

      // The fresh info lands ready → the readiness watcher reopens exactly once.
      setNexReady(A, true)
      expect(sse.openNexSse).toHaveBeenCalledTimes(2)
      expect(listCallsFor(A)).toBe(2)
      setNexReady(A, true)
      expect(sse.openNexSse).toHaveBeenCalledTimes(2)
    } finally {
      useNexHostStore.setState({ ensure: realEnsure })
    }
  })

  it('identity change without a subscriber does not re-ensure nex readiness', async () => {
    const realEnsure = useNexHostStore.getState().ensure
    const ensure = vi.fn<(hostId: string) => Promise<void>>().mockResolvedValue(undefined)
    useNexHostStore.setState({ ensure })
    try {
      const unsubscribe = useExecutionListStore.getState().subscribe(A)
      await flush()
      unsubscribe()
      useHostStore.setState((s) => ({ hosts: { ...s.hosts, [A]: { ...s.hosts[A], token: 'rotated' } } }))
      expect(cache(A).items).toEqual([])
      expect(ensure).not.toHaveBeenCalled()
    } finally {
      useNexHostStore.setState({ ensure: realEnsure })
    }
  })

  it('a host removed and re-added with a new identity mid-fetch does not commit the old daemon rows', async () => {
    const first = deferList()
    useExecutionListStore.getState().subscribe(A)

    useHostStore.setState((s) => {
      const hosts = { ...s.hosts }
      delete hosts[A]
      return { hosts }
    })
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [A]: { id: A, name: 'A', ip: '9.9.9.9', port: 1, token: 't', order: 0 } } }))

    first.resolve(page('old-daemon'))
    await flush()
    expect(cache(A).items).toEqual([])
    expect(cache(A).phase).not.toBe('ready')
  })

  it('clearHost while subscribed drops data, releases the lane, and later readiness reopens exactly once', async () => {
    useExecutionListStore.getState().subscribe(A)
    await flush()
    sseFor(A).opts.onFrame(frame('3'))
    expect(cache(A).items).toHaveLength(1)

    useExecutionListStore.getState().clearHost(A)
    useNexHostStore.setState((s) => {
      const byHost = { ...s.byHost }
      delete byHost[A]
      return { byHost }
    })
    expect(cache(A)).toBeUndefined()
    expect(sseFor(A).close).toHaveBeenCalledTimes(1)
    expect(subscriptionSlots.unreserve).toHaveBeenCalledTimes(1)
    expect(capFor(A)).toBe(4)

    setNexReady(A, true)
    expect(sse.openNexSse).toHaveBeenCalledTimes(2)
    expect(subscriptionSlots.reserve).toHaveBeenCalledTimes(2)
    expect(sseFor(A).opts.getLastEventId()).toBeNull()
    await flush()
    expect(cache(A).items).toHaveLength(1)
    setNexReady(A, true)
    expect(sse.openNexSse).toHaveBeenCalledTimes(2)
  })

  it('terminal SSE close releases the reserved lane and does not double-unreserve on cleanup', async () => {
    const unsubscribe = useExecutionListStore.getState().subscribe(A)
    await flush()
    expect(capFor(A)).toBe(3)

    sseFor(A).opts.onStatus('closed', new NexApiError(503, 'nex_unavailable', 'init failed'))
    expect(subscriptionSlots.unreserve).toHaveBeenCalledTimes(1)
    expect(capFor(A)).toBe(4)
    expect(cache(A).phase).toBe('error')
    expect(cache(A).error).toBe('nex_unavailable')
    expect(cache(A).items).toHaveLength(1)

    useExecutionListStore.getState().refetch(A)
    expect(sse.openNexSse).toHaveBeenCalledTimes(2)
    expect(subscriptionSlots.reserve).toHaveBeenCalledTimes(2)
    expect(capFor(A)).toBe(3)
    await flush()
    expect(cache(A).phase).toBe('ready')
    expect(cache(A).error).toBeNull()

    sseFor(A).opts.onStatus('closed', new Error('nex sse: HTTP 401'))
    expect(subscriptionSlots.unreserve).toHaveBeenCalledTimes(2)
    expect(cache(A).error).toBe('nex sse: HTTP 401')

    unsubscribe()
    expect(subscriptionSlots.unreserve).toHaveBeenCalledTimes(2)
    expect(capFor(A)).toBe(4)
  })

  it('a synchronous terminal close from openNexSse leaves no handle, releases the lane and does not fetch', async () => {
    // Mocked: the stream reports `closed` + error before openNexSse returns.
    const closeMock = vi.fn<() => void>()
    vi.mocked(sse.openNexSse).mockImplementationOnce((o) => {
      o.onStatus('closed', new NexApiError(0, 'host_removed', 'host removed'))
      return { close: closeMock }
    })
    useExecutionListStore.getState().subscribe(A)
    expect(sse.openNexSse).toHaveBeenCalledTimes(1)
    expect(api.listExecutions).not.toHaveBeenCalled()
    expect(capFor(A)).toBe(4)
    expect(subscriptionSlots.reserve).toHaveBeenCalledTimes(1)
    expect(subscriptionSlots.unreserve).toHaveBeenCalledTimes(1)
    expect(closeMock).toHaveBeenCalledTimes(1)
    expect(cache(A).phase).toBe('error')
    expect(cache(A).error).toBe('host_removed')

    // No handle was kept: refetch re-opens (not just re-fetches) and re-reserves.
    useExecutionListStore.getState().refetch(A)
    expect(sse.openNexSse).toHaveBeenCalledTimes(2)
    expect(subscriptionSlots.reserve).toHaveBeenCalledTimes(2)
    expect(capFor(A)).toBe(3)
    expect(api.listExecutions).toHaveBeenCalledTimes(1)
    await flush()
    expect(cache(A).phase).toBe('ready')
    expect(cache(A).error).toBeNull()
  })

  it('the real openNexSse closing synchronously for a removed host leaves no handle, releases the lane and does not fetch', async () => {
    const actual = await vi.importActual<typeof sse>('../lib/nex/nex-sse')
    vi.mocked(sse.openNexSse).mockImplementationOnce(actual.openNexSse)
    // Host gone from the host store (nex info still says ready — the window
    // between removal and the nex-host watcher catching up).
    useHostStore.setState((s) => {
      const hosts = { ...s.hosts }
      delete hosts[A]
      return { hosts }
    })
    useExecutionListStore.getState().subscribe(A)
    expect(sse.openNexSse).toHaveBeenCalledTimes(1)
    expect(api.listExecutions).not.toHaveBeenCalled()
    expect(capFor(A)).toBe(4)
    expect(subscriptionSlots.unreserve).toHaveBeenCalledTimes(1)
    expect(cache(A).phase).toBe('error')
    expect(cache(A).error).toBe('host_removed')

    useExecutionListStore.getState().refetch(A)
    expect(sse.openNexSse).toHaveBeenCalledTimes(2)
    expect(subscriptionSlots.reserve).toHaveBeenCalledTimes(2)
    expect(capFor(A)).toBe(3)
  })

  it('a non-error closed status (our own close) is not treated as terminal', async () => {
    const unsubscribe = useExecutionListStore.getState().subscribe(A)
    await flush()
    const { opts } = sseFor(A)
    unsubscribe()
    opts.onStatus('closed')
    expect(cache(A).phase).toBe('ready')
    expect(cache(A).error).toBeNull()
  })

  it('stale fetch after generation bump is ignored', async () => {
    const first = deferList()
    useExecutionListStore.getState().subscribe(A)
    setNexReady(A, false)
    const second = deferList()
    setNexReady(A, true)
    expect(api.listExecutions).toHaveBeenCalledTimes(2)

    first.resolve(page('stale'))
    await flush()
    expect(cache(A).items).toEqual([])

    second.resolve(page('fresh'))
    await flush()
    expect(cache(A).items.map((r) => r.id)).toEqual(['fresh'])
    expect(cache(A).phase).toBe('ready')
  })

  it('a fetch failure surfaces the error code and keeps the previous rows', async () => {
    useExecutionListStore.getState().subscribe(A)
    await flush()
    const failing = deferList()
    useExecutionListStore.getState().refetch(A)
    failing.reject(new NexApiError(503, 'nex_unavailable', 'down'))
    await flush()
    expect(cache(A).phase).toBe('error')
    expect(cache(A).error).toBe('nex_unavailable')
    expect(cache(A).items).toHaveLength(1)
  })

  it('two hosts maintain independent SSEs, reservations, cursors, debounces, and teardown', async () => {
    const uA = useExecutionListStore.getState().subscribe(A)
    useExecutionListStore.getState().subscribe(B)
    await flush()
    expect(sse.openNexSse).toHaveBeenCalledTimes(2)
    expect(sseFor(A).opts.hostId).toBe(A)
    expect(sseFor(B).opts.hostId).toBe(B)
    expect(capFor(A)).toBe(3)
    expect(capFor(B)).toBe(3)
    expect(listCallsFor(A)).toBe(1)
    expect(listCallsFor(B)).toBe(1)

    sseFor(A).opts.onFrame(frame())
    sseFor(B).opts.onFrame(frame('3'))
    expect(cache(A).lastSeq).toBeNull()
    expect(cache(B).lastSeq).toBe(3)
    expect(sseFor(A).opts.getLastEventId()).toBeNull()
    expect(sseFor(B).opts.getLastEventId()).toBe(3)

    await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS / 2)
    sseFor(B).opts.onFrame(frame())
    await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS / 2)
    expect(listCallsFor(A)).toBe(2)
    expect(listCallsFor(B)).toBe(1)
    await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS / 2)
    expect(listCallsFor(B)).toBe(2)

    uA()
    expect(sseFor(A).close).toHaveBeenCalledTimes(1)
    expect(sseFor(B).close).not.toHaveBeenCalled()
    expect(capFor(A)).toBe(4)
    expect(capFor(B)).toBe(3)
    expect(cache(B).items).toHaveLength(1)
  })

  it('refreshRevision increments per refresh cycle', async () => {
    useExecutionListStore.getState().subscribe(A)
    expect(cache(A).refreshRevision).toBe(0)
    await flush()
    expect(cache(A).refreshRevision).toBe(1)

    sseFor(A).opts.onFrame(frame())
    await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS)
    expect(cache(A).refreshRevision).toBe(2)

    sseFor(A).opts.onStatus('reconnecting')
    sseFor(A).opts.onStatus('open')
    await vi.advanceTimersByTimeAsync(LIST_REFRESH_DEBOUNCE_MS)
    expect(cache(A).refreshRevision).toBe(3)

    useExecutionListStore.getState().refetch(A)
    await flush()
    expect(cache(A).refreshRevision).toBe(4)
  })

  it('a page whose items is not a list commits an empty ready list and warns about the dropped page', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.mocked(api.listExecutions).mockResolvedValueOnce({ items: {} } as unknown as ExecutionsPage)
    useExecutionListStore.getState().subscribe(A)
    await flush()
    expect(cache(A).items).toEqual([])
    expect(cache(A).phase).toBe('ready')
    expect(cache(A).error).toBeNull()
    expect(warn).toHaveBeenCalledTimes(1)
    expect(warn.mock.calls[0]).toEqual([expect.stringContaining('dropped'), expect.objectContaining({ hostId: A, dropped: 1 })])
  })

  it('a row with labels: null is kept with {} and a row without an id is dropped', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.mocked(api.listExecutions).mockResolvedValueOnce({
      items: [{ ...row('exc_ok'), labels: null }, { ...row('exc_noid'), id: undefined }],
      next_cursor: '',
    } as unknown as ExecutionsPage)
    useExecutionListStore.getState().subscribe(A)
    await flush()
    expect(cache(A).phase).toBe('ready')
    expect(cache(A).items.map((r) => r.id)).toEqual(['exc_ok'])
    expect(cache(A).items[0].labels).toEqual({})
    expect(warn).toHaveBeenCalledWith(expect.stringContaining('dropped'), expect.objectContaining({ hostId: A, dropped: 1 }))
  })

  it('refreshRevision also increments when a refresh attempt fails', async () => {
    useExecutionListStore.getState().subscribe(A)
    await flush()
    expect(cache(A).refreshRevision).toBe(1)
    const failing = deferList()
    useExecutionListStore.getState().refetch(A)
    failing.reject(new NexApiError(503, 'nex_unavailable', 'down'))
    await flush()
    expect(cache(A).phase).toBe('error')
    expect(cache(A).refreshRevision).toBe(2)
    expect(cache(A).items).toHaveLength(1)
  })

  it('refetch with no subscribers opens nothing', () => {
    useExecutionListStore.getState().refetch(A)
    expect(sse.openNexSse).not.toHaveBeenCalled()
    expect(api.listExecutions).not.toHaveBeenCalled()
  })
})
