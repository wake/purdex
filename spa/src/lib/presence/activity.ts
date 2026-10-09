// spa/src/lib/presence/activity.ts — is the user at this window? (push spec §5.4: `active` = the window has focus AND
// saw user input — key, pointer, wheel — in the last 120 s.) Plan PU-4 Task 1.

/** How long after the last input the user still counts as at the window. */
export const ACTIVE_WINDOW_MS = 120_000
/** pointermove fires constantly: one counted per this much time is plenty against a 120 s window. */
const MOVE_THROTTLE_MS = 1000

export interface ActivityTracker {
  /** Focused and input within the window. False before any input. */
  isActive(now?: number): boolean
  lastInputAt(): number
  /** Called with the new answer when input ends an idle spell, or focus is gained or lost. Returns the unsubscribe. */
  subscribe(fn: (active: boolean) => void): () => void
  dispose(): void
}

export interface ActivityOptions {
  target?: Window
  now?: () => number
  windowMs?: number
}

export function createActivityTracker(opts: ActivityOptions = {}): ActivityTracker {
  const target = opts.target ?? window
  const now = opts.now ?? (() => Date.now())
  const windowMs = opts.windowMs ?? ACTIVE_WINDOW_MS
  let last = Number.NEGATIVE_INFINITY
  let lastMove = Number.NEGATIVE_INFINITY
  let disposed = false
  const subs = new Set<(active: boolean) => void>()

  const active = (at: number): boolean => at - last <= windowMs && document.hasFocus()
  let reported = false
  const notify = () => {
    const a = active(now())
    if (a === reported) return
    reported = a
    for (const fn of [...subs]) fn(a)
  }

  const onInput = () => {
    if (disposed) return
    last = now()
    notify()
  }
  const onMove = () => {
    if (disposed) return
    const t = now()
    if (t - lastMove < MOVE_THROTTLE_MS) return
    lastMove = t
    onInput()
  }
  const onFocusChange = () => { if (!disposed) notify() }

  const inputs: Array<[string, EventListener]> = [
    ['keydown', onInput], ['pointerdown', onInput], ['wheel', onInput], ['pointermove', onMove],
    ['focus', onFocusChange], ['blur', onFocusChange],
  ]
  for (const [name, fn] of inputs) target.addEventListener(name, fn, { capture: true, passive: true })

  return {
    isActive: (at = now()) => !disposed && active(at),
    lastInputAt: () => last,
    subscribe(fn) {
      subs.add(fn)
      return () => { subs.delete(fn) }
    },
    dispose() {
      disposed = true
      for (const [name, fn] of inputs) target.removeEventListener(name, fn, { capture: true })
      subs.clear()
    },
  }
}
