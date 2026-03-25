import { useState, useRef } from 'react'
import { X, Upload, LinkSimple, ClipboardText } from '@phosphor-icons/react'
import { useThemeStore, type ThemeImportPayload } from '../../stores/useThemeStore'
import { parseAndValidate } from '../../lib/theme-import'
import { useI18nStore } from '../../stores/useI18nStore'

interface ThemeImportModalProps {
  onClose: () => void
  onImported: (themeId: string) => void
}

type ImportTab = 'paste' | 'file' | 'url'

export function ThemeImportModal({ onClose, onImported }: ThemeImportModalProps) {
  const [activeTab, setActiveTab] = useState<ImportTab>('paste')
  const [jsonText, setJsonText] = useState('')
  const [urlText, setUrlText] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)

  const importTheme = useThemeStore((s) => s.importTheme)
  const t = useI18nStore((s) => s.t)

  const handleImport = (payload: ThemeImportPayload) => {
    try {
      const id = importTheme(payload)
      onImported(id)
      onClose()
    } catch (e) {
      setError(e instanceof Error ? e.message : t('error.import.failed'))
    }
  }

  const handlePasteImport = () => {
    setError(null)
    let parsed: unknown
    try {
      parsed = JSON.parse(jsonText)
    } catch {
      setError(t('error.json.invalid'))
      return
    }
    const result = parseAndValidate(parsed)
    if (typeof result === 'string') {
      setError(result)
      return
    }
    handleImport(result)
  }

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setError(null)
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => {
      let parsed: unknown
      try {
        parsed = JSON.parse(reader.result as string)
      } catch {
        setError(t('error.json.invalid_file'))
        return
      }
      const result = parseAndValidate(parsed)
      if (typeof result === 'string') {
        setError(result)
        return
      }
      handleImport(result)
    }
    reader.onerror = () => setError(t('error.file.read_failed'))
    reader.readAsText(file)
  }

  const handleUrlFetch = async () => {
    setError(null)
    if (!urlText.trim()) {
      setError(t('error.url.empty'))
      return
    }
    setLoading(true)
    try {
      const response = await fetch(urlText.trim())
      if (!response.ok) {
        setError(t('error.url.http', { status: response.status, statusText: response.statusText }))
        setLoading(false)
        return
      }
      let parsed: unknown
      try {
        parsed = await response.json()
      } catch {
        setError(t('error.url.not_json'))
        setLoading(false)
        return
      }
      const result = parseAndValidate(parsed)
      if (typeof result === 'string') {
        setError(result)
        setLoading(false)
        return
      }
      handleImport(result)
      // Don't setLoading(false) — component will unmount via onClose()
    } catch (e) {
      const msg = e instanceof Error ? e.message : t('error.import.failed')
      setError(msg.includes('Failed to fetch') ? t('error.url.fetch_failed') : msg)
      setLoading(false)
    }
  }

  const tabs: { id: ImportTab; label: string; icon: React.ReactNode }[] = [
    { id: 'paste', label: t('theme.import.tab.paste'), icon: <ClipboardText size={14} /> },
    { id: 'file', label: t('theme.import.tab.file'), icon: <Upload size={14} /> },
    { id: 'url', label: t('theme.import.tab.url'), icon: <LinkSimple size={14} /> },
  ]

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      data-testid="theme-import-modal"
    >
      <div className="bg-surface-primary border border-border-default rounded-lg shadow-lg w-[480px] flex flex-col">
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-border-subtle">
          <h3 className="text-sm font-medium text-text-primary">{t('theme.import.title')}</h3>
          <button
            aria-label={t('theme.import.close')}
            onClick={onClose}
            className="p-1 rounded text-text-muted hover:text-text-primary"
          >
            <X size={16} />
          </button>
        </div>

        {/* Tabs */}
        <div className="flex border-b border-border-subtle">
          {tabs.map((tab) => (
            <button
              key={tab.id}
              onClick={() => { setActiveTab(tab.id); setError(null) }}
              className={`flex items-center gap-1.5 px-4 py-2 text-xs border-b-2 ${
                activeTab === tab.id
                  ? 'border-accent text-text-primary'
                  : 'border-transparent text-text-muted hover:text-text-secondary'
              }`}
            >
              {tab.icon}
              {tab.label}
            </button>
          ))}
        </div>

        {/* Content */}
        <div className="px-4 py-4 min-h-[160px]">
          {activeTab === 'paste' && (
            <div className="space-y-3">
              <textarea
                aria-label={t('theme.import.paste.aria')}
                value={jsonText}
                onChange={(e) => setJsonText(e.target.value)}
                placeholder={t('theme.import.paste.placeholder')}
                className="w-full h-32 bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-2 font-mono resize-none focus:border-border-active focus:outline-none"
              />
              <button
                onClick={handlePasteImport}
                className="px-4 py-1.5 text-xs text-text-inverse bg-accent hover:bg-accent-hover rounded-md"
              >
                {t('common.import')}
              </button>
            </div>
          )}

          {activeTab === 'file' && (
            <div className="space-y-3">
              <div
                className="flex flex-col items-center justify-center h-32 border-2 border-dashed border-border-default rounded-md cursor-pointer hover:border-border-active"
                onClick={() => fileInputRef.current?.click()}
              >
                <Upload size={24} className="text-text-muted mb-2" />
                <span className="text-xs text-text-secondary">{t('theme.import.file.hint')}</span>
              </div>
              <input
                ref={fileInputRef}
                type="file"
                accept=".json"
                onChange={handleFileChange}
                className="hidden"
                aria-label={t('theme.import.file.aria')}
              />
            </div>
          )}

          {activeTab === 'url' && (
            <div className="space-y-3">
              <input
                type="url"
                aria-label={t('theme.import.url.aria')}
                value={urlText}
                onChange={(e) => setUrlText(e.target.value)}
                placeholder={t('theme.import.url.placeholder')}
                className="w-full bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 font-mono focus:border-border-active focus:outline-none"
              />
              <button
                onClick={handleUrlFetch}
                disabled={loading}
                className="px-4 py-1.5 text-xs text-text-inverse bg-accent hover:bg-accent-hover rounded-md disabled:opacity-50"
              >
                {loading ? t('theme.import.url.fetching') : t('theme.import.url.fetch_button')}
              </button>
            </div>
          )}

          {/* Error display */}
          {error && (
            <p className="mt-3 text-xs text-status-error" data-testid="import-error">{error}</p>
          )}
        </div>
      </div>
    </div>
  )
}
