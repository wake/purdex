// spa/src/hooks/useMiniWindowShortcuts.ts
import { useEffect } from 'react'

export function useMiniWindowShortcuts(paneId: string): void {
  useEffect(() => {
    if (!window.electronAPI?.onShortcut) return

    const cleanup = window.electronAPI.onShortcut(({ action }) => {
      switch (action) {
        case 'close-tab':
          window.close()
          break
        case 'go-back':
          window.electronAPI?.browserViewGoBack(paneId)
          break
        case 'go-forward':
          window.electronAPI?.browserViewGoForward(paneId)
          break
        case 'reload':
          window.electronAPI?.browserViewReload(paneId)
          break
        case 'focus-url':
          document.dispatchEvent(new CustomEvent('browser:focus-url'))
          break
        case 'print':
          window.electronAPI?.browserViewPrint(paneId)
          break
      }
    })

    return cleanup
  }, [paneId])
}
