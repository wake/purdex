// spa/src/lib/notification-gate.ts — whether an agent event becomes a desktop notification (`shouldNotify`) and the
// error-notification debounce it keeps per session; moved from hooks/useNotificationDispatcher.ts (#1690).
import { useAgentStore } from '../stores/useAgentStore'
import { splitCompositeKey } from './composite-key'
import type { NotificationSettings } from '../stores/useNotificationSettingsStore'
import { normalizeEventName } from './event-name'

// ---------------------------------------------------------------------------
// Error notification debounce (spec §4)
// ---------------------------------------------------------------------------

const ERROR_NOTIFY_WINDOW_MS = 60_000

/** Module-level debounce state. Not in Zustand — dispatcher-private ephemeral state. */
const errorDebounceState = new Map<string, { silentUntil: number }>()

// Max debounce entries: prevents unbounded growth with high-cardinality errorStrings.
// Oldest entry (insertion order) is evicted when the cap is reached.
const MAX_DEBOUNCE_ENTRIES = 1000

// Throttle TTL sweep to at most once per ERROR_NOTIFY_WINDOW_MS to avoid O(N) on every event.
let lastSweepAt = 0

/** Build a collision-free debounce key from the 3-tuple that identifies an error bucket. */
export function buildDebounceKey(ck: string, eventName: string, errorString: string): string {
  // Why: JSON.stringify over a fixed-length array escapes separator chars,
  // preventing collisions that a pipe-join scheme would produce.
  return JSON.stringify([ck, eventName, String(errorString ?? '')])
}

/** Testing-only seam: clear module-level Map and sweep timer between tests. */
export function __resetDebounceStateForTests(): void {
  errorDebounceState.clear()
  lastSweepAt = 0
}

/** Iterate debounce Map, parsing each key once and calling cb(parsed, rawKey). */
function forEachDebounceKey(cb: (parsed: [string, string, string], rawKey: string) => void): void {
  for (const rawKey of errorDebounceState.keys()) {
    try {
      const parsed = JSON.parse(rawKey) as [string, string, string]
      cb(parsed, rawKey)
    } catch {
      // Defensive: skip malformed keys (should never happen under normal use)
    }
  }
}

/** Remove all debounce entries for a given compositeKey (hostId:sessionCode). */
function purgeDebounceForCompositeKey(ck: string): void {
  const toDelete: string[] = []
  forEachDebounceKey((parsed, rawKey) => {
    if (parsed[0] === ck) toDelete.push(rawKey)
  })
  for (const k of toDelete) errorDebounceState.delete(k)
}

/** Remove all debounce entries for all sessions under a given hostId. */
function purgeDebounceForHost(hostId: string): void {
  const toDelete: string[] = []
  // Why: use startsWith(hostId + ':') instead of split(':')[0] so hostIds
  // that themselves contain ':' (e.g. "mlab:abc123") match correctly.
  forEachDebounceKey((parsed, rawKey) => {
    if (parsed[0].startsWith(hostId + ':')) toDelete.push(rawKey)
  })
  for (const k of toDelete) errorDebounceState.delete(k)
}

/** Testing-only seam: expose purgeDebounceForHost for unit tests. */
export function __purgeDebounceForHostForTests(hostId: string): void {
  purgeDebounceForHost(hostId)
}

// Subscribe to store changes to clean up debounce state when sessions/hosts are removed.
// Module-level subscription: registered once at import time, no teardown needed for this pattern.
useAgentStore.subscribe((state, prevState) => {
  // Fast-path: skip enumeration entirely when lastEvents reference is unchanged.
  // Why: handleNormalizedEvent triggers multiple setState calls per event; most don't touch lastEvents.
  if (state.lastEvents === prevState.lastEvents) return

  // Detect removed sessions (lastEvents key disappeared)
  for (const key of Object.keys(prevState.lastEvents)) {
    if (!(key in state.lastEvents)) {
      purgeDebounceForCompositeKey(key)
    }
  }
  // Detect removed hosts (any key whose host prefix is gone)
  const prevHostIds = new Set(Object.keys(prevState.lastEvents).map(k => splitCompositeKey(k).hostId))
  const currHostIds = new Set(Object.keys(state.lastEvents).map(k => splitCompositeKey(k).hostId))
  for (const hostId of prevHostIds) {
    if (!currHostIds.has(hostId)) {
      purgeDebounceForHost(hostId)
    }
  }
})

interface ShouldNotifyParams {
  derived: string | null
  eventName: string
  compositeKey: string
  /** Some pane of the active tab shows this agent — its primary pane or any other leaf of a split (#1840). */
  visibleInActiveTab: boolean
  /** Some pane of some tab shows this agent (#1840: any leaf, not only a tab's primary pane). */
  hasTab: boolean
  settings: NotificationSettings
  /** A session that is not tmux-backed (`cc-<id>`): it can never have a Purdex tab, so "no tab" is not a reason to stay quiet. */
  nonTmux?: boolean
  notificationSilent?: boolean
  /** Caller extracts from event.detail?.error before passing in (spec §4, option A). */
  errorString?: string
}

export function shouldNotify(params: ShouldNotifyParams): boolean {
  const { derived, eventName: rawEventName, compositeKey: ck, visibleInActiveTab, hasTab, settings, nonTmux = false, notificationSilent = false, errorString } = params
  // W2 transition: cc broadcasts PdxXxx; legacy literal keys live in shouldNotify
  // suppression checks and NotificationSettings.events. Normalize once at entry.
  const eventName = normalizeEventName(rawEventName)
  if (derived !== 'waiting' && derived !== 'idle' && derived !== 'error') return false
  if (notificationSilent) return false
  // Informational Notification subtypes (idle_prompt, auth_success) derive to 'idle'
  // but should not trigger desktop notifications — consistent with unread marking logic.
  if (derived === 'idle' && eventName === 'Notification') return false
  if (!settings.enabled) return false
  if (settings.events[eventName] === false) return false
  if (!hasTab && !nonTmux && !settings.notifyWithoutTab) return false
  // Only suppress when user is actively looking at this session:
  // both the app window must be focused AND a pane of the active tab must show it.
  if (visibleInActiveTab && document.hasFocus()) return false

  // Trailing-edge sliding debounce for error notifications (spec §4.1–§4.3).
  // Gate is only for derived==='error'; waiting/idle are not affected.
  if (derived === 'error') {
    const key = buildDebounceKey(ck, eventName, errorString ?? '')
    const now = Date.now()

    // TTL self-cleanup: throttled sweep — at most once per ERROR_NOTIFY_WINDOW_MS.
    // Why: avoids O(N) scan on every high-frequency error event.
    if (now - lastSweepAt >= ERROR_NOTIFY_WINDOW_MS) {
      for (const [k, v] of errorDebounceState) {
        if (v.silentUntil < now - 5 * ERROR_NOTIFY_WINDOW_MS) errorDebounceState.delete(k)
      }
      lastSweepAt = now
    }

    const entry = errorDebounceState.get(key)
    if (entry && now < entry.silentUntil) {
      // Within silence window: extend (trailing-edge sliding) and suppress.
      entry.silentUntil = now + ERROR_NOTIFY_WINDOW_MS
      return false
    }
    // First event or window expired: enforce hard cap before inserting new entry.
    if (entry == null && errorDebounceState.size >= MAX_DEBOUNCE_ENTRIES) {
      // Evict oldest entry (Map preserves insertion order).
      errorDebounceState.delete(errorDebounceState.keys().next().value as string)
    }
    errorDebounceState.set(key, { silentUntil: now + ERROR_NOTIFY_WINDOW_MS })
  }

  return true
}
