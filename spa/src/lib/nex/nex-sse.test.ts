// @vitest-environment node
// spa/src/lib/nex/nex-sse.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { openNexSse, resolveNexStreamUrl } from './nex-sse'
import type { NexSseFrame } from './sse-parser'

// @vitest-environment node has no `localStorage`, but useHostStore persists
// itself through one (zustand persist -> browserStorage). Minimal in-memory
// polyfill so `reset()`/`addHost()` work here exactly as under jsdom; this
// is glue for the environment, not behaviour under test.
if (typeof globalThis.localStorage === 'undefined') {
  const store = new Map<string, string>()
  globalThis.localStorage = {
    getItem: (k: string) => (store.has(k) ? store.get(k)! : null),
    setItem: (k: string, v: string) => { store.set(k, v) },
    removeItem: (k: string) => { store.delete(k) },
    clear: () => store.clear(),
    key: (i: number) => Array.from(store.keys())[i] ?? null,
    get length() { return store.size },
  } as Storage
}

function streamOf(chunks: string[], opts: { hang?: boolean } = {}): ReadableStream<Uint8Array> {
  const enc = new TextEncoder()
  return new ReadableStream({
    start(ctrl) {
      for (const c of chunks) ctrl.enqueue(enc.encode(c))
      if (!opts.hang) ctrl.close()
    },
  })
}

