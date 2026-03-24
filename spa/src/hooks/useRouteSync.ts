// spa/src/hooks/useRouteSync.ts
import { useEffect, useRef, useState } from 'react'
import { useLocation } from 'wouter'
import { useTabStore } from '../stores/useTabStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { parseRoute, tabToUrl } from '../lib/route-utils'
import { getPrimaryPane } from '../lib/pane-tree'

export function useRouteSync() {
  const [location, setLocation] = useLocation()
  const tabs = useTabStore((s) => s.tabs)
  const activeTabId = useTabStore((s) => s.activeTabId)
  const setActiveTab = useTabStore((s) => s.setActiveTab)
  const openSingletonTab = useTabStore((s) => s.openSingletonTab)
  const suppressSync = useRef(false)

  // Fix 1: hydration guard — don't run URL→Tab until persist has hydrated
  const [hydrated, setHydrated] = useState(useTabStore.persist.hasHydrated())

  useEffect(() => {
    if (hydrated) return
    return useTabStore.persist.onFinishHydration(() => setHydrated(true))
  }, [hydrated])

  // Fix 3: Tab → URL via derived activeUrl selector
  const activeUrl = useTabStore((s) => {
    if (!s.activeTabId) return null
    const tab = s.tabs[s.activeTabId]
    if (!tab) return null
    const primary = getPrimaryPane(tab.layout)
    if (!primary) return null
    return tabToUrl(s.activeTabId, primary.content)
  })

  useEffect(() => {
    if (suppressSync.current) {
      suppressSync.current = false
      return
    }
    if (activeUrl && location !== activeUrl) setLocation(activeUrl, { replace: true })
  }, [activeUrl])

  // Fix 5: record visit when activeTab changes
  useEffect(() => {
    if (!hydrated) return
    if (!activeTabId) return
    const tab = tabs[activeTabId]
    if (!tab) return
    const primary = getPrimaryPane(tab.layout)
    if (!primary) return
    useHistoryStore.getState().recordVisit(activeTabId, primary.content)
  }, [activeTabId, hydrated])

  // URL → Tab: when URL changes (back/forward/direct), find or create tab
  useEffect(() => {
    if (!hydrated) return // Fix 1: guard

    const parsed = parseRoute(location)
    if (!parsed) return

    // Fix 2: only set suppressSync inside cases that actually change state
    switch (parsed.kind) {
      case 'dashboard':
        suppressSync.current = true
        openSingletonTab({ kind: 'dashboard' })
        break
      case 'history':
        suppressSync.current = true
        openSingletonTab({ kind: 'history' })
        break
      case 'settings':
        suppressSync.current = true
        openSingletonTab({ kind: 'settings', scope: 'global' })
        break
      case 'session-tab': {
        const tab = tabs[parsed.tabId]
        if (tab) {
          suppressSync.current = true
          setActiveTab(parsed.tabId)
        }
        // else: tab not found → don't suppress, content area shows new-tab page
        break
      }
      case 'workspace':
        // Workspace activation handled by App — no state change here
        break
      case 'workspace-settings':
        suppressSync.current = true
        openSingletonTab({ kind: 'settings', scope: { workspaceId: parsed.workspaceId } })
        break
      case 'workspace-session-tab': {
        const tab = tabs[parsed.tabId]
        if (tab) {
          suppressSync.current = true
          setActiveTab(parsed.tabId)
        }
        break
      }
    }
  }, [location, hydrated])
}
