// spa/src/lib/nex/nex-sse.ts — fetch + ReadableStream SSE client for
// /api/nex/v1/events. A native EventSource is ruled out by the P-A contract
// (§4.3): it cannot send Authorization / X-Pdx-Client / Last-Event-ID
// headers and its auto-reconnect would replay a spent ticket URL. This
// client owns reconnection (backoff) but NOT the cursor: the reducer/store
// advances lastSeq from durable frames and this layer reads it back through
// getLastEventId at every (re)connect, so a cursor advanced by history
// paging while the socket was down is honoured.
import { useHostStore } from '../../stores/useHostStore'
import { hostAuthHeaders } from '../host-api'
import { getNexClientId } from './client-id'
import { SseParser, type NexSseFrame } from './sse-parser'
import { NexApiError, nexErrorFromResponse } from './types'

export type NexSseStatus = 'connecting' | 'open' | 'reconnecting' | 'closed'

export interface NexSseBackoff {
  initialMs: number
  /** Hard cap on the reconnect delay, jitter included — the delay never exceeds this. */
  maxMs: number
  /** 0..1 fraction of the delay added/subtracted at random. */
  jitter: number
  /** A connection open at least this long resets the backoff. */
  stableMs: number
  /**
   * No bytes (frames or `: keepalive` comments) received for this long on an
   * `open` connection → treated as a dead half-open socket (laptop sleep,
   * Tailscale path change never surfaces as a network error) and force a
   * reconnect. The Nexen server writes `: keepalive` every 15 s; default
   * gives it three misses of headroom.
   */
  idleMs: number
}

const DEFAULT_BACKOFF: NexSseBackoff = { initialMs: 1000, maxMs: 30000, jitter: 0.2, stableMs: 10000, idleMs: 45000 }

export interface NexSseOptions {
  hostId: string
  /** Origin-relative (as returned by attach(observe).stream_url) or absolute. */
  url: string
  getLastEventId: () => number | null
  onFrame: (frame: NexSseFrame) => void
  onStatus: (status: NexSseStatus, err?: Error) => void
  fetchImpl?: typeof fetch
  backoff?: Partial<NexSseBackoff>
}

export interface NexSseHandle {
  close(): void
}

/**
 * I1: stream_url already carries /api/nex; resolve it against the daemon
 * origin only. Credentials (Bearer, X-Pdx-Client) go out with this request,
 * so a foreign absolute URL must never survive as the fetch target — an
 * attach(observe) response is server data, and if it were ever wrong or
 * malicious an absolute stream_url could redirect those headers to another
 * origin. Only `pathname + search + hash` of the resolved URL is kept, then
 * rebuilt against the daemon base, so the origin is always the daemon's.
 */
export function resolveNexStreamUrl(hostId: string, url: string): string {
  const base = useHostStore.getState().getDaemonBase(hostId)
  const resolved = new URL(url, base)
  return new URL(resolved.pathname + resolved.search + resolved.hash, base).toString()
}