function sseResponse(chunks: string[], opts?: { hang?: boolean }): Response {
  return new Response(streamOf(chunks, opts), { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
}

describe('nex-sse', () => {
  let hostId: string
  beforeEach(() => {
    useHostStore.getState().reset()
    hostId = useHostStore.getState().addHost({ id: 'host-mlab', name: 'mlab', ip: '100.64.0.2', port: 7860, token: 'tok-1' })
    vi.useFakeTimers()
  })
  afterEach(() => vi.useRealTimers())

  it('resolveNexStreamUrl resolves against the daemon origin without re-prefixing', () => {
    expect(resolveNexStreamUrl(hostId, '/api/nex/v1/events?execution_id=exc_1'))
      .toBe('http://100.64.0.2:7860/api/nex/v1/events?execution_id=exc_1')
    expect(resolveNexStreamUrl(hostId, 'http://other:1/x')).toBe('http://other:1/x')
  })

  it('sends Bearer, X-Pdx-Client, Accept and Last-Event-ID; delivers frames; reports status', async () => {
    const fetchImpl = vi.fn().mockResolvedValueOnce(sseResponse(['id: 3\nevent: assistant\ndata: {"a":1}\n\n', 'event: stream_event\ndata: {}\n\n'], { hang: true }))
    const frames: NexSseFrame[] = []
    const statuses: string[] = []
    const h = openNexSse({
      hostId, url: '/api/nex/v1/events?execution_id=exc_1',
      getLastEventId: () => 2,
      onFrame: (f) => frames.push(f),
      onStatus: (s) => statuses.push(s),
      fetchImpl,
    })
    await vi.advanceTimersByTimeAsync(0)
    const [url, init] = fetchImpl.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/api/nex/v1/events?execution_id=exc_1')
    const hd = new Headers(init.headers)
    expect(hd.get('Authorization')).toBe('Bearer tok-1')
    expect(hd.get('X-Pdx-Client')).toMatch(/^[A-Za-z0-9._-]{1,64}$/)
    expect(hd.get('Accept')).toBe('text/event-stream')
    expect(hd.get('Last-Event-ID')).toBe('2')
    expect(url).not.toContain('ticket')
    await vi.advanceTimersByTimeAsync(0)
    expect(frames).toEqual([
      { id: '3', event: 'assistant', data: '{"a":1}' },
      { id: null, event: 'stream_event', data: '{}' },
    ])
    expect(statuses).toEqual(['connecting', 'open'])
    h.close()
    expect(statuses.at(-1)).toBe('closed')
  })

  it('omits Last-Event-ID when no cursor is known', async () => {
    const fetchImpl = vi.fn().mockResolvedValueOnce(sseResponse([], { hang: true }))
    openNexSse({ hostId, url: '/api/nex/v1/events', getLastEventId: () => null, onFrame: () => {}, onStatus: () => {}, fetchImpl })
    await vi.advanceTimersByTimeAsync(0)
    expect(new Headers(fetchImpl.mock.calls[0][1].headers).has('Last-Event-ID')).toBe(false)
  })

  it('reconnects with the CURRENT cursor and exponential backoff when the stream ends', async () => {
    let cursor = 5
    const fetchImpl = vi.fn()
      .mockResolvedValueOnce(sseResponse(['id: 6\nevent: user\ndata: {}\n\n']))   // ends
      .mockResolvedValueOnce(sseResponse([]))                                          // ends again
      .mockResolvedValueOnce(sseResponse([], { hang: true }))
    const statuses: string[] = []
    openNexSse({
      hostId, url: '/api/nex/v1/events', getLastEventId: () => cursor,
      onFrame: (f) => { if (f.id) cursor = Number(f.id) },
      onStatus: (s) => statuses.push(s),
      fetchImpl, backoff: { initialMs: 1000, maxMs: 30000, jitter: 0, stableMs: 10000 },
    })
    await vi.advanceTimersByTimeAsync(0)
    expect(statuses).toEqual(['connecting', 'open', 'reconnecting'])
    await vi.advanceTimersByTimeAsync(999)
    expect(fetchImpl).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(fetchImpl).toHaveBeenCalledTimes(2)
    expect(new Headers(fetchImpl.mock.calls[1][1].headers).get('Last-Event-ID')).toBe('6')
    await vi.advanceTimersByTimeAsync(0)
    await vi.advanceTimersByTimeAsync(2000)   // second retry after 2 s
    expect(fetchImpl).toHaveBeenCalledTimes(3)
    expect(statuses.at(-1)).toBe('open')
  })

  it('stops on 401/403 with closed + error, retries on 503', async () => {
    const onStatus = vi.fn()
    const fetchImpl = vi.fn().mockResolvedValueOnce(new Response('', { status: 401 }))
    openNexSse({ hostId, url: '/api/nex/v1/events', getLastEventId: () => null, onFrame: () => {}, onStatus, fetchImpl })
    await vi.advanceTimersByTimeAsync(0)
    expect(onStatus).toHaveBeenLastCalledWith('closed', expect.any(Error))
    await vi.advanceTimersByTimeAsync(60000)
    expect(fetchImpl).toHaveBeenCalledTimes(1)

    const fetch503 = vi.fn().mockResolvedValueOnce(new Response('{"code":"draining"}', { status: 503 })).mockResolvedValueOnce(sseResponse([], { hang: true }))
    const st: string[] = []
    openNexSse({ hostId, url: '/api/nex/v1/events', getLastEventId: () => null, onFrame: () => {}, onStatus: (s) => st.push(s), fetchImpl: fetch503, backoff: { jitter: 0 } })
    await vi.advanceTimersByTimeAsync(0)
    expect(st.at(-1)).toBe('reconnecting')
    await vi.advanceTimersByTimeAsync(1000)
    expect(fetch503).toHaveBeenCalledTimes(2)
  })

  it('close() while the reader is blocked cancels it, emits closed once and never reconnects', async () => {
    // A stream that never enqueues and never closes: read() stays pending
    // until cancel() — exactly the shape of an idle live SSE connection.
    const body = new ReadableStream<Uint8Array>({ start() {} })
    const fetchImpl = vi.fn().mockResolvedValueOnce(new Response(body, { status: 200 }))
    const onStatus = vi.fn()
    const h = openNexSse({ hostId, url: '/api/nex/v1/events', getLastEventId: () => null, onFrame: () => {}, onStatus, fetchImpl, backoff: { jitter: 0 } })
    await vi.advanceTimersByTimeAsync(0)
    expect(onStatus).toHaveBeenLastCalledWith('open')
    h.close()
    await vi.advanceTimersByTimeAsync(60000)
    expect(fetchImpl).toHaveBeenCalledTimes(1)
    expect(onStatus.mock.calls.filter((c) => c[0] === 'closed')).toHaveLength(1)
    expect(onStatus.mock.calls.some((c) => c[0] === 'reconnecting')).toBe(false)
  })

  it('close() aborts the fetch, cancels a pending reconnect and is idempotent', async () => {
    const fetchImpl = vi.fn().mockResolvedValueOnce(sseResponse([]))
    const onStatus = vi.fn()
    const h = openNexSse({ hostId, url: '/api/nex/v1/events', getLastEventId: () => null, onFrame: () => {}, onStatus, fetchImpl, backoff: { jitter: 0 } })
    await vi.advanceTimersByTimeAsync(0)
    expect(onStatus).toHaveBeenLastCalledWith('reconnecting')
    h.close()
    h.close()
    await vi.advanceTimersByTimeAsync(60000)
    expect(fetchImpl).toHaveBeenCalledTimes(1)
    expect(onStatus.mock.calls.filter((c) => c[0] === 'closed')).toHaveLength(1)
  })
})
