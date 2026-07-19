import { useEffect } from 'react'
import { getPlatformCapabilities } from '../lib/platform'
import { matchWebShortcut, isEditableTarget } from '../lib/web-shortcuts'
import { executeShortcutAction } from '../lib/shortcut-actions'

// Web-only keyboard shortcut layer. In Electron the menu accelerators + IPC
// (useShortcuts) own shortcuts. On the web we listen for keydown and dispatch
// the same action ids. Focus matrix: non-global shortcuts are suppressed when
// focus is an editable target (input/textarea/contentEditable, incl. xterm's
// hidden textarea) so form/terminal typing is never eaten.
export function useWebShortcuts(): void {
  useEffect(() => {
    if (getPlatformCapabilities().isElectron) return
    const onKeyDown = (e: KeyboardEvent) => {
      const match = matchWebShortcut(e)
      if (!match) return
      if (!match.global && isEditableTarget(e.target)) return
      e.preventDefault()
      executeShortcutAction(match.action)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])
}
