import { useEffect } from 'react'
import { useAgentStore, type NormalizedEvent } from '../stores/useAgentStore'
import { isAgentVisibleInActiveTab } from '../lib/active-session'
import { splitCompositeKey } from '../lib/composite-key'
import { useI18nStore } from '../stores/useI18nStore'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'
import type { NotificationSettings } from '../stores/useNotificationSettingsStore'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useSessionStore } from '../stores/useSessionStore'
import { buildNotificationContent } from '../lib/notification-content'
import { normalizeEventName } from '../lib/event-name'
import { findPane, findTabAndPaneBySessionCode } from '../lib/pane-tree'
import { usePaneFocusStore } from '../stores/usePaneFocusStore'
import { executionIdOfAgentCode, isExecAgentCode } from '../lib/nex/worker-agent-status'
import { isNonTmuxAgentCode } from '../lib/non-tmux-agent'
import { readWorkerSummary, workerTitleOf } from '../lib/nex/worker-summary'
import { useHostStore } from '../stores/useHostStore'
import { selectSessionTitleSupported, useNexHostStore } from '../stores/useNexHostStore'
import { hostLabel, hostLookOf } from '../lib/host-look'
import { landOnHostsPageIfHidden } from '../lib/shown-hosts'
import { createTab } from '../types/tab'
import { STORAGE_KEYS } from '../lib/storage'

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

export type NotificationAction =
  | { kind: 'open-session'; hostId: string; sessionCode: string }
  | { kind: 'open-host'; hostId: string }
  /** An approval request (lead-team spec §6.3): the dialog is already on screen; the click only focuses the window. */
  | { kind: 'open-approval'; hostId: string }

/** Check if a notification should be dispatched based on broadcast_ts dedup.
 *  New sessions default to Infinity (sentinel), so their first event is recorded
 *  but not dispatched — prevents snapshot flooding on new/restarted clients. */
export function shouldDispatch(sessionCode: string, broadcastTs: number): boolean {
  const data: Record<string, number> = JSON.parse(localStorage.getItem(STORAGE_KEYS.NOTIFICATION_SEEN) || '{}')
  const stored = data[sessionCode] ?? Infinity

  if (broadcastTs <= stored) {
    if (stored === Infinity) {
      data[sessionCode] = broadcastTs
      localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify(data))
    }
    return false
  }

  data[sessionCode] = broadcastTs
  localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify(data))
  return true
}

/** Request ids remembered per key. A worker's requests are serialized, so these are the last ones it asked. */
const SEEN_REQUESTS_CAP = 20

/** The request an event is about: a worker awaiting approval carries its pending request's id (useWorkerAgentProjection). */
function requestIdOf(event: NormalizedEvent | undefined): string | null {
  const id = event?.detail?.request_id
  return typeof id === 'string' && id !== '' ? id : null
}

function readSeenRequests(): Record<string, string[]> {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS) || '{}')
    const out: Record<string, string[]> = {}
    if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return out
    for (const [key, ids] of Object.entries(raw)) {
      if (Array.isArray(ids)) out[key] = ids.filter((id): id is string => typeof id === 'string')
    }
    return out
  } catch {
    return {} // a corrupt map is read as empty
  }
}

/**
 * Persistent dedup for an event about one request — a worker awaiting approval, which notifies like an agent ask once
 * per request (permission channel PC2 as amended 2026-10-07). It goes by the request's id, not by stamp: Nexen
 * serializes a worker's requests but two can still share the millisecond their `since` (the event's stamp) is taken
 * in, and `shouldDispatch` would drop the second. The baseline is an agent ask's:
 * - a key with no entry in either map (never seen on this client) records the id and stays quiet — the Infinity
 *   sentinel of `shouldDispatch`, so a first snapshot never floods;
 * - a known key and an id not seen yet dispatches once — including a request that came while the App was closed;
 * - an id seen before never dispatches again (refetch, reconnect, list row <-> live summary, tab switch, reload).
 * The stamp map still keeps the newest stamp, so the key's other events (Stop, …) keep their `shouldDispatch` dedup.
 */
export function shouldDispatchRequest(ck: string, requestId: string, broadcastTs: number): boolean {
  const seenTs: Record<string, number> = JSON.parse(localStorage.getItem(STORAGE_KEYS.NOTIFICATION_SEEN) || '{}')
  const seenRequests = readSeenRequests()
  const ids = seenRequests[ck]
  const known = seenTs[ck] !== undefined || ids !== undefined
  const fresh = ids === undefined || !ids.includes(requestId)
  if (fresh) {
    seenRequests[ck] = [...(ids ?? []), requestId].slice(-SEEN_REQUESTS_CAP)
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS, JSON.stringify(seenRequests))
  }
  const stored = seenTs[ck]
  if (stored === undefined || broadcastTs > stored) {
    seenTs[ck] = broadcastTs
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify(seenTs))
  }
  return fresh && known
}

