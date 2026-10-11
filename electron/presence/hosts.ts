// Host list and capability probe of the presence reporter (PA-2 D3, D3b, D4).
//
// Holds the host list handed over by the renderer (highest `rev` wins, equal replaces), the identity
// `(origin, token)` of every host, and asks each host which capabilities it announces. What happens
// once a host is known to be capable (sending, the owned set) is the caller's: it plugs in through
// `HostsHooks` and keeps its own per-host data in `HostState.slot`.
//
// Secrets: a host's token lives only in memory. It is used for the Authorization header and nowhere
// else — never in a log line, an error message, or anything handed to a renderer. Log lines are fixed
// templates plus an internal ordinal (`host#N`); renderer-supplied values (hostId, origin) and caught
// errors are never interpolated.

export interface HostEntry {
  hostId: string
  /** Origin only (scheme://host[:port]); validated by ipc.ts. */
  baseUrl: string
  token: string | null
}
export interface HostsPayload {
  rev: number
  hosts: HostEntry[]
}

export interface ResponseLike {
  ok: boolean
  status: number
  json(): Promise<unknown>
  body?: { cancel?: () => unknown } | null
}
export interface FetchInit {
  method?: string
  headers: Record<string, string>
  body?: string
  signal: AbortSignal
}
export interface HostsDeps {
  fetch: (url: string, init: FetchInit) => Promise<ResponseLike>
  setTimeout: (cb: () => void, ms: number) => unknown
  clearTimeout: (h: unknown) => void
  logger: { warn: (message: string) => void }
}

export const PROBE_DEADLINE_MS = 5_000
export const PROBE_RETRY_MS = 20_000
export const PROBE_REFRESH_MS = 10 * 60_000
export const CAPABILITY = 'push.presence.v2'

export interface HostState<S> {
  hostId: string
  origin: string
  token: string | null
  caps: 'unknown' | 'v2' | 'no'
  disposed: boolean
  probing: boolean
  probeTimer: unknown
  probeAbort: AbortController | null
  /** A send or probe failed and no send has succeeded since. */
  failed: boolean
  /** The recovery send for the current failure streak was already made. */
  recoverySent: boolean
  probeFailLogged: boolean
  /** The caller's own per-host data. */
  slot: S
}

export interface HostsHooks<S> {
  newSlot(entry: HostEntry): S
  /** The host is being dropped (removed, re-pointed, or the reporter stops). */
  onDispose(st: HostState<S>): void
  /** The host announces the capability: first time ('capability') or after a failure ('recovered'). */
  onCapable(st: HostState<S>, reason: 'capability' | 'recovered'): void
  /** The host answered and no longer announces the capability. */
  onIncapable(st: HostState<S>): void
  /** The list changed (membership or identity). Called before the fresh hosts are probed. */
  onListChanged(): void
  /** The list changed while running; the fresh hosts have been probed. */
  onListReady(): void
  /** stop() disposed everything. */
  onStopped(): void
}

export interface Hosts<S> {
  start(): void
  stop(): void
  isRunning(): boolean
  /** 'ignored' when the rev is older than the one held. */
  setHosts(payload: HostsPayload): 'applied' | 'ignored'
  list(): HostState<S>[]
  /** Log-safe name of a held host: its ordinal in the list, never a renderer-supplied value. */
  label(st: HostState<S>): string
  probe(st: HostState<S>): Promise<void>
  /** A send failed (the caller clears its own state and probes next). */
  markFailed(st: HostState<S>): void
  /** A send succeeded. */
  markRecovered(st: HostState<S>): void
}

export type Settled<T> = { ok: true; value: T } | { ok: false; timedOut: boolean }

export function headersFor(st: { token: string | null }, json: boolean): Record<string, string> {
  const h: Record<string, string> = {}
  if (json) h['Content-Type'] = 'application/json'
  if (st.token) h.Authorization = `Bearer ${st.token}`
  return h
}

/** Runs `work` under a deadline; on timeout aborts and settles without waiting for `work`. */
export function withDeadline<T>(
  deps: Pick<HostsDeps, 'setTimeout' | 'clearTimeout'>,
  work: (signal: AbortSignal) => Promise<T>,
  ms: number,
  ac: AbortController,
): Promise<Settled<T>> {
  return new Promise((resolve) => {
    let done = false
    const finish = (r: Settled<T>) => {
      if (done) return
      done = true
      deps.clearTimeout(timer)
      resolve(r)
    }
    const timer = deps.setTimeout(() => {
      try {
        ac.abort()
      } catch {
        // ignore
      }
      finish({ ok: false, timedOut: true })
    }, ms)
    let p: Promise<T>
    try {
      p = work(ac.signal)
    } catch {
      finish({ ok: false, timedOut: false })
      return
    }
    p.then(
      (value) => finish({ ok: true, value }),
      () => finish({ ok: false, timedOut: false }),
    )
  })
}

