// spa/src/lib/pool/jobs-driver.ts — what drives the jobs store (plan "WWD-7 design", Data flow: Jobs). The store reads and keeps; this decides when and through whom:
//   - an origin is read from its own daemon when the App is connected to one (only it lists fast-lane and ad-hoc jobs), else through a forwarding daemon with `?host=` — `jobSourceFor`, then the other forwarders as the next sources;
//   - a read that used up its pages goes on at once (`more`); a failure is acted on by the action of its error (errors.ts): the next source at once, the same one again after 1–2 s, back-off 2, 4, 8 … 30 s, or the origin dropped and read again after a back-off;
//   - `jobs.changed` re-reads that one job (a job that cannot be read is read again as a whole list); a host-events connection that opened reads after the cursor the origins its daemon answers for;
//   - an origin no daemon of the App's announces is polled every 10 s while a view watches and the window is visible (turning visible reads at once).
// It owns its timers and clears them when nobody watches. Dependencies are handed in so it can be driven without the stores (jobs-driver-live.ts hands in the real ones).
import { backoffMs, busyDelayMs, type PoolError } from './errors'
import { jobSourceFor, type JobSource, type ReadOutcome } from './jobs-store'

export const POLL_MS = 10_000

export interface JobsTopology {
  /** The pool's hosts, by alias: every origin that has jobs. */
  origins: string[]
  /** Origin alias → the App host (connected, announcing `jobs.v1`) that is that host's own daemon. */
  ownDaemon: Record<string, string>
  /** The connected App hosts announcing `jobs.v1`, in host order: they answer for any origin with `?host=`. */
  forwarders: string[]
}

export interface JobsStoreLike {
  read: (origin: string, source: JobSource) => Promise<ReadOutcome>
  readJob: (origin: string, source: JobSource, id: string) => Promise<ReadOutcome>
  drop: (origin: string) => void
}

export interface JobsDriverDeps {
  store: JobsStoreLike
  topology: () => JobsTopology
  visible: () => boolean
  /** Math.random(). */
  rand: () => number
  /** The store holds this origin (it has been read). */
  known: (origin: string) => boolean
}

interface OriginState {
  /** How many sources were tried and failed with `next_source` since the last good read. */
  skip: number
  failures: number
  /** The best source for the origin when it was last read: a topology that changes it makes the origin read again, through the new one. */
  primary: string
  /** A failure nobody retried (no view was watching), or one that gives up: the next `sync` reads the origin again. */
  dirty: boolean
  /** A `sync` came while a read was out: when it ends, the origin is looked at again. */
  resync: boolean
  timer: ReturnType<typeof setTimeout> | null
  /** When `timer` fires (ms): a later request for a later time keeps the earlier one. */
  due: number
  running: boolean
}

/** The sources that can answer for an origin, best first: its own daemon, then each forwarding daemon (not that same daemon again). */
export function candidates(origin: string, topo: JobsTopology): JobSource[] {
  const out: JobSource[] = []
  const own = jobSourceFor(origin, topo.ownDaemon, null)
  if (own) out.push(own)
  for (const f of topo.forwarders) {
    const s = jobSourceFor(origin, {}, f)
    if (s && !out.some((o) => o.daemonId === s.daemonId)) out.push(s)
  }
  return out
}

