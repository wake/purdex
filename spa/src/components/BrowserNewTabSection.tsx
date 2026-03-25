import { useState } from 'react'
import { Globe } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import type { NewTabProviderProps } from '../lib/new-tab-registry'

export function BrowserNewTabSection({ onSelect }: NewTabProviderProps) {
  const t = useI18nStore((s) => s.t)
  const [url, setUrl] = useState('')

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (!url.trim()) return
    const finalUrl = url.includes('://') ? url : `https://${url}`
    onSelect({ kind: 'browser', url: finalUrl })
  }

  return (
    <form onSubmit={handleSubmit} className="flex items-center gap-2 px-2">
      <Globe size={16} className="text-text-muted flex-shrink-0" />
      <input
        type="text"
        value={url}
        onChange={(e) => setUrl(e.target.value)}
        placeholder={t('browser.url_placeholder')}
        className="flex-1 bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 focus:border-border-active focus:outline-none"
      />
    </form>
  )
}
