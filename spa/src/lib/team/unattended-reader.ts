// spa/src/lib/team/unattended-reader.ts — the one reader of a host's first unattended page (`GET /api/team/unattended`),
// shared by the unattended panel and the team settings (journal v5 spec §3.9). Opening either reads the host; while a read
// of that daemon is out, another one JOINS it instead of asking again (single-flight per host), so two quick opens, or the
// two panels opened together, make one request.
//
// A read: when the host lists `team.relay_quota.v1` the quota store is told a GET is out (beginGet) and gets the rows at
// the end (endGet), so `team.relay_quota` events that arrive meanwhile apply after them; either way the page's quota part
// (or the failure) becomes the host's last read in the same store (`reads`), which both panels draw their rows from.
// It is bounded: a host that has not answered in HOST_READ_TIMEOUT_MS fails as `timeout` and its request is aborted.
//
// Who waits: each caller may pass a signal. Aborting it lets that caller go (its promise rejects `aborted`); the request
// itself is aborted only when every caller has let go, and a given-up read is nobody's failure (nothing is kept). A caller
// without a signal (the team settings: closing it does not cancel the read that fills the store) holds the read to its end.
//
// Re-reads: after a quota write lands on a provisional root (`pending_lineage`) the writer asks for the host again
// (relay-quota-writer.ts `registerRefetch`). While any panel watches the host (`watchHost`), that re-read is a fresh read
// here, whose answer reaches every panel through the store.
import { ApprovalApiError } from './approval-api'
import { getUnattended } from './unattended-api'
import { hostIdentityNow } from './quota-host'
import { useRelayQuotaStore } from './relay-quota'
import { registerRefetch } from './relay-quota-writer'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import type { UnattendedView } from './types'

/** How long one host may take to answer before it is named as failed (`timeout`) instead of holding a panel. */
export const HOST_READ_TIMEOUT_MS = 10_000

interface Flight {
  /** The daemon (endpoint + token) asked. */
  identity: string
  /** The connection generation it was asked in (the quota store's epoch, which a disconnect or reconnect moves). */
  epoch: number
  promise: Promise<UnattendedView>
  /** Callers still waiting; at 0 the request is given up. */
  holders: number
  settled: boolean
  giveUp: () => void
  /** A newer read of the host took its place: its GET bracket closes now, and whenever it answers it writes nothing. */
  replaced: () => void
}

const flights = new Map<string, Flight>()
const watchers = new Map<string, { count: number; dispose: () => void }>()

const codeOf = (e: unknown): string => (e instanceof ApprovalApiError ? e.code : 'error')
const aborted = () => new ApprovalApiError(0, 'aborted')

function start(hostId: string, identity: string): Flight {
  const store = useRelayQuotaStore.getState()
  const quotas = useUnattendedStore.getState().byHost[hostId]?.quotaSupport === 'yes'
  const epoch = quotas ? store.beginGet(hostId) : (store.epochs[hostId] ?? 0)
  const ctl = new AbortController()
  // Only the host's newest read writes (the last read and the rows it confirms): a read that was replaced while out
  // (refreshHost) may still answer after its replacement, and must never overwrite it, success or failure.
  let counts = true
  let bracket = quotas
  const closeBracket = (rows: UnattendedView['quotas'] | null) => {
    if (!bracket) return
    bracket = false
    useRelayQuotaStore.getState().endGet(hostId, rows ?? null, epoch)
  }
  const flight = { identity, epoch, holders: 0, settled: false, giveUp: () => {}, replaced: () => { counts = false; closeBracket(null) } } as Flight
  flight.promise = new Promise<UnattendedView>((resolve, reject) => {
    const timer = setTimeout(() => { finish(null, new ApprovalApiError(0, 'timeout'), true); ctl.abort() }, HOST_READ_TIMEOUT_MS)
    /** The one end of the read; `keep` = a real outcome of the host (not a given-up read) that becomes its last read. */
    const finish = (v: UnattendedView | null, e: unknown, keep: boolean) => {
      if (flight.settled) return
      flight.settled = true
      clearTimeout(timer)
      if (flights.get(hostId) === flight) flights.delete(hostId)
      if (keep && counts) useRelayQuotaStore.getState().setRead(hostId, v !== null ? { ok: true, ...quotaPart(v) } : { ok: false, code: codeOf(e) }, epoch)
      closeBracket(counts ? (v?.quotas ?? null) : null)
      if (v !== null) resolve(v)
      else reject(e)
    }
    flight.giveUp = () => { finish(null, aborted(), false); ctl.abort() }
    try {
      getUnattended(hostId, undefined, ctl.signal).then(
        // `list_failed` is the PUT's flag (the daemon's GET never sets it): if it ever comes, `approved` says nothing.
        (v) => { if (v.list_failed === true) finish(null, new ApprovalApiError(200, 'list_failed'), true); else finish(v, null, true) },
        (e: unknown) => finish(null, e, true),
      )
    } catch (e) {
      finish(null, e, true) // a transport that throws before it returns a promise still ends the read (and its GET bracket)
    }
  })
  flight.promise.catch(() => {}) // a read whose callers all let go (or a re-read nobody awaits) is not an unhandled rejection
  return flight
}

