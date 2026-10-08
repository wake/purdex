// spa/src/lib/nex/execution-list-effects.ts — the effectful half of the
// per-host execution list (P-C spec §4.3): subscriber refcount, the one
// site-wide SSE per host, its lane reservation, the debounced refetch and the
// guards that decide whether a list answer may still be committed. Where
// the rendered cache lives is the caller's business (`useExecutionListStore`),
// reached through the small `ListSink`.
import { findSuspects, statusDigest, type Suspect } from './delta-reconcile'
import { commitWalk, coveringPage, normalizeDelta, putOverlay, type Overlay } from './execution-overlay'
import { listAllExecutions, DELTA_PAGE_LIMIT, LIST_MAX_PAGES, LIST_PAGE_LIMIT, type WalkPage } from './list-all-executions'
import { openNexSse, type NexSseHandle, type NexSseStatus } from './nex-sse'
import { fingerprintOf } from './nex-host-effects'
import { subscriptionSlots } from './subscription-slots'
import { NexApiError, type ExecutionSummary } from './types'
import { isNexReady } from '../../components/hosts/nex/nex-ready'
import { useNexHostStore } from '../../stores/useNexHostStore'

/** Trailing debounce applied to an SSE-triggered refetch (spec §4.4.3). */
export const LIST_REFRESH_DEBOUNCE_MS = 500
/** The SPA safety reconcile period in delta mode (#1866 §4.5 item 5). */
export const SAFETY_RECONCILE_MS = 120_000
/** How long a suspect waits for a delta that explains it, once the stream has caught up with its page (§4.5). */
export const SUSPECT_GRACE_MS = 1_500

/**
 * The only kinds the site stream is asked for (`?kind=`): those whose commit
 * can change a list row. This is Nexen's declared durable vocabulary
 * (`execution.EventKinds`, v0.20.0), which covers state, the pending
 * permission, tool/task activity, observers, lease, title and archive. It
 * also includes `result`, whose commit rolls the turn cost up onto the row.
 * Token deltas, snapshots, `lease.renewed` and the other raw provider frames
 * change nothing a row shows. Each of them used to push the trailing debounce
 * back, so a host with one streaming worker never refreshed its list (#1866).
 * `peer_message` (Nexen v0.20.0) opens a peer turn in place of
 * `execution.message_accepted`, so a peer wake must refresh the list too; its
 * site-wide frame is stripped to msg_id / template_version / turn_id and only
 * refreshes — it never reaches a pane's reducer (peer mailbox spec §7).
 * Review this list when re-pinning Nexen.
 */
export const SITE_STREAM_KINDS = [
  'execution.delegated',
  'execution.rejected',
  'execution.running',
  'execution.terminal',
  'execution.interrupted',
  'execution.error',
  'execution.message_accepted',
  // v0.20.0: a peer-created turn publishes this instead of message_accepted.
  'peer_message',
  'execution.interrupt_requested',
  'execution.turn_stalled',
  'execution.turn_orphaned',
  'tool_use',
  'tool_result',
  'task_start',
  'task_end',
  'permission.requested',
  'permission.resolved',
  'execution.observer_attached',
  'execution.observer_detached',
  'execution.credential_repaired',
  'execution.archived',
  'execution.unarchived',
  'execution.terminated',
  'execution.title_changed',
  'lease.acquired',
  'lease.released',
  'result',
] as const

const SITE_STREAM_URL = `/api/nex/v1/events?${SITE_STREAM_KINDS.map((k) => `kind=${encodeURIComponent(k)}`).join('&')}`

/**
 * Frames that must not trigger a refetch even when a server ignores `kind=`.
 * This is a denylist, so a kind nobody listed still refreshes the list.
 */
const NOISE_KINDS: ReadonlySet<string> = new Set([
  'stream_event',
  'stream_snapshot',
  'lease.renewed',
  'assistant',
  'user',
  'system',
  'rate_limit_event',
  'control_request',
  'control_response',
  'control_cancel_request',
])