/** Steps one millisecond is split into for a request's Electron stamp (`requestBroadcastTs`). */
const REQUEST_STAMP_STEPS = 1024

/** FNV-1a, 32-bit. */
function hash32(s: string): number {
  let h = 0x811c9dc5
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return h >>> 0
}

/**
 * The `broadcastTs` handed to Electron for an event about one request. Electron main drops a number it showed in the
 * last 5 s (`recentBroadcasts` in electron/main.ts, one Set<number> for every window), so two requests stamped in the
 * same millisecond would collide on `since` alone and the second would never show. The request id moves the stamp
 * inside its millisecond by a fixed fraction:
 * - deterministic, so every window computes the same number for the same request and the multi-window dedup holds;
 * - strictly between `since` and `since + 1`, so it never equals an integer stamp another event carries;
 * - on a 1/1024 grid, which a double holds exactly for every stamp below 2^43 ms (year 2248) — a finer fraction would
 *   be rounded at today's epoch-ms magnitudes, possibly onto `since` or `since + 1`.
 * Two ids collide only when their hashes share a step (1 in 1023), on top of having to share the millisecond.
 */
function requestBroadcastTs(since: number, requestId: string): number {
  return since + (1 + (hash32(requestId) % (REQUEST_STAMP_STEPS - 1))) / REQUEST_STAMP_STEPS
}

/** Remove a session's lastSeenTs entry and its seen request ids (called on SessionEnd to prevent
 *  stale timestamps from blocking notifications if the code is reused). */
export function clearSeenTs(sessionCode: string): void {
  const data: Record<string, number> = JSON.parse(localStorage.getItem(STORAGE_KEYS.NOTIFICATION_SEEN) || '{}')
  delete data[sessionCode]
  localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify(data))
  const requests = readSeenRequests()
  if (requests[sessionCode] !== undefined) {
    delete requests[sessionCode]
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS, JSON.stringify(requests))
  }
}

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

