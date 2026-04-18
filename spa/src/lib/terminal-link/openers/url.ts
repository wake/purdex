import type { LinkOpener } from '../types'

export interface UrlOpenerDeps {
  isElectron: boolean
  openBrowserTab: (url: string) => void
  openMiniWindow: (url: string) => void
}

export function createUrlOpener(deps: UrlOpenerDeps): LinkOpener {
  return {
    id: 'builtin:url',
    priority: 0,
    canOpen: (token) => token.type === 'url',
    open: (token, _ctx, event) => {
      const uri = token.text
      if (deps.isElectron) {
        // Shift+Click 開在 mini window，普通 click 開在 browser tab
        if (event.shiftKey) deps.openMiniWindow(uri)
        else deps.openBrowserTab(uri)
      } else {
        window.open(uri, '_blank')
      }
    },
  }
}
