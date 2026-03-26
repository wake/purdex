import { useI18nStore } from '../stores/useI18nStore'

interface BrowserPaneProps {
  paneId: string
  url: string
}

export function BrowserPane({ paneId, url }: BrowserPaneProps) {
  const t = useI18nStore((s) => s.t)

  // Electron mode: placeholder div for WebContentsView (implemented in Electron plan)
  // SPA mode: disabled message
  if (!window.electronAPI) {
    return (
      <div className="flex-1 flex items-center justify-center">
        <p className="text-sm text-text-muted">{t('browser.requires_app')}</p>
      </div>
    )
  }

  return (
    <div
      className="w-full h-full"
      data-browser-pane={paneId}
      data-browser-url={url}
    />
  )
}
