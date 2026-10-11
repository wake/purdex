// spa/src/lib/launch-latch.ts — a launch click is latched per (host, session code, tmux instance) from the click until the
// daemon's claim would have expired (spec §2.3: 15 s), so a second click cannot post again before the agent's hooks switch the
// view. Module level on purpose: the pane unmounts on a tab switch (useTabAlivePool) and a new panel must see the latch.
export const LAUNCH_CLAIM_MS = 15_000

const expiry = new Map<string, number>()
const listeners = new Set<() => void>()
const notify = () => listeners.forEach((l) => l())

export const latchKey = (hostId: string, code: string, tmuxInstance: string): string => `${hostId}\0${code}\0${tmuxInstance}`

/** Click started: latched until `settled` or `release`. */
export function latchStart(key: string): void {
  expiry.set(key, Infinity)
  notify()
}

/** POST succeeded: keep the latch for the claim lifetime. */
export function latchSettled(key: string, now: number = Date.now()): void {
  expiry.set(key, now + LAUNCH_CLAIM_MS)
  notify()
}

/** POST failed (any error): free it immediately. */
export function latchRelease(key: string): void {
  expiry.delete(key)
  notify()
}

/** Milliseconds the key stays latched: 0 when free, Infinity while the POST is in flight. */
export function latchRemaining(key: string, now: number = Date.now()): number {
  const e = expiry.get(key)
  if (e === undefined) return 0
  if (e <= now) { expiry.delete(key); return 0 }
  return e - now
}

/** Called whenever any latch changes; returns the unsubscribe. */
export function subscribeLatch(fn: () => void): () => void {
  listeners.add(fn)
  return () => { listeners.delete(fn) }
}

/** Test helper. */
export function resetLaunchLatch(): void {
  expiry.clear()
}
