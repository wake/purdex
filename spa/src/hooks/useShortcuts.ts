import { useEffect } from 'react'
import { executeShortcutAction } from '../lib/shortcut-actions'

export function useShortcuts(): void {
  useEffect(() => {
    if (!window.electronAPI?.onShortcut) return
    const cleanup = window.electronAPI.onShortcut(({ action }) => executeShortcutAction(action))
    return cleanup
  }, [])
}
