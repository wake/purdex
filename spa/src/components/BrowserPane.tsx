import { useEffect, useRef, useCallback } from 'react'
import { useI18nStore } from '../stores/useI18nStore'
import { BrowserToolbar } from './BrowserToolbar'
import { useBrowserViewState } from '../hooks/useBrowserViewState'
import { useBrowserViewResize } from '../hooks/useBrowserViewResize'

interface BrowserPaneProps {
  paneId: string
  url: string
}

export function BrowserPane({ paneId, url }: BrowserPaneProps) {
  const t = useI18nStore((s) => s.t)
  const contentRef = useRef<HTMLDivElement>(null)
  const initialUrlRef = useRef(url)
  const state = useBrowserViewState(paneId)

  // Display URL: prefer live state, fallback to initial url prop
  const currentUrl = state.url || url

  // Open/close lifecycle — mount/unmount only
  useEffect(() => {
    if (!window.electronAPI) return
    window.electronAPI.openBrowserView(initialUrlRef.current, paneId)
    return () => { window.electronAPI?.closeBrowserView(paneId) }
  }, [paneId])

  // Navigate on URL change (skip initial mount)
  useEffect(() => {
    if (!window.electronAPI) return
    if (url === initialUrlRef.current) return
    initialUrlRef.current = url
    window.electronAPI.navigateBrowserView(paneId, url)
  }, [url, paneId])

  // Bounds sync via ResizeObserver — observe content area (below toolbar)
  useBrowserViewResize(paneId, contentRef)

  // Toolbar callbacks
  const handleGoBack = useCallback(() => window.electronAPI?.browserViewGoBack(paneId), [paneId])
  const handleGoForward = useCallback(() => window.electronAPI?.browserViewGoForward(paneId), [paneId])
  const handleReload = useCallback(() => window.electronAPI?.browserViewReload(paneId), [paneId])
  const handleStop = useCallback(() => window.electronAPI?.browserViewStop(paneId), [paneId])
  const handleNavigate = useCallback(
    (newUrl: string) => window.electronAPI?.navigateBrowserView(paneId, newUrl),
    [paneId],
  )
  const handleOpenExternal = useCallback(() => window.open(currentUrl, '_blank'), [currentUrl])
  const handleCopyUrl = useCallback(() => { navigator.clipboard.writeText(currentUrl) }, [currentUrl])
  const handlePopOut = useCallback(
    () => window.electronAPI?.browserViewOpenMiniWindow(currentUrl),
    [currentUrl],
  )

  // SPA fallback
  if (!window.electronAPI) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <p className="text-sm text-text-muted">{t('browser.requires_app')}</p>
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
