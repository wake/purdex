// spa/src/lib/nex/subscription-slots.ts — per-host LRU of live execution
// subscriptions (spec §4.3.2 step 4). The pdx daemon is HTTP/1.1; browsers
// and Electron allow 6 connections per host:port, and every live SSE holds
// one for its whole life. Capping live streams at 4 keeps two lanes free for
// REST (renew/send/interrupt) so the lease heartbeat can never starve.
// A host's site-wide executions stream (spec §4.3 connection budget) can
// reserve one of those 4 lanes, dropping the pane cap to 3 while it is held.
export const MAX_LIVE_SUBSCRIPTIONS_PER_HOST = 4

export type ReservedLane = 'site-wide'

export interface SlotRegistry {
  /** Claim/refresh a slot for key; returns the keys that must PAUSE (evicted LRU), if any. */
  touch(hostId: string, key: string): string[]
  /**
   * Claim a slot for key only if one is free (size < cap) — never evicts.
   * Refreshes recency if key already holds a slot. Returns whether key
   * holds a slot afterward. Lets an inactive-at-mount pane go live when
   * capacity allows (spec §4.3.2 step 4: only eviction pauses; an idle cap
   * should not).
   */
  claimIfFree(hostId: string, key: string): boolean
  /** Release a slot (pane closed / host removed). */
  release(hostId: string, key: string): void
  /** Subscribe to eviction notices for key; returns unsubscribe. */
  onEvict(key: string, cb: () => void): () => void
  /** Is key currently holding a live slot? */
  isLive(hostId: string, key: string): boolean
  /**
   * Hold the host's reserved lane: the pane cap drops by one and the LRU
   * eviction runs immediately. Idempotent per (hostId, tag). Only
   * 'site-wide' exists; any other tag throws.
   */
  reserve(hostId: string, tag: ReservedLane): void
  /** Give the lane back (no-op when not held); the cap returns to the full budget. */
  unreserve(hostId: string, tag: ReservedLane): void
  resetForTests(): void
}

// Insertion-ordered: first entry is the least recently touched.
const live = new Map<string, Set<string>>()
const listeners = new Map<string, Set<() => void>>()
const reserved = new Set<string>()

const set = (hostId: string) => { let s = live.get(hostId); if (!s) { s = new Set(); live.set(hostId, s) } return s }

const assertLane = (tag: ReservedLane) => {
  if (tag !== 'site-wide') throw new Error(`subscriptionSlots: unknown reserved lane "${String(tag)}"`)
}

/** Live pane cap for hostId right now: the full budget, minus one while the site-wide lane is held. */
export function capFor(hostId: string): number {
  return MAX_LIVE_SUBSCRIPTIONS_PER_HOST - (reserved.has(hostId) ? 1 : 0)
}

function evictToCap(hostId: string): string[] {
  const s = set(hostId)
  const cap = capFor(hostId)
  const evicted: string[] = []
  while (s.size > cap) {
    const oldest = s.values().next().value as string
    s.delete(oldest)
    evicted.push(oldest)
    listeners.get(oldest)?.forEach((cb) => cb())
  }
  return evicted
}

function create(): SlotRegistry {
  return {
    touch(hostId, key) {
      const s = set(hostId)
      s.delete(key); s.add(key)
      return evictToCap(hostId)
    },
    claimIfFree(hostId, key) {
      const s = set(hostId)
      if (s.has(key)) { s.delete(key); s.add(key); return true }
      if (s.size >= capFor(hostId)) return false
      s.add(key)
      return true
    },
    release(hostId, key) { live.get(hostId)?.delete(key) },
    onEvict(key, cb) {
      let l = listeners.get(key); if (!l) { l = new Set(); listeners.set(key, l) }
      l.add(cb)
      return () => { l!.delete(cb); if (l!.size === 0) listeners.delete(key) }
    },
    isLive(hostId, key) { return live.get(hostId)?.has(key) ?? false },
    reserve(hostId, tag) {
      assertLane(tag)
      reserved.add(hostId)
      evictToCap(hostId)
    },
    unreserve(hostId, tag) {
      assertLane(tag)
      reserved.delete(hostId)
    },
    resetForTests() { live.clear(); listeners.clear(); reserved.clear() },
  }
}

export const subscriptionSlots: SlotRegistry = create()
