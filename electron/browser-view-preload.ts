import { ipcRenderer } from 'electron'

document.addEventListener(
  'click',
  (e: MouseEvent) => {
    const link = (e.target as HTMLElement).closest('a[href]') as HTMLAnchorElement | null
    if (!link) return
    const href = link.href
    if (!href || href.startsWith('javascript:')) return

    if (e.shiftKey || link.target === '_blank') {
      e.preventDefault()
      ipcRenderer.send('browser-view:link-click', {
        url: href,
        shiftKey: e.shiftKey,
        targetBlank: link.target === '_blank',
      })
    }
    // Normal links: don't intercept, let will-navigate handle
  },
  true,
)
