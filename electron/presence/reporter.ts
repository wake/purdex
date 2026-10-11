// Main-process presence reporter: the send loop (PA-2 D1, D5, D10).
//
// One reporter per App, independent of windows. The host list, per-host identity and the capability
// probe live in `hosts.ts`; this module sends v2 presence to the hosts that announce
// `push.presence.v2` and keeps the owned set. Pure logic with injected deps (no `electron` import).
//
// Secrets: see hosts.ts. Log lines here are fixed templates plus the host id and a status number;
// caught errors are never interpolated.
import {
  createHosts,
  createSafeLog,
  headersFor,
  withDeadline,
  type FetchInit,
  type HostEntry,
  type HostsPayload,
  type HostState,
  type ResponseLike,
  type Settled,
} from './hosts'
import type { ForceReason, PresenceSnapshot } from './sampler'

export type { FetchInit, HostEntry, HostsPayload, ResponseLike }
export type SendReason = ForceReason | 'n' | 'hosts' | 'capability' | 'recovered' | 'tick' | 'coalesced'

export interface ReporterDeps {
  fetch: (url: string, init: FetchInit) => Promise<ResponseLike>
  /** Monotonic milliseconds. */
  now: () => number
  setTimeout: (cb: () => void, ms: number) => unknown
  clearTimeout: (h: unknown) => void
  setInterval: (cb: () => void, ms: number) => unknown
  clearInterval: (h: unknown) => void
  snapshot: (at: number) => PresenceSnapshot
  getAwayMinutes: () => number
  getInstallId: () => string
  logger: { warn: (message: string) => void }
  onOwnedChanged: (hostIds: string[]) => void
}
export interface Reporter {
  start(): void
  stop(): void
  /** 'ignored' when the rev is older than the one held. */
  setHosts(payload: HostsPayload): 'applied' | 'ignored'
  force(reason: SendReason): void
  getOwned(): string[]
}

export const SEND_INTERVAL_MS = 20_000
export const REQUEST_DEADLINE_MS = 10_000
export const TTL_MS = 45_000
export const LOG_THROTTLE_MS = 60_000
const MAX_MS = 2_592_000_000
const MIN_AWAY_MS = 60_000
const MAX_AWAY_MS = 3_600_000
const LOCKED_AWAY_MS = 60_000

/** Per-host send bookkeeping, kept in `HostState.slot`. */
interface SendSlot {
  inflight: boolean
  pending: boolean
  sendAbort: AbortController | null
  lastOkAt: number | null
  ownedTimer: unknown
  lastRejectLogAt: number | null
  sendFailLogged: boolean
}
type Host = HostState<SendSlot>

