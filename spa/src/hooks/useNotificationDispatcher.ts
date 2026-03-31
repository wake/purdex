import { useEffect } from 'react'
import { useAgentStore, deriveStatus } from '../stores/useAgentStore'
import { getActiveSessionCode } from '../lib/active-session'
import { useI18nStore } from '../stores/useI18nStore'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'
import type { NotificationSettings } from '../stores/useNotificationSettingsStore'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useSessionStore } from '../stores/useSessionStore'
import { buildNotificationContent } from '../lib/notification-content'
import { findTabBySessionCode, getPrimaryPane } from '../lib/pane-tree'
import { getPlatformCapabilities } from '../lib/platform'
import { useHostStore } from '../stores/useHostStore'
import { createTab } from '../types/tab'

const SEEN_KEY = 'tbox-notification-seen'

/** Check if a notification should be dispatched based on broadcast_ts dedup.
 *  New sessions default to Infinity (sentinel), so their first event is recorded
 *  but not dispatched — prevents snapshot flooding on new/restarted clients. */
export function shouldDispatch(sessionCode: string, broadcastTs: number): boolean {
  const data: Record<string, number> = JSON.parse(localStorage.getItem(SEEN_KEY) || '{}')
  const stored = data[sessionCode] ?? Infinity

  if (broadcastTs <= stored) {
    if (stored === Infinity) {
      data[sessionCode] = broadcastTs
      localStorage.setItem(SEEN_KEY, JSON.stringify(data))
    }
    return false
  }

  data[sessionCode] = broadcastTs
  localStorage.setItem(SEEN_KEY, JSON.stringify(data))
  return true
}

/** Remove a session's lastSeenTs entry (called on SessionEnd to prevent
 *  stale timestamps from blocking notifications if the code is reused). */
export function clearSeenTs(sessionCode: string): void {
  const data: Record<string, number> = JSON.parse(localStorage.getItem(SEEN_KEY) || '{}')
  delete data[sessionCode]
  localStorage.setItem(SEEN_KEY, JSON.stringify(data))
}

interface ShouldNotifyParams {
  derived: string | null
  eventName: string
  sessionCode: string
  focusedSession: string | null
  hasTab: boolean
  settings: NotificationSettings
}

export function shouldNotify(params: ShouldNotifyParams): boolean {
  const { derived, eventName, sessionCode, focusedSession, hasTab, settings } = params
  if (derived !== 'waiting' && derived !== 'idle' && derived !== 'error') return false
  // Informational Notification subtypes (idle_prompt, auth_success) derive to 'idle'
  // but should not trigger desktop notifications — consistent with unread marking logic.
  if (derived === 'idle' && eventName === 'Notification') return false
  if (!settings.enabled) return false
  if (settings.events[eventName] === false) return false
  if (!hasTab && !settings.notifyWithoutTab) return false
  // Only suppress when user is actively looking at this session:
  // both the app window must be focused AND the session tab must be active.
  if (focusedSession === sessionCode && document.hasFocus()) return false
  return true
}

