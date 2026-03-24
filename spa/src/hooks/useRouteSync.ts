// spa/src/hooks/useRouteSync.ts
import { useEffect, useRef } from 'react'
import { useLocation } from 'wouter'
import { useTabStore } from '../stores/useTabStore'
import { parseRoute, tabToUrl } from '../lib/route-utils'
import { getPrimaryPane } from '../lib/pane-tree'

export function useRouteSync() {
  const [location, setLocation] = useLocation()
  const tabs = useTabStore((s) => s.tabs)
  const activeTabId = useTabStore((s) => s.activeTabId)
  const setActiveTab = useTabStore((s) => s.setActiveTab)
  const openSingletonTab = useTabStore((s) => s.openSingletonTab)
  const suppressSync = useRef(false)

  // Tab → URL: when activeTab changes, update URL
  useEffect(() => {
    if (suppressSync.current) {
      suppressSync.current = false
      return
    }
    if (!activeTabId) return
    const tab = tabs[activeTabId]
    if (!tab) return
    const content = getPrimaryPane(tab.layout).content
    const url = tabToUrl(activeTabId, content)
    if (location !== url) setLocation(url, { replace: true })
  }, [activeTabId])

  // URL → Tab: when URL changes (back/forward/direct), find or create tab
  useEffect(() => {
    const parsed = parseRoute(location)
    if (!parsed) return

    suppressSync.current = true

    switch (parsed.kind) {
      case 'dashboard':
        openSingletonTab({ kind: 'dashboard' })
        break
      case 'history':
        openSingletonTab({ kind: 'history' })
        break
      case 'settings':
        openSingletonTab({ kind: 'settings', scope: 'global' })
        break
      case 'session-tab': {
        const tab = tabs[parsed.tabId]
        if (tab) setActiveTab(parsed.tabId)
        // else: tab not found → stay on URL, content area shows new-tab page
        break
      }
      case 'workspace':
        // Workspace activation handled by App — this just routes
        break
      case 'workspace-settings':
        openSingletonTab({ kind: 'settings', scope: { workspaceId: parsed.workspaceId } })
        break
      case 'workspace-session-tab': {
        const tab = tabs[parsed.tabId]
        if (tab) setActiveTab(parsed.tabId)
        break
      }
    }
  }, [location])
}
