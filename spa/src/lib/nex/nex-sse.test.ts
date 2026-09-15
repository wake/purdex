// @vitest-environment node
// spa/src/lib/nex/nex-sse.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { openNexSse, resolveNexStreamUrl } from './nex-sse'
import { NexApiError } from './types'
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

/** A stream whose chunks/close are driven by the test, to simulate spaced-out keepalives. */
function controllableStream(): { stream: ReadableStream<Uint8Array>; push: (s: string) => void; end: () => void } {
  const enc = new TextEncoder()
  let ctrlRef!: ReadableStreamDefaultController<Uint8Array>
  const stream = new ReadableStream<Uint8Array>({ start(ctrl) { ctrlRef = ctrl } })
  return { stream, push: (s: string) => ctrlRef.enqueue(enc.encode(s)), end: () => ctrlRef.close() }
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
    // A foreign absolute URL must never carry the client to another origin —
    // only its pathname+search+hash survive, rebuilt against the daemon base.
    expect(resolveNexStreamUrl(hostId, 'http://other:1/x?y=1')).toBe('http://100.64.0.2:7860/x?y=1')
  })

  it('openNexSse ignores a foreign absolute url origin and still fetches the daemon with the Bearer (never the foreign origin)', async () => {
    const fetchImpl = vi.fn().mockResolvedValueOnce(sseResponse([], { hang: true }))
    openNexSse({
      hostId, url: 'https://attacker.example/evil?x=1', getLastEventId: () => null,
      onFrame: () => {}, onStatus: () => {}, fetchImpl,
    })
    await vi.advanceTimersByTimeAsync(0)
    const [url, init] = fetchImpl.mock.calls[0]
    expect(url).toBe('http://100.64.0.2:7860/evil?x=1')
    expect(new Headers(init.headers).get('Authorization')).toBe('Bearer tok-1')
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

  it('clamps the jittered backoff delay to maxMs', async () => {
    // initialMs === maxMs === 30000, jitter 0.5, Math.random() stubbed to 1
    // (max positive jitter) => exp + delta = 30000 + 15000 = 45000 without a
    // clamp. The spec caps the delay (jitter included) at maxMs.
    const randomSpy = vi.spyOn(Math, 'random').mockReturnValue(1)
    try {
      const fetchImpl = vi.fn().mockResolvedValueOnce(sseResponse([]))
        .mockResolvedValueOnce(sseResponse([], { hang: true }))
      openNexSse({
        hostId, url: '/api/nex/v1/events', getLastEventId: () => null,
        onFrame: () => {}, onStatus: () => {}, fetchImpl,
        backoff: { initialMs: 30000, maxMs: 30000, jitter: 0.5 },
      })
      await vi.advanceTimersByTimeAsync(0)   // first fetch ends -> reconnect scheduled
      await vi.advanceTimersByTimeAsync(29999)
      expect(fetchImpl).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(1)
      expect(fetchImpl).toHaveBeenCalledTimes(2)
    } finally {
      randomSpy.mockRestore()
    }
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

  it('stops (does not retry) on a terminal structured error like 503 nex_unavailable (spec §4.5)', async () => {
    const onStatus = vi.fn()
    const fetchImpl = vi.fn().mockResolvedValueOnce(new Response('{"code":"nex_unavailable"}', { status: 503 }))
    openNexSse({ hostId, url: '/api/nex/v1/events', getLastEventId: () => null, onFrame: () => {}, onStatus, fetchImpl, backoff: { jitter: 0 } })
    await vi.advanceTimersByTimeAsync(0)
    const [status, err] = onStatus.mock.calls.at(-1)!
    expect(status).toBe('closed')
    expect(err).toBeInstanceOf(NexApiError)
    expect((err as NexApiError).code).toBe('nex_unavailable')
    await vi.advanceTimersByTimeAsync(60000)
    expect(fetchImpl).toHaveBeenCalledTimes(1)
  })

  it('retries on a bare (non-JSON) 5xx — a daemon restarting behind a proxy', async () => {
    const fetchImpl = vi.fn().mockResolvedValueOnce(new Response('', { status: 502 })).mockResolvedValueOnce(sseResponse([], { hang: true }))
    const st: string[] = []
    openNexSse({ hostId, url: '/api/nex/v1/events', getLastEventId: () => null, onFrame: () => {}, onStatus: (s) => st.push(s), fetchImpl, backoff: { jitter: 0 } })
    await vi.advanceTimersByTimeAsync(0)
    expect(st.at(-1)).toBe('reconnecting')
    await vi.advanceTimersByTimeAsync(1000)
    expect(fetchImpl).toHaveBeenCalledTimes(2)
  })

  it('refuses an unknown/removed host without calling fetch, terminal host_removed (never falls back to another daemon)', async () => {
    useHostStore.setState({ hosts: {}, hostOrder: [], activeHostId: null, runtime: {} } as never)
    const onStatus = vi.fn()
    const fetchImpl = vi.fn()
    openNexSse({ hostId, url: '/api/nex/v1/events', getLastEventId: () => null, onFrame: () => {}, onStatus, fetchImpl })
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchImpl).not.toHaveBeenCalled()
    const [status, err] = onStatus.mock.calls.at(-1)!
    expect(status).toBe('closed')
    expect(err).toBeInstanceOf(NexApiError)
    expect((err as NexApiError).code).toBe('host_removed')
    // Terminal: no reconnect attempt should ever call fetch either.
    await vi.advanceTimersByTimeAsync(60000)
    expect(fetchImpl).not.toHaveBeenCalled()
  })

  it('stops on a structured 404 execution_not_found', async () => {
    const onStatus = vi.fn()
    const fetchImpl = vi.fn().mockResolvedValueOnce(new Response('{"code":"execution_not_found"}', { status: 404 }))
    openNexSse({ hostId, url: '/api/nex/v1/events', getLastEventId: () => null, onFrame: () => {}, onStatus, fetchImpl, backoff: { jitter: 0 } })
    await vi.advanceTimersByTimeAsync(0)
    const [status, err] = onStatus.mock.calls.at(-1)!
    expect(status).toBe('closed')
    expect(err).toBeInstanceOf(NexApiError)
    expect((err as NexApiError).code).toBe('execution_not_found')
  })

  it('aborts the live stream and reconnects exactly once when onFrame throws', async () => {
    // A consumer exception (e.g. a store-side JSON.parse on a malformed
    // frame) must not leave the errored response's fetch un-aborted: the
    // next connect() would otherwise open a second live subscriber under
    // the same X-Pdx-Client while the first stream is still held open.
    const fetchImpl = vi.fn()
      .mockResolvedValueOnce(sseResponse(['id: 1\nevent: user\ndata: {}\n\n'], { hang: true }))
      .mockResolvedValueOnce(sseResponse([], { hang: true }))
    const statuses: Array<[string, Error | undefined]> = []
    openNexSse({
      hostId, url: '/api/nex/v1/events', getLastEventId: () => null,
      onFrame: () => { throw new Error('boom') },
      onStatus: (s, err) => statuses.push([s, err]),
      fetchImpl, backoff: { initialMs: 1000, jitter: 0 },
    })
    await vi.advanceTimersByTimeAsync(0)
    await vi.advanceTimersByTimeAsync(0)
    expect(fetchImpl.mock.calls[0][1].signal.aborted).toBe(true)
    expect(statuses.filter(([s]) => s === 'reconnecting')).toHaveLength(1)
    const [, err] = statuses.at(-1)!
    expect(err).toBeInstanceOf(Error)
    await vi.advanceTimersByTimeAsync(1000)
    expect(fetchImpl).toHaveBeenCalledTimes(2)
  })

  it('aborts and reconnects after idleMs of silence (a half-open connection past the keepalive interval)', async () => {
    // The first chunk arrives, then nothing — a half-open TCP connection
    // after laptop sleep / path change: reader.read() would otherwise wait
    // forever and the pane would sit at 'open' indefinitely.
    const fetchImpl = vi.fn()
      .mockResolvedValueOnce(sseResponse(['id: 1\nevent: user\ndata: {}\n\n'], { hang: true }))
      .mockResolvedValueOnce(sseResponse([], { hang: true }))
    const statuses: Array<[string, Error | undefined]> = []
    openNexSse({
      hostId, url: '/api/nex/v1/events', getLastEventId: () => null,
      onFrame: () => {},
      onStatus: (s, err) => statuses.push([s, err]),
      fetchImpl, backoff: { initialMs: 1000, jitter: 0, idleMs: 5000 },
    })
    await vi.advanceTimersByTimeAsync(0) // connect, open, read the one chunk
    expect(fetchImpl.mock.calls[0][1].signal.aborted).toBe(false)
    await vi.advanceTimersByTimeAsync(4999)
    expect(fetchImpl.mock.calls[0][1].signal.aborted).toBe(false)
    await vi.advanceTimersByTimeAsync(1)
    expect(fetchImpl.mock.calls[0][1].signal.aborted).toBe(true)
    const reconnecting = statuses.filter(([s]) => s === 'reconnecting')
    expect(reconnecting).toHaveLength(1)
    const [, err] = reconnecting[0]
    expect(err).toBeInstanceOf(Error)
    expect(err?.message).toMatch(/idle/)
    await vi.advanceTimersByTimeAsync(1000) // reconnect backoff
    expect(fetchImpl).toHaveBeenCalledTimes(2)
  })

  it('surfaces the idle-timeout error even when the aborted read rejects with AbortError', async () => {
    // With a real fetch, controller.abort() makes the pending reader.read()
    // reject with an AbortError before the idle-timeout branch runs, so the
    // idle cause must win unconditionally over whatever the aborted read
    // threw — otherwise onStatus reports the generic AbortError and the
    // 'idle' message is lost.
    const fetchImpl = vi.fn()
      .mockImplementationOnce((_url: string, init: RequestInit) => {
        const signal = init.signal!
        const stream = new ReadableStream<Uint8Array>({
          start(ctrl) {
            signal.addEventListener('abort', () => {
              ctrl.error(new DOMException('aborted', 'AbortError'))
            })
          },
        })
        return Promise.resolve(new Response(stream, { status: 200 }))
      })
      .mockResolvedValueOnce(sseResponse([], { hang: true }))
    const statuses: Array<[string, Error | undefined]> = []
    openNexSse({
      hostId, url: '/api/nex/v1/events', getLastEventId: () => null,
      onFrame: () => {},
      onStatus: (s, err) => statuses.push([s, err]),
      fetchImpl, backoff: { initialMs: 1000, jitter: 0, idleMs: 5000 },
    })
    await vi.advanceTimersByTimeAsync(0) // connect, open
    await vi.advanceTimersByTimeAsync(5000) // idle timer fires -> abort -> read() rejects with AbortError
    await vi.advanceTimersByTimeAsync(0) // let the rejected read() propagate
    const reconnecting = statuses.filter(([s]) => s === 'reconnecting')
    expect(reconnecting).toHaveLength(1)
    const [, err] = reconnecting[0]
    expect(err).toBeInstanceOf(Error)
    expect(err?.message).toMatch(/idle/)
  })

  it('resets the idle timer on every chunk, so periodic keepalives keep the connection open', async () => {
    const { stream, push } = controllableStream()
    const fetchImpl = vi.fn().mockResolvedValueOnce(new Response(stream, { status: 200 }))
    const onStatus = vi.fn()
    const h = openNexSse({
      hostId, url: '/api/nex/v1/events', getLastEventId: () => null, onFrame: () => {}, onStatus,
      fetchImpl, backoff: { jitter: 0, idleMs: 5000 },
    })
    await vi.advanceTimersByTimeAsync(0)
    for (let i = 0; i < 3; i++) {
      push(': keepalive\n\n')
      await vi.advanceTimersByTimeAsync(4000) // idleMs - 1000, repeated past idleMs total
    }
    expect(fetchImpl.mock.calls[0][1].signal.aborted).toBe(false)
    expect(onStatus.mock.calls.some((c) => c[0] === 'reconnecting')).toBe(false)
    h.close()
  })

  it('resets backoff to initialMs after a connection stayed open past stableMs, even under idle keepalives', async () => {
    const { stream, push, end } = controllableStream()
    const fetchImpl = vi.fn()
      .mockResolvedValueOnce(sseResponse([]))                       // ends immediately -> attempt 0->1
      .mockResolvedValueOnce(new Response(stream, { status: 200 })) // long stable connection
      .mockResolvedValueOnce(sseResponse([], { hang: true }))
    const statuses: string[] = []
    openNexSse({
      hostId, url: '/api/nex/v1/events', getLastEventId: () => null,
      onFrame: () => {}, onStatus: (s) => statuses.push(s), fetchImpl,
      backoff: { initialMs: 1000, maxMs: 30000, jitter: 0, stableMs: 10000, idleMs: 60000 },
    })
    await vi.advanceTimersByTimeAsync(0)    // fetch #1 ends -> reconnect scheduled at 1000ms (attempt was 0)
    await vi.advanceTimersByTimeAsync(1000) // fetch #2 connects and opens
    expect(fetchImpl).toHaveBeenCalledTimes(2)
    for (let i = 0; i < 3; i++) {
      push(': keepalive\n\n')
      await vi.advanceTimersByTimeAsync(4000) // 12s total, past stableMs (10s)
    }
    end()
    await vi.advanceTimersByTimeAsync(0) // stream ends -> attempt reset to 0 (stayed open >= stableMs)
    expect(fetchImpl).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(999)
    expect(fetchImpl).toHaveBeenCalledTimes(2) // if attempt had NOT reset, next backoff would be 2000ms
    await vi.advanceTimersByTimeAsync(1)
    expect(fetchImpl).toHaveBeenCalledTimes(3)
    expect(statuses.at(-1)).toBe('open')
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