export function useNotificationDispatcher(): void {
  useEffect(() => {
    const unsubscribe = useAgentStore.subscribe((state, prevState) => {
      const prevEvents = prevState.events
      const currentEvents = state.events

      // Clean up lastSeenTs for sessions that ended (prevents stale ts
      // blocking notifications if the session code is reused).
      for (const key of Object.keys(prevEvents)) {
        if (!currentEvents[key]) {
          clearSeenTs(key)
        }
      }

      for (const [compositeKeyStr, event] of Object.entries(currentEvents)) {
        const prev = prevEvents[compositeKeyStr]
        if (prev && prev.broadcast_ts === event.broadcast_ts) continue

        // Extract sessionCode from composite key (hostId:sessionCode)
        const colonIdx = compositeKeyStr.indexOf(':')
        const hostId = colonIdx >= 0 ? compositeKeyStr.slice(0, colonIdx) : ''
        const sessionCode = colonIdx >= 0 ? compositeKeyStr.slice(colonIdx + 1) : compositeKeyStr

        // Dedup layer 1: localStorage-based persistent dedup (handles restart/snapshot).
        // New sessions use Infinity sentinel — first event is recorded but not dispatched.
        // Layer 2: shouldNotify checks active session (derived from activeTabId) + document.hasFocus().
        // Layer 3: Electron main process recentBroadcasts dedup (5s window, multi-window).
        if (!shouldDispatch(compositeKeyStr, event.broadcast_ts)) continue

        const derived = deriveStatus(event.event_name, event.raw_event)
        const tabs = useTabStore.getState().tabs
        const hasTab = findTabBySessionCode(tabs, sessionCode) !== undefined
        const settings = useNotificationSettingsStore.getState().getSettingsForAgent(event.agent_type || '')
        const focusedSession = getActiveSessionCode()

        if (!shouldNotify({ derived, eventName: event.event_name, sessionCode, focusedSession, hasTab, settings })) continue

        const sessionsMap = useSessionStore.getState().sessions
        const hostSessions = sessionsMap[hostId] ?? []
        const session = hostSessions.find((s) => s.code === sessionCode)
        const sessionName = session?.name || sessionCode

        const content = buildNotificationContent(event.event_name, event.raw_event, sessionName, useI18nStore.getState().t)
        if (!content) continue

        const capabilities = getPlatformCapabilities()
        if (capabilities.canNotification && window.electronAPI?.showNotification) {
          window.electronAPI.showNotification({
            title: content.title,
            body: content.body,
            sessionCode,
            eventName: event.event_name,
            broadcastTs: event.broadcast_ts,
          })
        } else if ('Notification' in window && Notification.permission === 'granted') {
          const n = new Notification(content.title, { body: content.body })
          n.onclick = () => handleNotificationClick(sessionCode)
        }
      }
    })
    return unsubscribe
  }, [])

  // Electron notification click listener
  useEffect(() => {
    if (!window.electronAPI?.onNotificationClicked) return
    return window.electronAPI.onNotificationClicked((payload) => {
      handleNotificationClick(payload.sessionCode)
    })
  }, [])
}

function handleNotificationClick(sessionCode: string): void {
  const tabs = useTabStore.getState().tabs
  const tabId = findTabBySessionCode(tabs, sessionCode)

  // Resolve hostId: try to find from the tab's pane content, fallback to first host
  let hostId = useHostStore.getState().hostOrder[0] ?? ''
  if (tabId) {
    const tab = tabs[tabId]
    if (tab) {
      const primary = getPrimaryPane(tab.layout)
      if (primary.content.kind === 'session') {
        hostId = primary.content.hostId
      }
    }
  }

  const ck = `${hostId}:${sessionCode}`
  const event = useAgentStore.getState().events[ck]
  const agentSettings = useNotificationSettingsStore.getState().getSettingsForAgent(event?.agent_type || '')

  let handled = false
  if (tabId) {
    useTabStore.getState().setActiveTab(tabId)
    handled = true
  } else if (agentSettings.reopenTabOnClick) {
    const newTab = createTab({ kind: 'session', hostId, sessionCode, mode: 'stream' })
    useTabStore.getState().addTab(newTab)
    useTabStore.getState().setActiveTab(newTab.id)
    const activeWorkspaceId = useWorkspaceStore.getState().activeWorkspaceId
    if (activeWorkspaceId) {
      useWorkspaceStore.getState().addTabToWorkspace(activeWorkspaceId, newTab.id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(activeWorkspaceId, newTab.id)
    }
    handled = true
  }

  // Always markRead — cross-store subscription only fires on tab *change*,
  // but notification click on the already-active tab still needs to clear unread.
  if (handled) {
    useAgentStore.getState().markRead(hostId, sessionCode)
  }

  if (handled && window.electronAPI?.focusMyWindow) {
    window.electronAPI.focusMyWindow()
  }
}
