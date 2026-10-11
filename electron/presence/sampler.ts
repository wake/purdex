// Presence sampler (PA-2 D2): how long the user has been idle and whether the screen is locked.
//
// Pure logic with injected deps (no `electron` import) so it is unit-testable. Times are on a
// monotonic clock supplied by the caller (`now`), never the wall clock, so a clock change does not
// move the lock age.
//
// Every event and every state read bumps a revision; a read is applied only if no event arrived
// while it was in flight, so an older read never overwrites a newer event.

export type IdleState = 'active' | 'idle' | 'locked' | 'unknown'

/** The subset of Electron's `powerMonitor` the sampler uses. */
export interface PowerMonitorLike {
  getSystemIdleTime(): number
  getSystemIdleState(idleThresholdSeconds: number): IdleState
  on(event: 'lock-screen' | 'unlock-screen' | 'suspend' | 'resume', listener: () => void): unknown
  removeListener(event: 'lock-screen' | 'unlock-screen' | 'suspend' | 'resume', listener: () => void): unknown
}

/** Reasons the sampler asks for an immediate send. */
export type ForceReason = 'back' | 'lock' | 'unlock' | 'resume'

export interface PresenceSnapshot {
  idleMs: number
  locked: boolean
  lockedMs: number
}

export interface SamplerDeps {
  powerMonitor: PowerMonitorLike
  /** Monotonic milliseconds. */
  now: () => number
  setInterval: (cb: () => void, ms: number) => unknown
  clearInterval: (handle: unknown) => void
  onForce: (reason: ForceReason) => void
}

export interface Sampler {
  start(): void
  stop(): void
  /** Idle / lock state aged to `at` (monotonic ms, default now). */
  snapshot(at?: number): PresenceSnapshot
}

export const SAMPLE_INTERVAL_MS = 5000
/** Idle dropping by at least this much between two samples means the user came back. */
export const BACK_DROP_MS = 5000
/** Reported lock age when the real start is unknown (app started / resumed already locked). */
export const UNKNOWN_LOCK_AGE_MS = 60_000

const EVENTS = ['lock-screen', 'unlock-screen', 'suspend', 'resume'] as const

export function createSampler(deps: SamplerDeps): Sampler {
  const pm = deps.powerMonitor
  let running = false
  let timer: unknown = null
  let rev = 0

  let idleMs = 0
  let idleAt = deps.now()
  let locked = false
  /** Monotonic lock start; null with locked=true means the age is unknown. */
  let lockStart: number | null = null

  const force = (reason: ForceReason) => {
    try {
      deps.onForce(reason)
    } catch {
      // a failing consumer must not break sampling
    }
  }

  /** Reads the idle time; `silent` skips the "came back" check (a forced reason covers it). */
  const sampleIdle = (silent: boolean) => {
    let seconds: number
    try {
      seconds = pm.getSystemIdleTime()
    } catch {
      return
    }
    if (typeof seconds !== 'number' || !Number.isFinite(seconds) || seconds < 0) return
    const next = Math.floor(seconds) * 1000
    const dropped = idleMs - next
    idleMs = next
    idleAt = deps.now()
    if (!silent && dropped >= BACK_DROP_MS) force('back')
  }

  /** Reconciles with the system lock state (used at start and on resume). */
  const reconcile = () => {
    const mine = ++rev
    let state: IdleState
    try {
      state = pm.getSystemIdleState(60)
    } catch {
      return
    }
    if (rev !== mine) return // an event landed while the read was in flight: it is newer
    if (state === 'locked') {
      if (!locked) locked = true
      // A lock we cannot date: the monotonic clock may not have counted a sleep, and Electron
      // gives no lock start. Report "already locked a minute" (away at once).
      lockStart = null
    } else if (state === 'active' || state === 'idle') {
      locked = false
      lockStart = null
    }
    // 'unknown' cannot reconcile: keep the current lock state and its start.
  }

  const onLock = () => {
    rev++
    if (!locked) {
      locked = true
      lockStart = deps.now()
    }
    force('lock')
  }
  const onUnlock = () => {
    rev++
    locked = false
    lockStart = null
    sampleIdle(true)
    force('unlock')
  }
  const onSuspend = () => {
    rev++
  }
  const onResume = () => {
    rev++
    reconcile()
    sampleIdle(true)
    force('resume')
  }
  const handlers: Record<(typeof EVENTS)[number], () => void> = {
    'lock-screen': onLock,
    'unlock-screen': onUnlock,
    suspend: onSuspend,
    resume: onResume,
  }

  return {
    start() {
      if (running) return
      running = true
      // Subscribe first, then read the current state, so nothing falls in the gap.
      for (const ev of EVENTS) pm.on(ev, handlers[ev])
      reconcile()
      sampleIdle(true)
      timer = deps.setInterval(() => sampleIdle(false), SAMPLE_INTERVAL_MS)
    },
    stop() {
      if (!running) return
      running = false
      for (const ev of EVENTS) pm.removeListener(ev, handlers[ev])
      if (timer !== null) deps.clearInterval(timer)
      timer = null
    },
    snapshot(at = deps.now()) {
      const aged = Math.max(0, at - idleAt)
      let lockedMs = 0
      if (locked) lockedMs = lockStart === null ? UNKNOWN_LOCK_AGE_MS : Math.max(0, at - lockStart)
      return { idleMs: idleMs + aged, locked, lockedMs }
    },
  }
}
