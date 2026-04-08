import { useRef, useCallback, useEffect } from 'react'
import { BrowserToolbar } from './BrowserToolbar'
import { useBrowserViewState } from '../hooks/useBrowserViewState'
import { useBrowserViewResize } from '../hooks/useBrowserViewResize'
import { useMiniWindowShortcuts } from '../hooks/useMiniWindowShortcuts'
import { useThemeStore } from '../stores/useThemeStore'

interface Props {
  paneId: string
}

export function MiniBrowserApp({ paneId }: Props) {
  const contentRef = useRef<HTMLDivElement>(null)
  const state = useBrowserViewState(paneId)
  const activeThemeId = useThemeStore((s) => s.activeThemeId)
  useEffect(() => {
    document.documentElement.dataset.theme = activeThemeId
  }, [activeThemeId])

  useBrowserViewResize(paneId, contentRef)
  useMiniWindowShortcuts(paneId)

  const handleGoBack = useCallback(() => window.electronAPI?.browserViewGoBack(paneId), [paneId])
  const handleGoForward = useCallback(() => window.electronAPI?.browserViewGoForward(paneId), [paneId])
  const handleReload = useCallback(() => window.electronAPI?.browserViewReload(paneId), [paneId])
  const handleStop = useCallback(() => window.electronAPI?.browserViewStop(paneId), [paneId])
  const handleNavigate = useCallback(
    (url: string) => window.electronAPI?.navigateBrowserView(paneId, url),
    [paneId],
  )
  const handleOpenExternal = useCallback(() => window.open(state.url, '_blank'), [state.url])
  const handleCopyUrl = useCallback(() => { navigator.clipboard.writeText(state.url) }, [state.url])
  const handleMoveToTab = useCallback(
    () => window.electronAPI?.browserViewMoveToTab(paneId),
    [paneId],
  )

  return (
    <div className="flex flex-col h-screen bg-surface-primary text-text-primary">
      {/* Electron title bar drag region */}
      <div
        className="h-10 flex items-end flex-shrink-0"
        style={{ WebkitAppRegion: 'drag' } as React.CSSProperties}
      />
      <BrowserToolbar
        url={state.url}
        canGoBack={state.canGoBack}
        canGoForward={state.canGoForward}
        isLoading={state.isLoading}
        context="mini-window"
        onGoBack={handleGoBack}
        onGoForward={handleGoForward}
        onReload={handleReload}
        onStop={handleStop}
        onNavigate={handleNavigate}
        onOpenExternal={handleOpenExternal}
        onCopyUrl={handleCopyUrl}
        onMoveToTab={handleMoveToTab}
      />
      <div ref={contentRef} className="flex-1" />
    </div>
  )
}
