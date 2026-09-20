// spa/src/lib/storage/world-lock.ts — ONE world-moving block at a time, across
// every renderer of this client (Profile Sync P3b).
//
// WHY. The epoch fence (world-fence.ts) is raised by read-check-write on
// `localStorage`, which has no compare-and-swap: window A reads the fence, window
// B raises a higher one and starts writing, A writes its LOWER epoch over it —
// and from then on the store writes of both pass the fence and land interleaved.
// Web Locks is a real mutex between renderers (the browser process arbitrates,
// and a holder that dies releases), so a switch / promote runs its synchronous
// block under the exclusive lock `purdex-world-switch`. The second one enters
// only when the first has left, reads what the first wrote — and is then an
// ordinary late-comer to the fence's rules (`behind-fence`: refused, catches up).
//
// THE BLOCK STAYS SYNCHRONOUS. `body` returns a value, not a promise: nothing may
// yield between the three store writes of a switch (lib/profile/switch-active.ts),
// lock or no lock. The lock only decides WHEN the block starts.
//
// BOUNDED. A renderer stuck inside its block (a debugger, a frozen tab) would
// make every other window's switch wait for ever: the request is aborted after
// `WORLD_LOCK_TIMEOUT_MS` and the caller answers `busy`. An AbortController and a
// `setTimeout`, not `AbortSignal.timeout` — fake timers cannot advance that one
// (lib/profile/api.ts measured it). A block that has started is never "timed
// out": it is synchronous, so the timer cannot fire inside it, an abort does not
// take a granted lock back, and `busy` is only ever said of a request that was
// NOT granted. No timer outlives the call (THE IRON RULE, lib/profile/start.ts: a user
// who never switches never gets here at all).
//
// WITHOUT WEB LOCKS — `navigator.locks` needs a secure context, so a dev build
// served over plain http on a tailnet IP has none (the same trap as
// `crypto.subtle`); the packaged app and https do — `body` runs inside the call,
// exactly as it did before this file existed, and the read-check-write window of
// the fence stays open: see world-fence.ts, WHAT IS LEFT.
export const WORLD_LOCK_NAME = 'purdex-world-switch'
export const WORLD_LOCK_TIMEOUT_MS = 3_000

/** The part of `LockManager` this file uses (lib.dom's is not in every TS target this repo builds with). */
interface Locks {
  request<T>(name: string, options: { mode: 'exclusive'; signal: AbortSignal }, callback: () => T): Promise<T>
}

function webLocks(): Locks | undefined {
  return typeof navigator === 'undefined' ? undefined : (navigator as unknown as { locks?: Locks }).locks
}

/**
 * Runs `body` — synchronous, see the header — under the cross-renderer lock, or
 * right away where there is none. `busy()` is the answer when the lock was not
 * granted in time; `body` has then not run and never will. What `body` throws
 * rejects the promise, with the lock released.
 */
export function withWorldLock<T>(body: () => T, busy: () => T): Promise<T> {
  const locks = webLocks()
  if (locks === undefined) {
    // Not `async`: `body` must have run by the time this function RETURNS, as it always did.
    try {
      return Promise.resolve(body())
    } catch (err) {
      return Promise.reject(err)
    }
  }

  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), WORLD_LOCK_TIMEOUT_MS)
  let granted = false
  return locks
    .request(WORLD_LOCK_NAME, { mode: 'exclusive', signal: controller.signal }, () => {
      granted = true
      return body()
    })
    .catch((err: unknown) => {
      if (!granted && controller.signal.aborted) return busy()
      throw err
    })
    .finally(() => clearTimeout(timer))
}