export type HostListPhase = 'idle' | 'loading' | 'ready' | 'error'

export interface HostListCache {
  items: ExecutionSummary[]
  phase: HostListPhase
  error: string | null
  /** Last durable seq seen on the site stream; replayed as Last-Event-ID. */
  lastSeq: number | null
  /** Bumped on every completed refresh attempt (success or failure); consumers key their own follow-up queries on it. */
  refreshRevision: number
  /** The last successful walk hit the page cap (D9): the newest rows may be missing. An error keeps the previous value with the previous rows. */
  truncated: boolean
  /**
   * The last successful walk answered in FULL: it reached the last page, no row was dropped as malformed, no cursor
   * repeated (so not `truncated` either). Only then does a missing row say the execution is not listed. False until a
   * walk answers. An error keeps the previous value with the previous rows.
   */
  complete: boolean
  /** (Optional so a hand-built cache need not name it; absent reads as {}.) Version of each cached row (#1866 §4.3): the covering page's `ver`, a delta's `ver`, or 0 from a legacy fetch. */
  rowVers?: Record<string, number>
  /**
   * (Optional; absent reads as 0.) Bumped by an archive-membership delta (`execution.archived` / `unarchived` in `cause`, a `null` row, an archived
   * row) and by every committed reconcile; the archived query keys on it (§4.7).
   */
  archivedRevision?: number
  /** (Optional; absent reads as 0.) Safety-reconcile suspects that no delta explained (#1866 §4.5): the SPA side lost an update. Separate from the daemon's counters. */
  spaMismatchTotal?: number
  /** (Optional.) The last committed walk's pages: an id absent from the walk is still floored by its covering page's ver (#1963). */
  walkPages?: WalkPage[]
}

/** Per-host capability (§4.1): `delta` once the host's first hello arrives; sticky until the fingerprint changes. */
export type DeltaCap = 'unknown' | 'delta'

/** `nex.executions.hello` value. */
export interface NexHello { epoch: string; bseq: number }

/** `nex.execution` value (§3.5): `row` is `null` when the execution is gone. */
export interface NexDelta {
  epoch: string
  bseq: number
  id: string
  ver: number
  cause: readonly string[]
  row: ExecutionSummary | null
}

export type HostListCaches = Record<string, HostListCache>

/** Where the caches are kept. `set` receives the current map and returns the next (same reference = no change). */
export interface ListSink {
  get: () => HostListCaches
  set: (update: (byHost: HostListCaches) => HostListCaches) => void
}

interface HostListRuntime {
  subscribers: Set<string>
  generation: number
  sse: NexSseHandle | null
  reserved: boolean
  deltaCap: DeltaCap
  /** The hello's epoch and the last bseq processed; null until a hello. */
  baseline: { epoch: string; last: number } | null
  /** Deltas that arrive while a walk is in flight (§4.3); null between walks. */
  overlay: Overlay | null
  debounce: ReturnType<typeof setTimeout> | null
  fetchToken: number
  /** The 120 s safety reconcile (§4.5); only while a delta host has a subscriber. */
  safetyTimer: ReturnType<typeof setInterval> | null
  /** Differences the last safety reconcile found, waiting for the delta stream to explain them (§8 R3-1). */
  suspects: PendingSuspect[]
}

interface PendingSuspect extends Suspect { timer: ReturnType<typeof setTimeout> | null }

export interface ExecutionListEffects {
  subscribe: (hostId: string) => () => void
  refetch: (hostId: string) => void
  clearHost: (hostId: string) => void
  /** A hello arrived on the host-events stream (§4.1). Not wired into production until PR2b. */
  onHello: (hostId: string, hello: NexHello) => void
  /** A `nex.execution` delta arrived (§4.4). Not wired into production until PR2b. */
  applyDelta: (hostId: string, delta: NexDelta) => void
  open: (hostId: string) => void
  close: (hostId: string, opts: { dropCache: boolean }) => void
  /** Hosts that currently have at least one subscriber. */
  subscribedHosts: () => string[]
  resetForTests: () => void
}