export function createReporter(deps: ReporterDeps): Reporter {
  let tickTimer: unknown = null
  let lastOwnedKey = '[]'
  let installIdLogged = false
  const log = createSafeLog(deps.logger)

  // ---- owned set (D10) ----
  const isOwned = (st: Host) =>
    !st.disposed && st.caps === 'v2' && st.slot.lastOkAt !== null && deps.now() - st.slot.lastOkAt < TTL_MS
  const ownedIds = () => hosts.list().filter(isOwned).map((s) => s.hostId).sort()
  const updateOwned = () => {
    const ids = ownedIds()
    const key = JSON.stringify(ids)
    if (key === lastOwnedKey) return
    lastOwnedKey = key
    try {
      deps.onOwnedChanged(ids.slice())
    } catch {
      // ignore a failing consumer
    }
  }
  const armExpiry = (st: Host) => {
    if (st.slot.ownedTimer !== null) deps.clearTimeout(st.slot.ownedTimer)
    st.slot.ownedTimer = deps.setTimeout(() => updateOwned(), TTL_MS)
  }
  const clearOk = (st: Host) => {
    st.slot.lastOkAt = null
    if (st.slot.ownedTimer !== null) deps.clearTimeout(st.slot.ownedTimer)
    st.slot.ownedTimer = null
  }

  // ---- sending (D5) ----
  const clampMs = (n: number, max: number) => Math.min(max, Math.max(0, Math.floor(Number.isFinite(n) ? n : 0)))

  const buildBody = (clientId: string): Record<string, unknown> => {
    const snap = deps.snapshot(deps.now())
    const idleMs = clampMs(snap.idleMs, MAX_MS)
    const lockedMs = snap.locked ? clampMs(snap.lockedMs, MAX_MS) : 0
    const awayMs = Math.min(MAX_AWAY_MS, Math.max(MIN_AWAY_MS, Math.floor(deps.getAwayMinutes() * 60_000)))
    const active = snap.locked ? lockedMs < LOCKED_AWAY_MS : idleMs < awayMs
    return {
      client_id: clientId,
      active,
      sessions: [],
      ttl_ms: TTL_MS,
      v: 2,
      idle_ms: idleMs,
      locked: snap.locked,
      locked_ms: lockedMs,
      away_after_ms: awayMs,
    }
  }

  const markSendFailed = (st: Host) => {
    hosts.markFailed(st)
    clearOk(st)
    updateOwned()
    void hosts.probe(st)
  }

  const send = (st: Host, reason: SendReason): void => {
    if (!hosts.isRunning() || st.disposed || st.caps !== 'v2') return
    if (st.slot.inflight) {
      // Only a forced reason (unlock / back / N change / ...) is worth a follow-up; the 20 s tick is not.
      if (reason !== 'tick') st.slot.pending = true
      return
    }
    let clientId: string
    try {
      clientId = `mac:${deps.getInstallId()}`
    } catch {
      if (!installIdLogged) {
        installIdLogged = true
        log('presence: install id unavailable, not sending')
      }
      return
    }
    st.slot.inflight = true
    st.slot.pending = false
    const ac = new AbortController()
    st.slot.sendAbort = ac
    const body = JSON.stringify(buildBody(clientId))
    void withDeadline(
      deps,
      async (signal) => {
        const resp = await deps.fetch(`${st.origin}/api/push/presence`, {
          method: 'PUT',
          headers: headersFor(st, true),
          body,
          signal,
        })
        try {
          void resp.body?.cancel?.()
        } catch {
          // ignore
        }
        return resp.status
      },
      REQUEST_DEADLINE_MS,
      ac,
    ).then((r) => settle(st, r))
  }

  const settle = (st: Host, r: Settled<number>) => {
    // Identity guard: a removed or re-pointed host's completion is ignored and never resent.
    if (st.disposed || !hosts.isRunning()) return
    st.slot.inflight = false
    st.slot.sendAbort = null
    // A follow-up is sent only for a forced reason that arrived while the request was in flight, once,
    // with the latest snapshot. A failure or timeout alone never chains another request.
    const resend = st.slot.pending
    if (r.ok && r.value >= 200 && r.value < 300) {
      hosts.markRecovered(st)
      st.slot.sendFailLogged = false
      st.slot.lastOkAt = deps.now()
      armExpiry(st)
      updateOwned()
    } else if (r.ok && r.value >= 400 && r.value < 500) {
      clearOk(st)
      updateOwned()
      const at = deps.now()
      if (st.slot.lastRejectLogAt === null || at - st.slot.lastRejectLogAt >= LOG_THROTTLE_MS) {
        st.slot.lastRejectLogAt = at
        log(`presence: send rejected status=${r.value} ${hosts.label(st)}`)
      }
      // The same body is never retried at once; a forced reason that arrived meanwhile is still sent.
    } else {
      if (!st.slot.sendFailLogged) {
        st.slot.sendFailLogged = true
        log(
          r.ok
            ? `presence: send failed status=${r.value} ${hosts.label(st)}`
            : `presence: send failed (${r.timedOut ? 'timeout' : 'network'}) ${hosts.label(st)}`,
        )
      }
      markSendFailed(st)
    }
    st.slot.pending = false
    if (resend) send(st, 'coalesced')
  }

  const forceAll = (reason: SendReason) => {
    for (const st of hosts.list()) send(st, reason)
  }

  const hosts = createHosts<SendSlot>(deps, {
    newSlot: () => ({
      inflight: false,
      pending: false,
      sendAbort: null,
      lastOkAt: null,
      ownedTimer: null,
      lastRejectLogAt: null,
      sendFailLogged: false,
    }),
    onDispose(st) {
      if (st.slot.ownedTimer !== null) deps.clearTimeout(st.slot.ownedTimer)
      st.slot.ownedTimer = null
      try {
        st.slot.sendAbort?.abort()
      } catch {
        // ignore
      }
    },
    onCapable: (st, reason) => send(st, reason),
    onIncapable(st) {
      clearOk(st)
      updateOwned()
    },
    onListChanged: updateOwned,
    onListReady: () => forceAll('hosts'),
    onStopped: updateOwned,
  })

  return {
    start() {
      if (hosts.isRunning()) return
      hosts.start()
      tickTimer = deps.setInterval(() => forceAll('tick'), SEND_INTERVAL_MS)
    },
    stop() {
      if (!hosts.isRunning()) return
      if (tickTimer !== null) deps.clearInterval(tickTimer)
      tickTimer = null
      hosts.stop()
    },
    setHosts: (payload) => hosts.setHosts(payload),
    force: (reason) => forceAll(reason),
    getOwned: () => ownedIds(),
  }
}