export function openNexSse(opts: NexSseOptions): NexSseHandle {
  const fetchImpl = opts.fetchImpl ?? fetch
  const backoff: NexSseBackoff = { ...DEFAULT_BACKOFF, ...opts.backoff }

  let closed = false
  let attempt = 0
  let controller: AbortController | null = null
  let reader: ReadableStreamDefaultReader<Uint8Array> | null = null
  let timer: ReturnType<typeof setTimeout> | null = null
  let idleTimer: ReturnType<typeof setTimeout> | null = null

  const clearIdleTimer = () => {
    if (idleTimer) { clearTimeout(idleTimer); idleTimer = null }
  }

  const status = (s: NexSseStatus, err?: Error) => {
    if (closed && s !== 'closed') return
    if (err) opts.onStatus(s, err)
    else opts.onStatus(s)
  }

  const scheduleReconnect = (err?: Error) => {
    if (closed) return
    const exp = Math.min(backoff.maxMs, backoff.initialMs * 2 ** attempt)
    const delta = exp * backoff.jitter * (Math.random() * 2 - 1)
    attempt += 1
    status('reconnecting', err)
    const delay = Math.min(backoff.maxMs, Math.max(0, Math.round(exp + delta)))
    timer = setTimeout(() => { timer = null; void connect() }, delay)
  }

  const connect = async () => {
    if (closed) return
    // resolveNexStreamUrl -> getDaemonBase() silently falls back to the
    // active/first host for an unknown hostId — a host removed before the
    // first connect, or between reconnect attempts, must never ride that
    // fallback to a different daemon (spec §4.3.2 step 5: never fall back
    // to another daemon). Refuse terminally instead, same as 401/403 below.
    if (!useHostStore.getState().hosts[opts.hostId]) {
      closed = true
      status('closed', new NexApiError(0, 'host_removed', 'host removed'))
      return
    }
    const target = resolveNexStreamUrl(opts.hostId, opts.url)
    controller = new AbortController()
    status(attempt === 0 ? 'connecting' : 'reconnecting')
    const headers = new Headers(hostAuthHeaders(opts.hostId))
    headers.set('Accept', 'text/event-stream')
    headers.set('X-Pdx-Client', getNexClientId())
    const last = opts.getLastEventId()
    if (last != null) headers.set('Last-Event-ID', String(last))

    let res: Response
    try {
      res = await fetchImpl(target, { headers, signal: controller.signal, cache: 'no-store' })
    } catch {
      if (closed) return
      scheduleReconnect()
      return
    }
    if (closed) return
    if (res.status === 401 || res.status === 403) {
      closed = true
      status('closed', new Error(`nex sse: HTTP ${res.status}`))
      return
    }
    if (!res.ok) {
      // A structured error is transient (worth retrying) only when it is a
      // 5xx AND the code is 'draining' (daemon shutting down gracefully) or
      // an unstructured http_<status> (a bare 5xx from a restarting daemon
      // behind a proxy — no JSON body to carry a real code). Every other
      // structured code — 401/403 handled above, nex_unavailable,
      // execution_not_found, … — is terminal: retrying would only spin
      // forever against an error that will not change (spec §4.5).
      const err = await nexErrorFromResponse(res)
      if (res.status >= 500 && (err.code === 'draining' || err.code.startsWith('http_'))) {
        scheduleReconnect(err)
      } else {
        closed = true
        status('closed', err)
      }
      return
    }
    if (!res.body) {
      scheduleReconnect()
      return
    }

    status('open')
    const openedAt = Date.now()
    const parser = new SseParser()
    const decoder = new TextDecoder()
    reader = res.body.getReader()
    let loopErr: Error | undefined
    let idleTimedOut = false
    // Resets on every chunk received (comment-only keepalives included — we
    // reset on bytes, not on parsed frames); expiry means the socket went
    // half-open (no error, no more data) and we force a reconnect.
    const resetIdleTimer = () => {
      clearIdleTimer()
      idleTimer = setTimeout(() => {
        idleTimer = null
        idleTimedOut = true
        controller?.abort()
        void reader?.cancel().catch(() => {})
      }, backoff.idleMs)
    }
    resetIdleTimer()
    try {
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        resetIdleTimer()
        for (const frame of parser.push(decoder.decode(value, { stream: true }))) {
          if (closed) return
          opts.onFrame(frame)
        }
      }
    } catch (e) {
      // Either the read itself was aborted/network-failed, or a consumer
      // (onFrame) threw mid-frame — either way the loop exits here without
      // having closed the response body, so it must be aborted below before
      // reconnecting, or the old stream leaks as a second live subscriber
      // under the same X-Pdx-Client once scheduleReconnect() opens a new one.
      loopErr = e instanceof Error ? e : new Error(String(e))
    } finally {
      clearIdleTimer()
      reader = null
    }
    if (closed) return
    // An idle timeout wins over whatever the aborted read did: cancel()
    // resolves the pending read() with `done: true` (no error at all), but
    // an unlocked reader on a real fetch instead rejects with an
    // AbortError from controller.abort() — either way the idle cause is
    // the one worth surfacing to the caller, not the abort mechanics.
    if (idleTimedOut) loopErr = new Error('nex sse: idle timeout')
    if (loopErr) {
      controller?.abort() // no-op if the body already errored/aborted itself
      console.warn('nex sse: stream error, reconnecting', loopErr)
    }
    if (Date.now() - openedAt >= backoff.stableMs) attempt = 0
    scheduleReconnect(loopErr)
  }

  void connect()

  return {
    close() {
      if (closed) return
      closed = true
      if (timer) { clearTimeout(timer); timer = null }
      clearIdleTimer()
      controller?.abort()
      // abort() alone does not wake a read() that is already pending on a
      // locked reader (and test streams may not observe the signal at all);
      // cancel the reader explicitly so the loop exits now, not on the next chunk.
      void reader?.cancel().catch(() => {})
      opts.onStatus('closed')
    },
  }
}