export function createJobsDriver(deps: JobsDriverDeps) {
  const states = new Map<string, OriginState>()
  let watchers = 0
  /** Bumped by `stop`: a release handed out before it is of no use any more. */
  let epoch = 0
  let poll: ReturnType<typeof setInterval> | null = null

  const stateOf = (origin: string): OriginState => {
    let s = states.get(origin)
    if (!s) { s = { skip: 0, failures: 0, primary: '', dirty: false, resync: false, timer: null, due: 0, running: false }; states.set(origin, s) }
    return s
  }

  function schedule(origin: string, ms: number) {
    const st = stateOf(origin)
    if (watchers === 0) { st.dirty = true; return } // nobody watches: not retried on its own; the next view (or event, or reconnect) reads it again
    const due = Date.now() + ms
    if (st.timer !== null) {
      if (st.due <= due) return
      clearTimeout(st.timer)
    }
    st.due = due
    st.timer = setTimeout(() => { st.timer = null; void refresh(origin) }, ms)
  }

  /** What to do about a failure: go on at once with the next source (`true`), or it has been scheduled / given up on (`false`). */
  function failed(origin: string, st: OriginState, error: PoolError, tried: number): boolean {
    switch (error.action) {
      case 'next_source':
        if (st.skip + 1 < tried) { st.skip += 1; return true }
        st.skip = 0 // every source failed: the whole round again after a back-off
        schedule(origin, backoffMs(st.failures++))
        return false
      case 'retry_same':
        schedule(origin, busyDelayMs(deps.rand()))
        return false
      case 'backoff':
        schedule(origin, backoffMs(st.failures++))
        return false
      case 'reset_origin':
        deps.store.drop(origin)
        schedule(origin, backoffMs(st.failures++))
        return false
      default:
        stateOf(origin).dirty = true
        return false // none, log, read_only …: nothing to try differently; the next sync, event, reconnect or poll reads again
    }
  }

  const keyOf = (s: JobSource | undefined): string => (s ? `${s.daemonId}\u0000${s.host ?? ''}` : '')

  async function refresh(origin: string): Promise<void> {
    const st = stateOf(origin)
    if (st.running) return // the read in flight is after the same cursor
    if (st.timer !== null) { clearTimeout(st.timer); st.timer = null }
    st.running = true
    st.dirty = false
    try {
      for (;;) {
        const cands = candidates(origin, deps.topology())
        if (cands.length === 0) return
        st.primary = keyOf(cands[0])
        const out = await deps.store.read(origin, cands[Math.min(st.skip, cands.length - 1)])
        if (out.ok) {
          st.skip = 0
          st.failures = 0
          if (out.more) continue
          return
        }
        if (!failed(origin, st, out.error, cands.length)) return
      }
    } finally {
      st.running = false
      if (st.resync) { st.resync = false; syncOrigin(origin, deps.topology()) }
    }
  }

  async function jobChanged(origin: string, id: string): Promise<void> {
    const st = stateOf(origin)
    const cands = candidates(origin, deps.topology())
    if (cands.length === 0) return
    st.primary = keyOf(cands[0])
    const out = await deps.store.readJob(origin, cands[Math.min(st.skip, cands.length - 1)], id)
    if (out.ok) {
      if (out.more) void refresh(origin)
      return
    }
    // the one job could not be had: the whole list is read again, through the next source or after the wait
    if (failed(origin, st, out.error, cands.length)) void refresh(origin)
  }

  const primaryOf = (origin: string): string | undefined => candidates(origin, deps.topology())[0]?.daemonId

  /** A host-events connection opened or came back: the origins its daemon answers for are read after their cursor. */
  function hostOpened(hostId: string) {
    for (const origin of deps.topology().origins) if (primaryOf(origin) === hostId) void refresh(origin)
  }

  /**
   * One origin looked at again: read if the store has not got it, if a failure left it dirty, or if it was read through a source that is no longer the best one (the store then starts it over: a cursor is its source's own).
   * While a read is out the look waits for it to end.
   */
  function syncOrigin(origin: string, topo: JobsTopology) {
    const st = stateOf(origin)
    if (st.running) { st.resync = true; return }
    if (!deps.known(origin)) {
      if (st.timer === null) void refresh(origin)
      return
    }
    if (st.dirty) {
      if (st.timer === null) void refresh(origin)
      return
    }
    const best = keyOf(candidates(origin, topo)[0])
    if (best === '') return
    if (st.primary === '') st.primary = best // read by someone else (the store has it): this is the source to compare against from now on
    else if (best !== st.primary) void refresh(origin)
  }

  /** Reads what is known to exist and has not been read, what a failure left behind, and what was read through a source that is no longer the best one. */
  function sync() {
    const topo = deps.topology()
    for (const origin of topo.origins) syncOrigin(origin, topo)
  }

  function pollOnce() {
    if (!deps.visible()) return
    const topo = deps.topology()
    for (const origin of topo.origins) if (!Object.hasOwn(topo.ownDaemon, origin)) void refresh(origin)
  }

  const onVisibility = () => { if (deps.visible()) pollOnce() }

  /** A view that shows jobs is mounted: sync now, and poll the origins no daemon announces while it lasts. The returned function releases it (once). */
  function watch(): () => void {
    watchers += 1
    if (watchers === 1) {
      poll = setInterval(pollOnce, POLL_MS)
      if (typeof document !== 'undefined') document.addEventListener('visibilitychange', onVisibility)
    }
    sync()
    let released = false
    const mine = epoch
    return () => {
      if (released || mine !== epoch) return
      released = true
      watchers -= 1
      if (watchers === 0) stop()
    }
  }

  function stopPolling() {
    if (poll !== null) { clearInterval(poll); poll = null }
    if (typeof document !== 'undefined') document.removeEventListener('visibilitychange', onVisibility)
  }

  /** Clears every timer (the App is closing, or a test ends). */
  function stop() {
    epoch += 1
    watchers = 0
    stopPolling()
    for (const st of states.values()) {
      st.resync = false // a look that waited for a read to end is cancelled with the rest
      if (st.timer !== null) { clearTimeout(st.timer); st.timer = null; st.dirty = true } // what the retry would have read is read by the next view
    }
  }

  return { jobChanged, hostOpened, sync, watch, stop }
}

export type JobsDriver = ReturnType<typeof createJobsDriver>
