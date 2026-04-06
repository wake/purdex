// spa/src/hooks/useRouteSync.ts
import { useEffect, useState } from 'react'
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

  // Hydration guard — don't run URL→Tab until persist has hydrated
  const [hydrated, setHydrated] = useState(useTabStore.persist.hasHydrated())

  useEffect(() => {
    if (hydrated) return
    return useTabStore.persist.onFinishHydration(() => setHydrated(true))
  }, [hydrated])

  // Tab → URL: derived activeUrl selector (idempotent — only sets if URL differs)
  const activeUrl = useTabStore((s) => {
    if (!s.activeTabId) return null
    const tab = s.tabs[s.activeTabId]
    if (!tab) return null
    const primary = getPrimaryPane(tab.layout)
    if (!primary) return null
    return tabToUrl(s.activeTabId, primary.content)
  })

  // location excluded: including it would create navigate→location→navigate loop.
  // setLocation excluded: stable function from wouter.
  useEffect(() => {
    if (!hydrated) return
    if (activeUrl && location !== activeUrl && !location.startsWith(activeUrl + '/')) {
      setLocation(activeUrl, { replace: true })
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeUrl, hydrated])

  // Record visit when activeTab changes.
  // tabs excluded: only activeTabId change should trigger a visit record; tabs object changes frequently.
  useEffect(() => {
    if (!hydrated) return
    if (!activeTabId) return
    const tab = tabs[activeTabId]
    if (!tab) return
    const primary = getPrimaryPane(tab.layout)
    if (!primary) return
    useHistoryStore.getState().recordVisit(activeTabId, primary.content)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTabId, hydrated])

  // URL → Tab: when URL changes (back/forward/direct), find or create tab
  useEffect(() => {
    if (!hydrated) return

    const parsed = parseRoute(location)
    if (!parsed) return

    // Check if URL already matches the current active tab — avoid redundant state changes
    const currentTabId = useTabStore.getState().activeTabId
    if (currentTabId) {
      const currentTab = useTabStore.getState().tabs[currentTabId]
      if (currentTab) {
        const primary = getPrimaryPane(currentTab.layout)
        if (primary) {
          const currentUrl = tabToUrl(currentTabId, primary.content)
          if (currentUrl === location || location.startsWith(currentUrl + '/')) return // already in sync (includes sub-paths)
        }
      }
    }

    switch (parsed.kind) {
      case 'history':
        openSingletonTab({ kind: 'history' })
        break
      case 'hosts':
        openSingletonTab({ kind: 'hosts' })
        break
      case 'settings':
        openSingletonTab({ kind: 'settings', scope: 'global' })
        break
      case 'session-tab': {
        const tab = useTabStore.getState().tabs[parsed.tabId]
        if (tab) {
          setActiveTab(parsed.tabId)
        } else {
          setActiveTab(null) // show empty state
        }
        break
      }
      case 'workspace':
        // Workspace activation handled by App — no state change here
        break
      case 'workspace-settings':
        openSingletonTab({ kind: 'settings', scope: { workspaceId: parsed.workspaceId } })
        break
      case 'workspace-session-tab': {
        const tab = useTabStore.getState().tabs[parsed.tabId]
        if (tab) {
          setActiveTab(parsed.tabId)
        } else {
          setActiveTab(null) // show empty state
        }
        break
      }
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps -- openSingletonTab, setActiveTab: stable Zustand selectors
  }, [location, hydrated])
}
