import { useEffect, useRef } from 'react'
import { useI18nStore } from '../stores/useI18nStore'

interface BrowserPaneProps {
  paneId: string
  url: string
}

export function BrowserPane({ paneId, url }: BrowserPaneProps) {
  const t = useI18nStore((s) => s.t)
  const ref = useRef<HTMLDivElement>(null)
  const initialUrlRef = useRef(url)

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

  // Bounds sync via ResizeObserver
  useEffect(() => {
    if (!window.electronAPI || !ref.current) return
    const observer = new ResizeObserver(() => {
      const el = ref.current
      if (!el) return
      const rect = el.getBoundingClientRect()
      window.electronAPI!.resizeBrowserView(paneId, {
        x: Math.round(rect.x),
        y: Math.round(rect.y),
        width: Math.round(rect.width),
        height: Math.round(rect.height),
      })
    })
    observer.observe(ref.current)
    return () => observer.disconnect()
  }, [paneId, url])

  // SPA fallback
  if (!window.electronAPI) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <p className="text-sm text-text-muted">{t('browser.requires_app')}</p>
      </div>
    )
  }

  return <div ref={ref} className="w-full h-full" data-browser-pane={paneId} />
}
