// spa/src/hooks/useRouteSync.ts
import { useEffect, useState } from 'react'
import { useLocation } from 'wouter'
import { useTabStore } from '../stores/useTabStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { parseRoute, tabToUrl } from '../lib/route-utils'
import { getPrimaryPane } from '../lib/pane-tree'
import { useWorkspaceStore } from '../features/workspace'
import { resolveExecutionHostId } from '../lib/nex/resolve-host'

type TabStoreState = ReturnType<typeof useTabStore.getState>

/** URL of the active tab's primary pane, or null when there is none. */
function activeTabUrl(s: TabStoreState): string | null {
  if (!s.activeTabId) return null
  const tab = s.tabs[s.activeTabId]
  if (!tab) return null
  const primary = getPrimaryPane(tab.layout)
  if (!primary) return null
  return tabToUrl(s.activeTabId, primary.content)
}

/** Does `location` show the tab at `tabUrl` (exactly, or one of its sub-paths)? */
function urlShowsTab(location: string, tabUrl: string): boolean {
  return location === tabUrl || location.startsWith(tabUrl + '/')
}

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

  // The active tab's URL — Tab → URL's trigger (re-renders only when it changes).
  const activeUrl = useTabStore(activeTabUrl)

  // URL → Tab: when URL changes (back/forward/direct), find or create tab.
  // Declared BEFORE Tab → URL on purpose: when both effects run in the same
  // commit — the first hydrated pass of a cold start always does — the URL is
  // the source of truth, so it is applied to the store before Tab → URL decides
  // whether the URL needs correcting (#1326).
  useEffect(() => {
    if (!hydrated) return

    const parsed = parseRoute(location)
    if (!parsed) return

    // Check if URL already matches the current active tab — avoid redundant state changes
    const currentUrl = activeTabUrl(useTabStore.getState())
    if (currentUrl && urlShowsTab(location, currentUrl)) return // already in sync (includes sub-paths)

    switch (parsed.kind) {
      case 'history':
        openSingletonTab({ kind: 'history' })
        break
      case 'hosts':
      case 'hosts-invalid':
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
      case 'workspace-settings': {
        const wsStore = useWorkspaceStore.getState()
        wsStore.setActiveWorkspace(parsed.workspaceId)
        const tabId = openSingletonTab({ kind: 'settings', scope: { workspaceId: parsed.workspaceId } })
        wsStore.insertTab(tabId, parsed.workspaceId)
        break
      }
      case 'workspace-session-tab': {
        // Legacy alias (#1336): /w/<ws>/t/<tab>/<mode> is not a canonical
        // route. Every tab has exactly one owner since P3c (from the
        // workspace store, not the URL), so `parsed.workspaceId` is ignored
        // here — this only activates the tab. Tab→URL then normalises the
        // location to the canonical /t/<tab>/<mode>.
        const tab = useTabStore.getState().tabs[parsed.tabId]
        if (tab) {
          setActiveTab(parsed.tabId)
        } else {
          setActiveTab(null) // show empty state
        }
        break
      }
      case 'execution':
        // Execution pane (spec §4.3.3), singleton per (host, execution id).
        // It owns its own observe subscription, so a direct URL /
        // back-forward never dead-ends. The host segment (or its absence) is
        // resolved at open time so the pane always has a concrete host.
        openSingletonTab({ kind: 'execution', executionId: parsed.executionId, host: resolveExecutionHostId(parsed.host) })
        break
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps -- openSingletonTab, setActiveTab: stable Zustand selectors
  }, [location, hydrated])

  // Record visit when activeTab changes.
  // Declared AFTER URL → Tab and skipped when this render's activeTabId is no
  // longer the store's: on a deep-link cold start URL → Tab switches the tab in
  // the same commit, and recording the render's (stale, persisted) tab would
  // log a phantom visit — the switch re-renders and records the real one.
  // tabs excluded: only activeTabId change should trigger a visit record; tabs object changes frequently.
  useEffect(() => {
    if (!hydrated) return
    if (!activeTabId) return
    if (activeTabId !== useTabStore.getState().activeTabId) return
    const tab = tabs[activeTabId]
    if (!tab) return
    const primary = getPrimaryPane(tab.layout)
    if (!primary) return
    useHistoryStore.getState().recordVisit(activeTabId, primary.content)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTabId, hydrated])

  // Tab → URL: replace the URL when it doesn't show the active tab.
  // Reads the store NOW, not this render's `activeUrl`: URL → Tab (above) may
  // have just switched the active tab in this same commit, and the render's
  // value is then stale. Acting on it replaced a cold-start deep link with the
  // persisted tab's URL while URL → Tab applied the deep link — the next commit
  // each undid the other, forever (#1326). So what gets corrected here is a URL
  // URL → Tab had nothing to apply for (unparseable, `/w/<ws>`, …) or one left
  // behind by a tab switch made from the UI.
  // location excluded: including it would create navigate→location→navigate loop.
  // setLocation excluded: stable function from wouter.
  useEffect(() => {
    if (!hydrated) return
    const url = activeTabUrl(useTabStore.getState())
    if (url && !urlShowsTab(location, url)) {
      setLocation(url, { replace: true })
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeUrl, hydrated])
}
