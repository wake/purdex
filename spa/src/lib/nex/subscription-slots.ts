// spa/src/lib/nex/subscription-slots.ts — per-host LRU of live execution
// subscriptions (spec §4.3.2 step 4). The pdx daemon is HTTP/1.1; browsers
// and Electron allow 6 connections per host:port, and every live SSE holds
// one for its whole life. Capping live streams at 4 keeps two lanes free for
// REST (renew/send/interrupt) so the lease heartbeat can never starve.
export const MAX_LIVE_SUBSCRIPTIONS_PER_HOST = 4

export interface SlotRegistry {
  /** Claim/refresh a slot for key; returns the keys that must PAUSE (evicted LRU), if any. */
  touch(hostId: string, key: string): string[]
  /** Release a slot (pane closed / host removed). */
  release(hostId: string, key: string): void
  /** Subscribe to eviction notices for key; returns unsubscribe. */
  onEvict(key: string, cb: () => void): () => void
  /** Is key currently holding a live slot? */
  isLive(hostId: string, key: string): boolean
  resetForTests(): void
}

function create(): SlotRegistry {
  // Insertion-ordered: first entry is the least recently touched.
  const live = new Map<string, Set<string>>()
  const listeners = new Map<string, Set<() => void>>()
  const set = (hostId: string) => { let s = live.get(hostId); if (!s) { s = new Set(); live.set(hostId, s) } return s }
  return {
    touch(hostId, key) {
      const s = set(hostId)
      s.delete(key); s.add(key)
      const evicted: string[] = []
      while (s.size > MAX_LIVE_SUBSCRIPTIONS_PER_HOST) {
        const oldest = s.values().next().value as string
        s.delete(oldest)
        evicted.push(oldest)
        listeners.get(oldest)?.forEach((cb) => cb())
      }
      return evicted
    },
    release(hostId, key) { live.get(hostId)?.delete(key) },
    onEvict(key, cb) {
      let l = listeners.get(key); if (!l) { l = new Set(); listeners.set(key, l) }
      l.add(cb)
      return () => { l!.delete(cb); if (l!.size === 0) listeners.delete(key) }
    },
    isLive(hostId, key) { return live.get(hostId)?.has(key) ?? false },
    resetForTests() { live.clear(); listeners.clear() },
  }
}

export const subscriptionSlots: SlotRegistry = create()