export const emptyListCache = (refreshRevision = 0, archivedRevision = 0): HostListCache =>
  ({ items: [], phase: 'idle', error: null, lastSeq: null, refreshRevision, truncated: false, complete: false, rowVers: {}, archivedRevision })

const errorText = (err: unknown): string =>
  err instanceof NexApiError ? err.code : err instanceof Error ? err.message : String(err)

const infoReady = (hostId: string): boolean =>
  isNexReady(useNexHostStore.getState().byHost[hostId]?.info)

export function createExecutionListEffects(sink: ListSink): ExecutionListEffects {
  // Rendered state and connection ownership are kept apart: the cache is what
  // a subscriber displays and survives close/reopen (instant re-display),
  // while the runtime — subscriber tokens, SSE handle, timers, the lane
  // reservation — is never rendered and must not be reset by a cache reset
  // (a host identity change wipes the rows but the subscribers still want
  // the next daemon).
  const runtimes = new Map<string, HostListRuntime>()
  let nextToken = 1

  const runtimeOf = (hostId: string): HostListRuntime => {
    let rt = runtimes.get(hostId)
    if (!rt) {
      rt = { subscribers: new Set(), generation: 0, sse: null, reserved: false, deltaCap: 'unknown', baseline: null, overlay: null, debounce: null, fetchToken: 0, safetyTimer: null, suspects: [] }
      runtimes.set(hostId, rt)
    }
    return rt
  }

  const patchCache = (hostId: string, update: (cache: HostListCache) => HostListCache) =>
    sink.set((byHost) => {
      const cur = byHost[hostId]
      if (!cur) return byHost
      const next = update(cur)
      return next === cur ? byHost : { ...byHost, [hostId]: next }
    })

  const ensureCache = (hostId: string) =>
    sink.set((byHost) => (byHost[hostId] ? byHost : { ...byHost, [hostId]: emptyListCache() }))

  /** A one-shot walk of the list (§4.2). In `delta` mode it is versioned and deltas that arrive meanwhile are overlaid. */
  function fetchAll(hostId: string, safety = false): void {
    const rt = runtimeOf(hostId)
    const generation = rt.generation
    const token = ++rt.fetchToken
    const fingerprint = fingerprintOf(hostId)
    const delta = rt.deltaCap === 'delta'
    const overlay: Overlay | null = delta ? new Map() : null
    rt.overlay = overlay
    patchCache(hostId, (c) => (c.phase === 'ready' ? c : { ...c, phase: 'loading' }))

    const stillCurrent = () =>
      rt.generation === generation
      && rt.fetchToken === token
      && fingerprintOf(hostId) === fingerprint
      && infoReady(hostId)
      && rt.subscribers.size > 0
    const endWalk = () => { if (rt.fetchToken === token) rt.overlay = null }

    listAllExecutions(hostId, { includeArchived: false, delta }, stillCurrent)
      .then((result) => {
        if (!result || !stillCurrent()) return
        const { items, dropped, truncated, stuck, stuckPage } = result
        if (rt.baseline && result.epoch !== undefined && result.epoch !== rt.baseline.epoch) {
          // Another daemon process than the one whose hello we hold: its vers are not comparable with the deltas
          // we have seen. The hello for the new epoch follows and reconciles (§4.4).
          console.warn('nex-delta: list walk from another epoch discarded', { hostId, walk: result.epoch, baseline: rt.baseline.epoch })
          return
        }
        if (stuck) console.warn('nex: executions cursor repeated', { hostId, page: stuckPage })
        if (truncated) console.warn('nex: executions list truncated', { hostId, pageLimit: delta ? DELTA_PAGE_LIMIT : LIST_PAGE_LIMIT, maxPages: LIST_MAX_PAGES })
        if (dropped > 0) console.warn('nex: executions page dropped malformed row(s)', { hostId, dropped })
        const complete = dropped === 0 && !stuck && !truncated
        const committed = overlay
          ? commitWalk(items, result.pages, overlay)
          : { items, vers: Object.fromEntries(items.map((i) => [i.id, 0])) }
        // The safety reconcile compares the walk with the cache BEFORE the commit repairs it (§4.5).
        const found = safety && delta && result.epoch !== undefined && sink.get()[hostId]
          ? findSuspects(sink.get()[hostId], items, result.pages, overlay)
          : []
        patchCache(hostId, (c) => ({
          ...c, items: committed.items, rowVers: committed.vers, walkPages: result.pages, phase: 'ready', error: null, truncated, complete,
          refreshRevision: c.refreshRevision + 1, archivedRevision: (c.archivedRevision ?? 0) + 1,
        }))
        // A successful safety walk supersedes whatever the previous one left waiting, clean or not (it re-evaluated them).
        if (safety && delta && result.epoch !== undefined) cancelSuspects(rt)
        if (found.length > 0) registerSuspects(hostId, rt, found)
      })
      .catch((err: unknown) => {
        if (!stillCurrent()) return
        patchCache(hostId, (c) => ({ ...c, phase: 'error', error: errorText(err), refreshRevision: c.refreshRevision + 1 }))
      })
      .finally(endWalk)
  }

  function cancelSuspects(rt: HostListRuntime): void {
    for (const s of rt.suspects) if (s.timer) clearTimeout(s.timer)
    rt.suspects = []
  }

  function registerSuspects(hostId: string, rt: HostListRuntime, found: Suspect[]): void {
    // An id the new walk reconsidered replaces its older, still pending suspect.
    const ids = new Set(found.map((f) => f.id))
    rt.suspects = rt.suspects.filter((s) => {
      if (!ids.has(s.id)) return true
      if (s.timer) clearTimeout(s.timer)
      return false
    })
    for (const f of found) rt.suspects.push({ ...f, timer: null })
    armSuspects(hostId, rt)
  }

  /**
   * A suspect is judged only once the stream has delivered every delta up to its page's high-water mark (a late
   * socket write of an older delta must not be counted as a loss, §8 R3-1); then it gets the grace.
   */
  function armSuspects(hostId: string, rt: HostListRuntime): void {
    if (!rt.baseline) return
    for (const s of rt.suspects) {
      if (s.timer || rt.baseline.last < s.H) continue
      s.timer = setTimeout(() => {
        rt.suspects = rt.suspects.filter((x) => x !== s)
        const total = (sink.get()[hostId]?.spaMismatchTotal ?? 0) + 1
        patchCache(hostId, (c) => ({ ...c, spaMismatchTotal: total }))
        console.warn('nex-delta: spa mismatch', { hostId, id: s.id, field: s.field, cached: s.cached, fetched: s.fetched, total })
      }, SUSPECT_GRACE_MS)
    }
  }

  /** A delta that arrived for a pending suspect explains it: newer than the page, or the very state the page listed. */
  function observeDelta(rt: HostListRuntime, d: NexDelta): void {
    if (rt.suspects.length === 0) return
    const digest = statusDigest(normalizeDelta(d.ver, d.row).row)
    rt.suspects = rt.suspects.filter((s) => {
      if (s.id !== d.id) return true
      if (d.ver <= s.V && !(d.bseq <= s.H && digest === s.listDigest)) return true
      if (s.timer) clearTimeout(s.timer)
      return false
    })
  }

  function stopSafety(rt: HostListRuntime): void {
    if (rt.safetyTimer) clearInterval(rt.safetyTimer)
    rt.safetyTimer = null
    cancelSuspects(rt)
  }

  /** The 120 s reconcile: delta mode, a subscriber, a visible document, no walk already in flight (§4.5). */
  function armSafety(hostId: string, rt: HostListRuntime): void {
    if (rt.safetyTimer || rt.deltaCap !== 'delta' || rt.subscribers.size === 0) return
    rt.safetyTimer = setInterval(() => {
      if (rt.deltaCap !== 'delta' || rt.subscribers.size === 0 || !infoReady(hostId)) return
      if (typeof document !== 'undefined' && document.visibilityState !== 'visible') return
      if (rt.overlay) return
      fetchAll(hostId, true)
    }, SAFETY_RECONCILE_MS)
  }

  /** Stop the legacy stream and give its lane back (a hello, or a close). */
  function dropLegacyStream(rt: HostListRuntime, hostId: string): void {
    if (rt.debounce) {
      clearTimeout(rt.debounce)
      rt.debounce = null
    }
    const sse = rt.sse
    rt.sse = null
    sse?.close()
    if (rt.reserved) {
      rt.reserved = false
      subscriptionSlots.unreserve(hostId, 'site-wide')
    }
  }

  function close(hostId: string, { dropCache }: { dropCache: boolean }): void {
    const rt = runtimeOf(hostId)
    rt.generation += 1
    rt.overlay = null
    stopSafety(rt)
    dropLegacyStream(rt, hostId)
    if (dropCache) {
      // Another daemon, or the host is gone: whatever it announced does not carry over (§4.1).
      rt.deltaCap = 'unknown'
      rt.baseline = null
    }
    patchCache(hostId, (c) => {
      if (dropCache) return emptyListCache(c.refreshRevision, c.archivedRevision)
      return c.phase === 'loading' ? { ...c, phase: 'idle' } : c
    })
  }

  function open(hostId: string): void {
    const rt = runtimeOf(hostId)
    ensureCache(hostId)
    if (rt.sse || !infoReady(hostId)) return
    // A delta host has no lane and no stream: the host-events stream carries its changes (§4.1).
    if (rt.deltaCap === 'delta') {
      fetchAll(hostId)
      armSafety(hostId, rt)
    } else if (openLegacyStream(hostId)) fetchAll(hostId)
  }

  /** Reserve the lane and open the site-wide SSE (§4.2). False when the stream is already dead on arrival. */
  function openLegacyStream(hostId: string): boolean {
    const rt = runtimeOf(hostId)
    const generation = rt.generation

    // Reserve before the stream exists: the lane is what keeps a fifth pane
    // SSE from taking the browser connection this stream is about to use.
    rt.reserved = true
    subscriptionSlots.reserve(hostId, 'site-wide')

    const scheduleRefetch = () => {
      if (rt.debounce) clearTimeout(rt.debounce)
      rt.debounce = setTimeout(() => {
        rt.debounce = null
        fetchAll(hostId)
      }, LIST_REFRESH_DEBOUNCE_MS)
    }

    let prevStatus: NexSseStatus | null = null
    const handle = openNexSse({
      hostId,
      url: SITE_STREAM_URL,
      getLastEventId: () => sink.get()[hostId]?.lastSeq ?? null,
      onFrame: (frame) => {
        if (rt.generation !== generation) return
        if (frame.id != null) {
          const seq = Number(frame.id)
          patchCache(hostId, (c) => (c.lastSeq !== null && c.lastSeq >= seq ? c : { ...c, lastSeq: seq }))
        }
        if (!NOISE_KINDS.has(frame.event)) scheduleRefetch()
      },
      onStatus: (status, err) => {
        if (rt.generation !== generation) return
        if (status === 'closed' && err) {
          close(hostId, { dropCache: false })
          patchCache(hostId, (c) => ({ ...c, phase: 'error', error: errorText(err) }))
          return
        }
        if (status === 'open' && prevStatus === 'reconnecting') scheduleRefetch()
        prevStatus = status
      },
    })
    // openNexSse may report a terminal `closed` synchronously, before it
    // returns (nex-sse.ts: a host missing from the host store is refused
    // without awaiting). That ran `close()` above: the generation moved on,
    // the lane is already released and the cache carries the error. Keeping
    // the (already dead) handle would make `refetch` re-fetch instead of
    // reopen and leave nothing to close on unsubscribe; fetching would
    // commit rows for a stream that is not there.
    if (rt.generation !== generation) {
      handle.close()
      return false
    }
    rt.sse = handle
    return true
  }

  const subscribe = (hostId: string): (() => void) => {
    const rt = runtimeOf(hostId)
    const token = `sub-${nextToken++}`
    rt.subscribers.add(token)
    if (rt.subscribers.size === 1) open(hostId)
    else ensureCache(hostId)
    return () => {
      if (!rt.subscribers.delete(token)) return
      if (rt.subscribers.size === 0) close(hostId, { dropCache: false })
    }
  }

  const refetch = (hostId: string): void => {
    const rt = runtimes.get(hostId)
    if (!rt || rt.subscribers.size === 0) return
    if (rt.deltaCap === 'delta' || rt.sse) fetchAll(hostId)
    else open(hostId)
  }

  const clearHost = (hostId: string): void => {
    close(hostId, { dropCache: true })
    sink.set((byHost) => {
      if (!(hostId in byHost)) return byHost
      const next = { ...byHost }
      delete next[hostId]
      return next
    })
  }

  const onHello = (hostId: string, hello: NexHello): void => {
    const rt = runtimeOf(hostId)
    rt.deltaCap = 'delta'
    rt.baseline = { epoch: hello.epoch, last: hello.bseq }
    cancelSuspects(rt) // the reconcile this hello triggers re-evaluates
    dropLegacyStream(rt, hostId)
    // Every hello reconciles when anything is subscribed: deltas may have been missed while disconnected.
    if (rt.subscribers.size > 0 && infoReady(hostId)) fetchAll(hostId)
    armSafety(hostId, rt)
  }

  const applyDelta = (hostId: string, d: NexDelta): void => {
    const rt = runtimes.get(hostId)
    if (!rt || rt.deltaCap !== 'delta' || !rt.baseline) return
    if (d.epoch !== rt.baseline.epoch) return
    if (d.bseq !== rt.baseline.last + 1) {
      rt.baseline.last = d.bseq
      cancelSuspects(rt)
      if (rt.subscribers.size > 0) fetchAll(hostId)
      return
    }
    rt.baseline.last = d.bseq
    observeDelta(rt, d)
    armSuspects(hostId, rt)
    const entry = normalizeDelta(d.ver, d.row, d.bseq)
    if (rt.overlay) putOverlay(rt.overlay, d.id, entry)
    const membership = d.row === null || d.row.archived === true
      || d.cause.includes('execution.archived') || d.cause.includes('execution.unarchived')
    patchCache(hostId, (c) => {
      // A cached row's ver, else the page that read this id's range: a stale upsert must not bring back what the walk omitted (#1963).
      const known = c.rowVers?.[d.id] ?? coveringPage(c.walkPages ?? [], d.id)?.ver
      if (known !== undefined && d.ver <= known) return membership ? { ...c, archivedRevision: (c.archivedRevision ?? 0) + 1 } : c
      const rest = c.items.filter((i) => i.id !== d.id)
      const items = entry.row === null
        ? rest
        : [...rest, entry.row].sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))
      const { [d.id]: _gone, ...vers } = c.rowVers ?? {}
      return {
        ...c, items, rowVers: entry.row === null ? vers : { ...vers, [d.id]: d.ver },
        archivedRevision: membership ? (c.archivedRevision ?? 0) + 1 : c.archivedRevision,
      }
    })
  }

  const subscribedHosts = (): string[] =>
    [...runtimes].filter(([, rt]) => rt.subscribers.size > 0).map(([hostId]) => hostId)

  const resetForTests = (): void => {
    for (const rt of runtimes.values()) {
      if (rt.debounce) clearTimeout(rt.debounce)
      stopSafety(rt)
    }
    runtimes.clear()
    nextToken = 1
  }

  return { subscribe, refetch, clearHost, onHello, applyDelta, open, close, subscribedHosts, resetForTests }
}