export function createSafeLog(logger: HostsDeps['logger']): (message: string) => void {
  return (message) => {
    try {
      logger.warn(message)
    } catch {
      // logging must never break reporting
    }
  }
}

export function createHosts<S>(deps: HostsDeps, hooks: HostsHooks<S>): Hosts<S> {
  let running = false
  let hosts = new Map<string, HostState<S>>()
  let heldRev: number | null = null
  const log = createSafeLog(deps.logger)

  const ordinalOf = (st: HostState<S>): number => [...hosts.values()].indexOf(st)

  const dispose = (st: HostState<S>) => {
    st.disposed = true
    if (st.probeTimer !== null) deps.clearTimeout(st.probeTimer)
    st.probeTimer = null
    try {
      st.probeAbort?.abort()
    } catch {
      // ignore
    }
    hooks.onDispose(st)
  }

  const scheduleProbe = (st: HostState<S>, ms: number) => {
    if (st.probeTimer !== null) deps.clearTimeout(st.probeTimer)
    st.probeTimer = deps.setTimeout(() => {
      st.probeTimer = null
      void probe(st)
    }, ms)
  }

  const probe = async (st: HostState<S>): Promise<void> => {
    if (!running || st.disposed || st.probing) return
    st.probing = true
    if (st.probeTimer !== null) {
      deps.clearTimeout(st.probeTimer)
      st.probeTimer = null
    }
    const ac = new AbortController()
    st.probeAbort = ac
    const r = await withDeadline(
      deps,
      async (signal) => {
        const resp = await deps.fetch(`${st.origin}/api/info`, { method: 'GET', headers: headersFor(st, false), signal })
        if (!resp.ok) throw new Error('status')
        const doc = (await resp.json()) as { capabilities?: unknown } | null
        const caps = doc?.capabilities
        if (!Array.isArray(caps)) throw new Error('shape')
        return caps.includes(CAPABILITY)
      },
      PROBE_DEADLINE_MS,
      ac,
    )
    // Identity guard: only the state that started this probe may apply its answer.
    if (st.disposed || !running) return
    st.probing = false
    st.probeAbort = null
    if (!r.ok) {
      if (!st.probeFailLogged) {
        st.probeFailLogged = true
        log(`presence: capability probe failed host#${ordinalOf(st)}`)
      }
      scheduleProbe(st, PROBE_RETRY_MS)
      return
    }
    st.probeFailLogged = false
    const wasV2 = st.caps === 'v2'
    st.caps = r.value ? 'v2' : 'no'
    scheduleProbe(st, PROBE_REFRESH_MS)
    if (!r.value) {
      hooks.onIncapable(st)
      return
    }
    if (!wasV2) {
      hooks.onCapable(st, 'capability')
    } else if (st.failed && !st.recoverySent) {
      st.recoverySent = true
      hooks.onCapable(st, 'recovered')
    }
  }

  const newState = (e: HostEntry): HostState<S> => ({
    hostId: e.hostId,
    origin: e.baseUrl,
    token: e.token,
    caps: 'unknown',
    disposed: false,
    probing: false,
    probeTimer: null,
    probeAbort: null,
    failed: false,
    recoverySent: false,
    probeFailLogged: false,
    slot: hooks.newSlot(e),
  })

  return {
    start() {
      if (running) return
      running = true
      for (const st of hosts.values()) void probe(st)
    },
    stop() {
      if (!running) return
      running = false
      for (const st of hosts.values()) dispose(st)
      hosts = new Map()
      heldRev = null
      hooks.onStopped()
    },
    isRunning: () => running,
    setHosts(payload) {
      if (heldRev !== null && payload.rev < heldRev) return 'ignored'
      heldRev = payload.rev
      const next = new Map<string, HostState<S>>()
      let changed = false
      for (const e of payload.hosts) {
        const prev = hosts.get(e.hostId)
        if (prev && prev.origin === e.baseUrl && prev.token === e.token) {
          next.set(e.hostId, prev)
          continue
        }
        if (prev) dispose(prev)
        next.set(e.hostId, newState(e))
        changed = true
      }
      for (const [id, st] of hosts) {
        if (!next.has(id)) {
          dispose(st)
          changed = true
        }
      }
      const fresh = [...next.values()].filter((s) => s.caps === 'unknown' && !s.probing && s.probeTimer === null)
      hosts = next
      if (changed) {
        hooks.onListChanged()
        if (running) {
          for (const st of fresh) void probe(st)
          hooks.onListReady()
        }
      }
      return 'applied'
    },
    list: () => [...hosts.values()],
    label: (st) => `host#${ordinalOf(st)}`,
    probe,
    markFailed(st) {
      if (!st.failed) st.recoverySent = false
      st.failed = true
    },
    markRecovered(st) {
      st.failed = false
      st.recoverySent = false
    },
  }
}
