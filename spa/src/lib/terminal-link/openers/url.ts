import type { LinkOpener } from '../types'

export interface UrlOpenerDeps {
  isElectron: boolean
  openBrowserTab: (url: string) => void
  openMiniWindow: (url: string) => void
}

export function createUrlOpener(deps: UrlOpenerDeps): LinkOpener {
  return {
    id: 'builtin:url',
    // priority 0 = builtin default，讓第三方 opener 以更高 priority 覆寫
    priority: 0,
    canOpen: (token) => token.type === 'url',
    open: (token, _ctx, event) => {
      const uri = token.text
      // 兜底檢查 scheme：即使第三方 matcher 誤產 token，也不會把 `javascript:` / `data:` 傳到 window.open
      if (!uri.startsWith('http://') && !uri.startsWith('https://')) return
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
