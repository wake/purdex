import { useEffect, type RefObject } from 'react'

/**
 * Observes the given element and pushes its bounds to Electron's BrowserViewManager
 * via the resizeBrowserView IPC. Used by both BrowserPane and MiniBrowserApp.
 */
export function useBrowserViewResize(paneId: string, ref: RefObject<HTMLDivElement | null>): void {
  useEffect(() => {
    const el = ref.current
    if (!window.electronAPI || !el) return

    let rafId = 0
    const observer = new ResizeObserver(() => {
      cancelAnimationFrame(rafId)
      rafId = requestAnimationFrame(() => {
        const rect = el.getBoundingClientRect()
        if (!rect.width || !rect.height) return
        window.electronAPI!.resizeBrowserView(paneId, {
          x: Math.round(rect.x),
          y: Math.round(rect.y),
          width: Math.round(rect.width),
          height: Math.round(rect.height),
        })
      })
    })
    observer.observe(el)

    return () => {
      cancelAnimationFrame(rafId)
      observer.disconnect()
    }
  }, [paneId, ref])
}
