// spa/src/hooks/useAgentNotifications.ts — agent-store events → desktop notifications (persistent dedup, gate,
// content); split from useNotificationDispatcher (#1690).
import { useEffect } from 'react'
import { isProxyStop, useAgentStore, type NormalizedEvent } from '../stores/useAgentStore'
import { isAgentVisibleInActiveTab } from '../lib/active-session'
import { splitCompositeKey } from '../lib/composite-key'
import { useI18nStore } from '../stores/useI18nStore'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'
import { buildNotificationContent } from '../lib/notification-content'
import { findPane, findTabAndPaneBySessionCode } from '../lib/pane-tree'
import { executionIdOfAgentCode } from '../lib/nex/worker-agent-status'
import { readWorkerSummary, workerTitleOf } from '../lib/nex/worker-summary'
import { selectSessionTitleSupported, useNexHostStore } from '../stores/useNexHostStore'
import { clearSeenTs, requestBroadcastTs, requestIdOf, shouldDispatch, shouldDispatchRequest } from '../lib/notification-dedup'
import { shouldNotify } from '../lib/notification-gate'
import { currentMemberKeys } from '../lib/team/member-set'
import { sessionSeatNow } from './useSessionSeat'
import { hostLabel, hostLookOf } from '../lib/host-look'
import { notifyTitle } from '../lib/notify/notify-title'

export function useAgentNotifications(): void {
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
          notificationSilent: event.detail?.notification_silent === true,
          fromProxy: isProxyStop(event),
          errorString,
          // Read from the stores NOW: this subscription is created once, so a set captured at mount would freeze membership.
          isMember: currentMemberKeys().has(compositeKeyStr),
        })) continue

        // The daemon's label (notify-title spec §3.4) titles a tmux agent as the phone push does; a worker, an older daemon or an unlabelled session keeps today's title.
        const labelled = executionIdOfAgentCode(sessionCode) === null && event.notify_label !== undefined
        const sessionName = labelled ? labelledTitle(hostId, sessionCode, event.notify_label!) : notificationName(hostId, sessionCode)

        const content = buildNotificationContent(event.raw_event_name, (event.detail ?? {}) as Record<string, unknown>, sessionName, useI18nStore.getState().t, { finalTitle: labelled })
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
}

/** The title the phone push would carry, with THIS device's name for the host; the tmux name is the daemon's, else the session's own name. */
function labelledTitle(hostId: string, sessionCode: string, label: NonNullable<NormalizedEvent['notify_label']>): string {
  const tmux = label.tmux || useSessionStore.getState().sessions[hostId]?.find((s) => s.code === sessionCode)?.name || sessionCode
  const locale = useI18nStore.getState().activeLocaleId === 'zh-TW' ? 'zh-TW' : 'en'
  return notifyTitle({ ...label, tmux }, hostLabel(hostId, hostLookOf(hostId)), '', locale)
}

/**
 * The notification title for an agent key. A worker (`exec-<id>`) is titled
 * like its tab — same summary source, same title rule (worker-summary.ts) —
 * falling back to the execution id; a tmux session by its name, else its code.
 * The worker's `fromTitle` is read from the pane that shows it, a secondary
 * pane of a split included (#1840), as its status bar does.
 */
export function notificationName(hostId: string, sessionCode: string): string {
  const executionId = executionIdOfAgentCode(sessionCode)
  if (executionId !== null) {
    const tabs = useTabStore.getState().tabs
    const hit = findTabAndPaneBySessionCode(tabs, hostId, sessionCode)
    const shown = hit ? findPane(tabs[hit.tabId].layout, hit.paneId)?.content : undefined
    const fromTitle = shown?.kind === 'execution' ? shown.fromTitle : undefined
    const titleSupported = selectSessionTitleSupported(hostId)(useNexHostStore.getState())
    return workerTitleOf({ fromTitle }, readWorkerSummary(hostId, executionId), titleSupported) ?? executionId
  }
  // a session that is a persona is named by it (spec §9.3); one no store can place yet keeps its tmux name
  const persona = sessionSeatNow(hostId, sessionCode)
  if (persona !== null) return persona.name
  const session = useSessionStore.getState().sessions[hostId]?.find((s) => s.code === sessionCode)
  return session?.name || sessionCode
}