export function useNotificationDispatcher(): void {
  useEffect(() => {
    const unsubscribe = useAgentStore.subscribe((state, prevState) => {
      const prevEvents = prevState.lastEvents
      const currentEvents = state.lastEvents

      // Clean up lastSeenTs for sessions that ended (prevents stale ts
      // blocking notifications if the session code is reused).
      for (const key of Object.keys(prevEvents)) {
        if (!currentEvents[key]) {
          clearSeenTs(key)
        }
      }

      for (const [compositeKeyStr, event] of Object.entries(currentEvents)) {
        const prev = prevEvents[compositeKeyStr]
        const requestId = requestIdOf(event)
        // The same stamp is the same event — unless it is about another request (two can share a millisecond).
        if (prev && prev.broadcast_ts === event.broadcast_ts && requestIdOf(prev) === requestId) continue

        const { hostId, sessionCode } = splitCompositeKey(compositeKeyStr)

        // Dedup layer 1: localStorage-based persistent dedup (handles restart/snapshot).
        // New sessions use Infinity sentinel — first event is recorded but not dispatched.
        // An event about one request (a worker awaiting approval) is deduped by its request id instead.
        // Layer 2: shouldNotify checks "a pane of the active tab shows it" + document.hasFocus().
        // Layer 3: Electron main process recentBroadcasts dedup (5s window, multi-window).
        const fresh = requestId !== null
          ? shouldDispatchRequest(compositeKeyStr, requestId, event.broadcast_ts)
          : shouldDispatch(compositeKeyStr, event.broadcast_ts)
        if (!fresh) continue

        const derived = event.status || null
        const tabs = useTabStore.getState().tabs
        // Every pane of every tab, not only a tab's primary pane (#1840): an agent in a secondary pane of a split is
        // in a tab, and is in front of the user when that split tab is the active one.
        const hasTab = findTabAndPaneBySessionCode(tabs, hostId, sessionCode) !== undefined
        const settings = useNotificationSettingsStore.getState().getSettingsForAgent(event.agent_type || '')
        const visibleInActiveTab = isAgentVisibleInActiveTab(hostId, sessionCode)

        // Extract errorString before passing to shouldNotify (spec §4, option A).
        const errorString = String(event.detail?.error ?? '')

        if (!shouldNotify({
          derived,
          eventName: event.raw_event_name,
          compositeKey: compositeKeyStr,
          visibleInActiveTab,
          hasTab,
          settings,
          nonTmux: isNonTmuxAgentCode(sessionCode),
          notificationSilent: event.detail?.notification_silent === true,
          errorString,
        })) continue

        const sessionName = notificationName(hostId, sessionCode)

        const content = buildNotificationContent(event.raw_event_name, (event.detail ?? {}) as Record<string, unknown>, sessionName, useI18nStore.getState().t)
        if (!content) continue

        if (window.electronAPI?.showNotification) {
          window.electronAPI.showNotification({
            title: content.title,
            body: content.body,
            sessionCode,
            eventName: event.raw_event_name,
            broadcastTs: requestId !== null ? requestBroadcastTs(event.broadcast_ts, requestId) : event.broadcast_ts,
            action: { kind: 'open-session', hostId, sessionCode },
          })
        }
      }
    })
    return unsubscribe
  }, [])

  // Electron notification click listener
  useEffect(() => {
    if (!window.electronAPI?.onNotificationClicked) return
    return window.electronAPI.onNotificationClicked((payload) => {
      // Electron main always forwards `action` (carrying hostId). A payload
      // without it has no host to route to, so it is ignored rather than
      // guessed — session codes are not unique across hosts.
      if (!payload.action) return
      if (payload.action.kind === 'open-host') {
        handleNotificationClick({ kind: 'open-host', hostId: payload.action.hostId })
      } else if (payload.action.kind === 'open-approval') {
        handleNotificationClick({ kind: 'open-approval', hostId: payload.action.hostId })
      } else {
        handleNotificationClick({
          kind: 'open-session',
          hostId: payload.action.hostId,
          sessionCode: payload.action.sessionCode ?? payload.sessionCode,
        })
      }
    })
  }, [])

  // L2/L3 connection notifications (daemon refused, tmux down)
  useEffect(() => {
    const prevState: Record<string, { daemon?: string; tmux?: string }> = {}

    const unsubscribe = useHostStore.subscribe((state) => {
      const t = useI18nStore.getState().t

      for (const hostId of state.hostOrder) {
        const rt = state.runtime[hostId]
        const prev = prevState[hostId]
        const hostName = hostLabel(hostId, hostLookOf(hostId, state.hosts))

        // L2: daemon refused (was connected, now refused)
        if (prev?.daemon === 'connected' && rt?.daemonState === 'refused') {
          sendConnectionNotification(
            t('notification.daemon_refused', { name: hostName }),
            { kind: 'open-host', hostId },
          )
        }
        // L3: tmux down (was ok, now unavailable)
        if (prev?.tmux === 'ok' && rt?.tmuxState === 'unavailable') {
          sendConnectionNotification(
            t('notification.tmux_down', { name: hostName }),
            { kind: 'open-host', hostId },
          )
        }

        prevState[hostId] = { daemon: rt?.daemonState, tmux: rt?.tmuxState }
      }
    })
    return unsubscribe
  }, [])
}

/**
 * The notification title for an agent key. A worker (`exec-<id>`) is titled
 * like its tab — same summary source, same title rule (worker-summary.ts) —
 * falling back to the execution id; a tmux session by its name, else its code.
 * The worker's `fromTitle` is read from the pane that shows it, a secondary
 * pane of a split included (#1840), as its status bar does.
 */
function notificationName(hostId: string, sessionCode: string): string {
  const executionId = executionIdOfAgentCode(sessionCode)
  if (executionId !== null) {
    const tabs = useTabStore.getState().tabs
    const hit = findTabAndPaneBySessionCode(tabs, hostId, sessionCode)
    const shown = hit ? findPane(tabs[hit.tabId].layout, hit.paneId)?.content : undefined
    const fromTitle = shown?.kind === 'execution' ? shown.fromTitle : undefined
    const titleSupported = selectSessionTitleSupported(hostId)(useNexHostStore.getState())
    return workerTitleOf({ fromTitle }, readWorkerSummary(hostId, executionId), titleSupported) ?? executionId
  }
  const session = useSessionStore.getState().sessions[hostId]?.find((s) => s.code === sessionCode)
  return session?.name || sessionCode
}

