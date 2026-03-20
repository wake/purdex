import { useEffect } from 'react'
import { useTabStore } from '../stores/useTabStore'
import { parseHash, setHash } from '../lib/hash-routing'

export function useHashRouting(activeTabId: string | null, setActiveTab: (id: string) => void) {
  const activeTab = useTabStore((s) => activeTabId ? s.tabs[activeTabId] ?? null : null)
  const viewMode = activeTab?.viewMode

  // Restore tab + viewMode from URL on mount
  useEffect(() => {
    const { tabId, viewMode: urlViewMode } = parseHash()
    if (tabId && useTabStore.getState().tabs[tabId]) {
      setActiveTab(tabId)
      if (urlViewMode) {
        useTabStore.getState().setViewMode(tabId, urlViewMode)
      }
    }
  }, [setActiveTab])

  // Sync activeTabId + viewMode → URL
  useEffect(() => {
    if (activeTabId) setHash(activeTabId, viewMode ?? undefined)
  }, [activeTabId, viewMode])

  // Listen for browser back/forward
  useEffect(() => {
    const handler = () => {
      const { tabId, viewMode: urlViewMode } = parseHash()
      if (tabId && useTabStore.getState().tabs[tabId]) {
        setActiveTab(tabId)
        if (urlViewMode) {
          useTabStore.getState().setViewMode(tabId, urlViewMode)
        }
      }
    }
    window.addEventListener('hashchange', handler)
    return () => window.removeEventListener('hashchange', handler)
  }, [setActiveTab])
}