function quotaPart(v: UnattendedView) {
  return {
    ...(v.quotas !== undefined ? { quotas: v.quotas } : {}),
    ...(v.quotasFailed ? { quotasFailed: true } : {}),
    ...(v.held !== undefined ? { held: v.held } : {}),
  }
}

/**
 * The host's read now: the one out to the same daemon over the same connection, else a new one. Null when the host is not
 * configured. A new read REPLACES the one out (a re-point, a new connection generation, or `fresh`): the old one goes on
 * for its own callers, but nobody joins it any more and it writes nothing (the spec's single-flight, with the 10-17
 * ruling: a forced re-read may replace a read in flight, the replaced one never writes). Endpoint and token alone do not
 * name a connection: a reconnect to the same daemon moves the store's epoch (unattended-support.ts `fence`).
 */
function current(hostId: string, fresh: boolean): Flight | null {
  const identity = hostIdentityNow(hostId)
  if (identity === null) return null
  const cur = flights.get(hostId)
  const epoch = useRelayQuotaStore.getState().epochs[hostId] ?? 0
  if (!fresh && cur !== undefined && !cur.settled && cur.identity === identity && cur.epoch === epoch) return cur
  cur?.replaced()
  const next = start(hostId, identity)
  flights.set(hostId, next)
  return next
}

function join(flight: Flight, signal?: AbortSignal): Promise<UnattendedView> {
  flight.holders += 1
  if (signal === undefined) return flight.promise // held to its end
  return new Promise<UnattendedView>((resolve, reject) => {
    const leave = () => {
      reject(aborted())
      flight.holders -= 1
      if (flight.holders === 0 && !flight.settled) flight.giveUp()
    }
    signal.addEventListener('abort', leave, { once: true })
    flight.promise.then(
      (v) => { signal.removeEventListener('abort', leave); resolve(v) },
      (e: unknown) => { signal.removeEventListener('abort', leave); reject(e) },
    )
  })
}

/** The host's first unattended page: joins the read already out to this daemon, else starts one. */
export function readFirstPage(hostId: string, signal?: AbortSignal): Promise<UnattendedView> {
  if (signal?.aborted) return Promise.reject(aborted())
  const flight = current(hostId, false)
  if (flight === null) return Promise.reject(new ApprovalApiError(0, 'no_host'))
  return join(flight, signal)
}

/** A fresh read even while one is out (the writer's re-read): later reads join it. Held to its end. */
export function refreshHost(hostId: string): Promise<UnattendedView> {
  const flight = current(hostId, true)
  if (flight === null) return Promise.reject(new ApprovalApiError(0, 'no_host'))
  return join(flight)
}

/** A panel shows this host's quotas: while any does, the writer's re-read of the host is a fresh read. Returns the disposer. */
export function watchHost(hostId: string): () => void {
  const w = watchers.get(hostId)
  if (w !== undefined) w.count += 1
  else watchers.set(hostId, { count: 1, dispose: registerRefetch(hostId, () => { refreshHost(hostId).catch(() => {}) }) })
  let done = false
  return () => {
    if (done) return
    done = true
    const cur = watchers.get(hostId)
    if (cur === undefined) return
    cur.count -= 1
    if (cur.count === 0) { cur.dispose(); watchers.delete(hostId) }
  }
}

/** Test seam: give up every read and forget every watcher. */
export function resetReader(): void {
  for (const f of flights.values()) f.giveUp()
  flights.clear()
  for (const w of watchers.values()) w.dispose()
  watchers.clear()
}