export function handleNotificationClick(action: NotificationAction): void {
  switch (action.kind) {
    case 'open-session': {
      const { hostId, sessionCode } = action
      const tabs = useTabStore.getState().tabs
      // Any pane of any tab, a primary pane preferred (#1840).
      const hit = findTabAndPaneBySessionCode(tabs, hostId, sessionCode)
      const ck = `${hostId}:${sessionCode}`
      const event = useAgentStore.getState().lastEvents[ck]
      const agentSettings = useNotificationSettingsStore.getState().getSettingsForAgent(event?.agent_type || '')

      let handled = false
      if (landOnHostsPageIfHidden(hostId)) {
        // Host ownership H2d-3: the notification of a host hidden in this workbench still fired; its click lands on
        // the Hosts page — no tab created, and none focused even when a tab of that session exists (its pane is gated).
        handled = true
      } else if (hit) {
        const { tabId, paneId } = hit
        // The pane becomes its tab's most recently focused pane (usePaneFocusStore, rule F) and is asked to take focus,
        // both before the tab is shown. A tab already on screen has no activation, so the one-shot request is what
        // moves the keyboard there (#1840 A1) — the user must not type the reply into the pane they were in. A tab
        // being shown serves the request with its activation's focus (useActivationFocus), so it focuses once.
        usePaneFocusStore.getState().requestFocus(tabId, paneId)
        useTabStore.getState().setActiveTab(tabId)
        const ws = useWorkspaceStore.getState().findWorkspaceByTab(tabId)
        // No workspace = nobody has adopted the tab yet (features/workspace/lib/adopt-standalone.ts waits before
        // it believes that). There is no "Home" view to switch to; like a click on the tab (`handleSelectTab`),
        // the workspace on screen stays.
        if (ws) {
          useWorkspaceStore.getState().setActiveWorkspace(ws.id)
          useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tabId)
        }
        handled = true
      } else if (isNonTmuxAgentCode(sessionCode)) {
        // A session outside tmux has no tab and never gets one (its code is not a tmux code): the click only clears
        // the unread mark and brings the app to the front.
        useAgentStore.getState().markRead(hostId, sessionCode)
        window.electronAPI?.focusMyWindow?.()
      } else if (isExecAgentCode(sessionCode)) {
        // A worker with no open tab: never reopen it as a tmux tab (an exec key is not a tmux code). Nothing to
        // focus, so only the unread mark is cleared (worker-pane theme spec §8.2).
        useAgentStore.getState().markRead(hostId, sessionCode)
      } else if (agentSettings.reopenTabOnClick) {
        const session = useSessionStore.getState().sessions[hostId]?.find(s => s.code === sessionCode)
        const sessionName = session?.name ?? ''
        // Generation from the session payload we are reopening (spec §4.5);
        // '' when the session is not in the cache = unknown, never a match.
        const newTab = createTab({ kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: sessionName, tmuxInstance: session?.tmux_instance ?? '' })
        useTabStore.getState().addTab(newTab)
        useTabStore.getState().setActiveTab(newTab.id)
        useWorkspaceStore.getState().insertTab(newTab.id)
        // `insertTab` with no target always finds or makes a workspace (active → first → Unsorted).
        const ws = useWorkspaceStore.getState().findWorkspaceByTab(newTab.id)
        if (ws) useWorkspaceStore.getState().setActiveWorkspace(ws.id)
        handled = true
      }

      if (handled) {
        useAgentStore.getState().markRead(hostId, sessionCode)
      }
      if (handled && window.electronAPI?.focusMyWindow) {
        window.electronAPI.focusMyWindow()
      }
      break
    }
    case 'open-host': {
      useTabStore.getState().openSingletonTab({ kind: 'hosts' })
      useHostStore.getState().setActiveHost(action.hostId)
      if (window.electronAPI?.focusMyWindow) {
        window.electronAPI.focusMyWindow()
      }
      break
    }
    case 'open-approval': {
      // The dialog is global and already shows the oldest open request; there is no tab to open or host to switch.
      if (window.electronAPI?.focusMyWindow) {
        window.electronAPI.focusMyWindow()
      }
      break
    }
  }
}

function sendConnectionNotification(message: string, action: NotificationAction): void {
  if (window.electronAPI?.showNotification) {
    window.electronAPI.showNotification({
      title: message,
      body: '',
      sessionCode: '',
      eventName: 'ConnectionStatus',
      broadcastTs: Date.now(),
      action: action.kind === 'open-session'
        ? { kind: 'open-session', hostId: action.hostId, sessionCode: action.sessionCode }
        : { kind: action.kind, hostId: action.hostId },
    })
  }
}
