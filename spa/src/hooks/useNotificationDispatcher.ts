import { useEffect, useRef } from 'react'
import { useAgentStore, deriveStatus } from '../stores/useAgentStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'
import type { NotificationSettings } from '../stores/useNotificationSettingsStore'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useSessionStore } from '../stores/useSessionStore'
import { buildNotificationContent } from '../lib/notification-content'
import { findTabBySessionCode } from '../lib/pane-tree'
import { getPlatformCapabilities } from '../lib/platform'
import { createTab } from '../types/tab'

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
  if (derived !== 'waiting' && derived !== 'idle') return false
  if (!settings.enabled) return false
  if (settings.events[eventName] === false) return false
  if (!hasTab && !settings.notifyWithoutTab) return false
  if (focusedSession === sessionCode) return false
  return true
}

export function useNotificationDispatcher(): void {
  const notifiedRef = useRef(new Set<number>())

  useEffect(() => {
    const unsubscribe = useAgentStore.subscribe((state, prevState) => {
      const prevEvents = prevState.events
      const currentEvents = state.events

      for (const [sessionCode, event] of Object.entries(currentEvents)) {
        const prev = prevEvents[sessionCode]
        if (prev && prev.broadcast_ts === event.broadcast_ts) continue

        // Double protection: skip if this broadcast_ts was already notified
        // (guards against DB fix edge cases and initial snapshot replays)
        if (notifiedRef.current.has(event.broadcast_ts)) continue

        const derived = deriveStatus(event.event_name, event.raw_event)
        const tabs = useTabStore.getState().tabs
        const hasTab = findTabBySessionCode(tabs, sessionCode) !== undefined
        const settings = useNotificationSettingsStore.getState().getSettingsForAgent(event.agent_type || '')
        const focusedSession = state.focusedSession

        if (!shouldNotify({ derived, eventName: event.event_name, sessionCode, focusedSession, hasTab, settings })) continue

        const sessions = useSessionStore.getState().sessions
        const session = sessions.find((s) => s.code === sessionCode)
        const sessionName = session?.name || sessionCode

        const content = buildNotificationContent(event.event_name, event.raw_event, sessionName, useI18nStore.getState().t)
        if (!content) continue

        notifiedRef.current.add(event.broadcast_ts)

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
  const event = useAgentStore.getState().events[sessionCode]
  const agentSettings = useNotificationSettingsStore.getState().getSettingsForAgent(event?.agent_type || '')

  let handled = false
  if (tabId) {
    useTabStore.getState().setActiveTab(tabId)
    useAgentStore.getState().setFocusedSession(sessionCode)
    handled = true
  } else if (agentSettings.reopenTabOnClick) {
    const newTab = createTab({ kind: 'session', sessionCode, mode: 'stream' })
    useTabStore.getState().addTab(newTab)
    useTabStore.getState().setActiveTab(newTab.id)
    const activeWorkspaceId = useWorkspaceStore.getState().activeWorkspaceId
    if (activeWorkspaceId) {
      useWorkspaceStore.getState().addTabToWorkspace(activeWorkspaceId, newTab.id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(activeWorkspaceId, newTab.id)
    }
    useAgentStore.getState().setFocusedSession(sessionCode)
    handled = true
  }

  if (handled && window.electronAPI?.focusMyWindow) {
    window.electronAPI.focusMyWindow()
  }
}
