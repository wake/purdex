import { useEffect, useRef, useCallback } from 'react'
import { GlobeX } from '@phosphor-icons/react'
import { BrowserToolbar } from './BrowserToolbar'
import { useBrowserViewState } from '../hooks/useBrowserViewState'
import { useBrowserViewResize } from '../hooks/useBrowserViewResize'
import { useI18nStore } from '../stores/useI18nStore'
import { useTabStore } from '../stores/useTabStore'
import { findPane } from '../lib/pane-tree'

interface BrowserPaneProps {
  paneId: string
  url: string
}

// Every electronAPI method is called as `?.method?.(…)`: the App loads the SPA from the dev server, so its
// preload can be older than this code and lack any one of them (#1816) — a missing method is a no-op, never a
// TypeError.
export function BrowserPane({ paneId, url }: BrowserPaneProps) {
  const t = useI18nStore((s) => s.t)
  const contentRef = useRef<HTMLDivElement>(null)
  const initialUrlRef = useRef(url)
  const state = useBrowserViewState(paneId)
  // Without openBrowserView no view can ever show here: the pane says so instead of staying empty.
  const canOpen = typeof window.electronAPI?.openBrowserView === 'function'

  // Display URL: prefer live state, fallback to initial url prop
  const currentUrl = state.url || url

  // Open/close lifecycle — mount/unmount only
  useEffect(() => {
    if (!window.electronAPI) return
    window.electronAPI.openBrowserView?.(initialUrlRef.current, paneId)
    return () => { window.electronAPI?.closeBrowserView?.(paneId) }
  }, [paneId])

  // Navigate on URL change (skip initial mount)
  useEffect(() => {
    if (!window.electronAPI) return
    if (url === initialUrlRef.current) return
    initialUrlRef.current = url
    window.electronAPI.navigateBrowserView?.(paneId, url)
  }, [url, paneId])

  // Bounds sync via ResizeObserver — observe content area (below toolbar)
  useBrowserViewResize(paneId, contentRef)

  // Toolbar callbacks
  const handleGoBack = useCallback(() => window.electronAPI?.browserViewGoBack?.(paneId), [paneId])
  const handleGoForward = useCallback(() => window.electronAPI?.browserViewGoForward?.(paneId), [paneId])
  const handleReload = useCallback(() => window.electronAPI?.browserViewReload?.(paneId), [paneId])
  const handleStop = useCallback(() => window.electronAPI?.browserViewStop?.(paneId), [paneId])
  const handleNavigate = useCallback(
    (newUrl: string) => window.electronAPI?.navigateBrowserView?.(paneId, newUrl),
    [paneId],
  )
  const handleOpenExternal = useCallback(() => window.open(currentUrl, '_blank'), [currentUrl])
  const handleCopyUrl = useCallback(() => { navigator.clipboard.writeText(currentUrl) }, [currentUrl])
  const handlePopOut = useCallback(
    () => window.electronAPI?.browserViewOpenMiniWindow?.(currentUrl),
    [currentUrl],
  )
  // Leaves the degraded state the way a New Tab pick enters a pane: the pane's content is replaced, so the
  // tab and any split around it stay as they are.
  const handleBackToNewTab = useCallback(() => {
    const { tabs, setPaneContent } = useTabStore.getState()
    const tabId = Object.keys(tabs).find((id) => findPane(tabs[id].layout, paneId) !== undefined)
    if (tabId) setPaneContent(tabId, paneId, { kind: 'new-tab' })
  }, [paneId])

  if (!canOpen) {
    return (
      <div
        data-browser-pane={paneId}
        className="flex flex-col items-center justify-center h-full w-full p-8 text-center"
      >
        <GlobeX size={48} className="text-zinc-500 mb-4" />
        <p className="text-sm text-zinc-400 mb-3">{t('browser.unsupported')}</p>
        <button
          type="button"
          onClick={handleBackToNewTab}
          className="text-sm text-accent-base hover:underline cursor-pointer"
        >
          {t('browser.unsupported_back')}
        </button>
      </div>
    )
  }

  return (
    <div className="flex flex-col h-full" data-browser-pane={paneId}>
      <BrowserToolbar
        url={currentUrl}
        canGoBack={state.canGoBack}
        canGoForward={state.canGoForward}
        isLoading={state.isLoading}
        onGoBack={handleGoBack}
        onGoForward={handleGoForward}
        onReload={handleReload}
        onStop={handleStop}
        onNavigate={handleNavigate}
        onOpenExternal={handleOpenExternal}
        onCopyUrl={handleCopyUrl}
        onPopOut={handlePopOut}
      />
      {/* Content area: WebContentsView overlays this div */}
      <div ref={contentRef} className="flex-1" />
    </div>
  )
}
