import { useState, useMemo } from 'react'
import { MagnifyingGlass, FloppyDisk, ArrowCounterClockwise, X } from '@phosphor-icons/react'
import { getLocale } from '../../lib/locale-registry'
import { useI18nStore } from '../../stores/useI18nStore'

type FilterMode = 'all' | 'modified' | 'missing'

interface LocaleEditorProps {
  baseLocaleId: string
  onClose: () => void
}

function getGroup(key: string): string {
  return key.split('.')[0]
}

export function LocaleEditor({ baseLocaleId, onClose }: LocaleEditorProps) {
  const baseLocale = getLocale(baseLocaleId)
  const importLocale = useI18nStore((s) => s.importLocale)
  const setLocale = useI18nStore((s) => s.setLocale)
  const isEditingCustom = useI18nStore((s) => baseLocaleId in s.customLocales)
  const updateCustomLocale = useI18nStore((s) => s.updateCustomLocale)
  const t = useI18nStore((s) => s.t)

  const enTranslations = useMemo(() => getLocale('en')?.translations ?? {}, [])
  const allKeys = useMemo(() => Object.keys(enTranslations).sort(), [enTranslations])

  const [name, setName] = useState(() => isEditingCustom ? (baseLocale?.name ?? 'Custom') : `${baseLocale?.name ?? 'Custom'} (Custom)`)
  const [translations, setTranslations] = useState<Record<string, string>>(
    () => ({ ...enTranslations, ...baseLocale?.translations }),
  )
  const [search, setSearch] = useState('')
  const [filter, setFilter] = useState<FilterMode>('all')

  // Compute counts and filtered keys
  const { filteredKeys, allCount, modifiedCount, missingCount } = useMemo(() => {
    const lowerSearch = search.toLowerCase()
    const baseTranslations = baseLocale?.translations ?? {}

    let modified = 0
    let missing = 0
    const matched: string[] = []

    for (const key of allKeys) {
      const value = translations[key] ?? ''
      const isModified = value !== (baseTranslations[key] ?? enTranslations[key] ?? '')
      const isMissing = value === ''

      if (isModified) modified++
      if (isMissing) missing++

      // Search filter
      if (lowerSearch) {
        const keyMatch = key.toLowerCase().includes(lowerSearch)
        const valueMatch = value.toLowerCase().includes(lowerSearch)
        const enMatch = (enTranslations[key] ?? '').toLowerCase().includes(lowerSearch)
        if (!keyMatch && !valueMatch && !enMatch) continue
      }

      // Tab filter
      if (filter === 'modified' && !isModified) continue
      if (filter === 'missing' && !isMissing) continue

      matched.push(key)
    }

    return { filteredKeys: matched, allCount: allKeys.length, modifiedCount: modified, missingCount: missing }
  }, [allKeys, translations, search, filter, baseLocale, enTranslations])

  // Group filtered keys
  const grouped = useMemo(() => {
    const groups: Record<string, string[]> = {}
    for (const key of filteredKeys) {
      const group = getGroup(key)
      if (!groups[group]) groups[group] = []
      groups[group].push(key)
    }
    return groups
  }, [filteredKeys])

  const handleTranslationChange = (key: string, value: string) => {
    setTranslations((prev) => ({ ...prev, [key]: value }))
  }

  const handleSave = () => {
    if (isEditingCustom) {
      updateCustomLocale(baseLocaleId, { name, translations })
      setLocale(baseLocaleId)
    } else {
      const newId = importLocale({ name, translations })
      setLocale(newId)
    }
    onClose()
  }

  const handleReset = () => {
    setTranslations({ ...enTranslations, ...baseLocale?.translations })
  }

  const isModified = (key: string): boolean => {
    const baseTranslations = baseLocale?.translations ?? {}
    return (translations[key] ?? '') !== (baseTranslations[key] ?? enTranslations[key] ?? '')
  }

  if (!baseLocale) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
      data-testid="locale-editor"
    >
      <div className="bg-surface-primary border border-border-default rounded-lg shadow-lg w-[720px] max-h-[80vh] flex flex-col">
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-border-subtle">
          <h3 className="text-sm font-medium text-text-primary">{t('locale.editor.title')}</h3>
          <button
            aria-label={t('locale.editor.close')}
            onClick={onClose}
            className="p-1 rounded text-text-muted hover:text-text-primary"
          >
            <X size={16} />
          </button>
        </div>

        {/* Name input */}
        <div className="px-4 py-3 border-b border-border-subtle">
          <label className="block text-xs text-text-secondary mb-1">{t('locale.editor.name_label')}</label>
          <input
            type="text"
            aria-label={t('locale.editor.name_aria')}
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="w-full bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 focus:border-border-active focus:outline-none"
          />
        </div>

        {/* Search + Filter tabs */}
        <div className="px-4 py-2 border-b border-border-subtle space-y-2">
          <div className="relative">
            <MagnifyingGlass size={14} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-text-muted" />
            <input
              type="text"
              placeholder={t('locale.editor.search')}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-full bg-surface-input border border-border-default rounded-md text-text-primary text-xs pl-8 pr-3 py-1.5 focus:border-border-active focus:outline-none"
            />
          </div>
          <div className="flex gap-1">
            {(['all', 'modified', 'missing'] as const).map((mode) => {
              const count = mode === 'all' ? allCount : mode === 'modified' ? modifiedCount : missingCount
              const label = t(`locale.editor.filter.${mode}`, { count })
              return (
                <button
                  key={mode}
                  onClick={() => setFilter(mode)}
                  className={`px-2.5 py-1 text-xs rounded-md transition-colors ${
                    filter === mode
                      ? 'bg-accent text-text-inverse'
                      : 'text-text-secondary hover:text-text-primary hover:bg-surface-hover'
                  }`}
                >
                  {label}
                </button>
              )
            })}
          </div>
        </div>

        {/* Translation list */}
        <div className="flex-1 overflow-y-auto px-4 py-2">
          {Object.entries(grouped).map(([group, keys]) => (
            <div key={group} className="mb-3">
              <div className="py-1.5 text-xs font-medium text-text-secondary uppercase tracking-wider">
                {group}
              </div>
              <div className="space-y-1">
                {keys.map((key) => {
                  const modified = isModified(key)
                  return (
                    <div
                      key={key}
                      className={`grid grid-cols-[1fr_1.2fr_1.2fr] gap-2 items-center py-1 px-2 rounded ${
                        modified ? 'border border-green-600/40 bg-green-950/10' : ''
                      }`}
                      data-testid={`locale-key-${key}`}
                    >
                      <span className="text-xs font-mono text-text-muted truncate" title={key}>
                        {key}
                      </span>
                      <span
                        className="text-xs text-text-muted bg-surface-secondary/50 rounded px-2 py-1 truncate"
                        title={enTranslations[key] ?? ''}
                      >
                        {enTranslations[key] ?? ''}
                      </span>
                      <input
                        type="text"
                        value={translations[key] ?? ''}
                        onChange={(e) => handleTranslationChange(key, e.target.value)}
                        className="bg-surface-input border border-border-default rounded text-text-primary text-xs px-2 py-1 w-full focus:border-border-active focus:outline-none"
                        aria-label={key}
                      />
                    </div>
                  )
                })}
              </div>
            </div>
          ))}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between px-4 py-3 border-t border-border-subtle">
          <button
            onClick={handleReset}
            className="flex items-center gap-1 text-xs text-text-secondary hover:text-text-primary"
          >
            <ArrowCounterClockwise size={14} />
            {t('common.reset')}
          </button>
          <div className="flex items-center gap-2">
            <button
              onClick={onClose}
              className="px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary rounded-md border border-border-default"
            >
              {t('common.cancel')}
            </button>
            <button
              onClick={handleSave}
              disabled={!name.trim()}
              className="flex items-center gap-1 px-3 py-1.5 text-xs text-text-inverse bg-accent hover:bg-accent-hover rounded-md disabled:opacity-50 disabled:cursor-not-allowed"
              aria-label={t('locale.editor.save')}
            >
              <FloppyDisk size={14} />
              {t('common.save')}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
