import { useState } from 'react'
import { DownloadSimple, Upload, Trash, PaintBrush } from '@phosphor-icons/react'
import { SettingItem } from './SettingItem'
import { getAllThemes } from '../../lib/theme-registry'
import type { ThemeDefinition } from '../../lib/theme-registry'
import { useThemeStore } from '../../stores/useThemeStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { ThemeEditor } from './ThemeEditor'
import { ThemeImportModal } from './ThemeImportModal'

function exportTheme(theme: ThemeDefinition) {
  const data = JSON.stringify({ name: theme.name, tokens: theme.tokens }, null, 2)
  const blob = new Blob([data], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${theme.name}.json`
  a.click()
  URL.revokeObjectURL(url)
}

export function AppearanceSection() {
  const activeThemeId = useThemeStore((s) => s.activeThemeId)
  const setActiveTheme = useThemeStore((s) => s.setActiveTheme)
  const deleteCustomTheme = useThemeStore((s) => s.deleteCustomTheme)
  const t = useI18nStore((s) => s.t)
  const [showEditor, setShowEditor] = useState(false)
  const [showImportModal, setShowImportModal] = useState(false)

  const allThemes = getAllThemes()
  const presetThemes = allThemes.filter((th) => th.builtin)
  const customThemeList = allThemes.filter((th) => !th.builtin)

  const activeTheme = allThemes.find((th) => th.id === activeThemeId)

  const handleThemeChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    setActiveTheme(e.target.value)
  }

  const handleDelete = (id: string) => {
    deleteCustomTheme(id)
  }

  const handleImported = (themeId: string) => {
    setActiveTheme(themeId)
  }

  return (
    <div>
      <h2 className="text-lg text-text-primary">{t('settings.appearance.title')}</h2>
      <p className="text-xs text-text-secondary mb-6">{t('settings.appearance.desc')}</p>

      <SettingItem label={t('settings.appearance.theme.label')} description={t('settings.appearance.theme.desc')}>
        <div className="flex items-center gap-2">
          <select
            aria-label={t('settings.appearance.theme.aria')}
            value={activeThemeId}
            onChange={handleThemeChange}
            className="bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 w-40 hover:border-text-muted focus:border-border-active focus:outline-none"
          >
            <optgroup label={t('settings.appearance.theme.preset')}>
              {presetThemes.map((th) => (
                <option key={th.id} value={th.id}>{th.name}</option>
              ))}
            </optgroup>
            {customThemeList.length > 0 && (
              <optgroup label={t('settings.appearance.theme.custom')}>
                {customThemeList.map((th) => (
                  <option key={th.id} value={th.id}>{th.name}</option>
                ))}
              </optgroup>
            )}
          </select>

          {activeTheme && !activeTheme.builtin && (
            <>
              <button
                aria-label={t('settings.appearance.theme.export')}
                onClick={() => exportTheme(activeTheme)}
                className="p-1.5 rounded-md border border-border-default text-text-secondary hover:text-text-primary hover:border-border-active"
                title={t('common.export')}
              >
                <DownloadSimple size={14} />
              </button>
              <button
                aria-label={t('settings.appearance.theme.delete')}
                onClick={() => handleDelete(activeTheme.id)}
                className="p-1.5 rounded-md border border-border-default text-text-secondary hover:text-status-error hover:border-status-error"
                title={t('common.delete')}
              >
                <Trash size={14} />
              </button>
            </>
          )}
        </div>
      </SettingItem>

      <SettingItem label={t('settings.appearance.customize.label')} description={t('settings.appearance.customize.desc')}>
        <div className="flex items-center gap-2">
          <button
            aria-label={t('settings.appearance.customize.button')}
            onClick={() => setShowEditor(true)}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active"
          >
            <PaintBrush size={14} />
            {t('settings.appearance.customize.button')}
          </button>
          <button
            aria-label={t('settings.appearance.customize.import')}
            onClick={() => setShowImportModal(true)}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active"
          >
            <Upload size={14} />
            {t('common.import')}
          </button>
        </div>
      </SettingItem>

      <SettingItem label="Language" description="Interface language" disabled>
        <select
          disabled
          className="bg-surface-input border border-border-default rounded-md text-text-secondary text-xs px-3 py-1.5 w-40"
          defaultValue="zh-TW"
        >
          <option value="zh-TW">繁體中文</option>
          <option value="en">English</option>
        </select>
      </SettingItem>

      {showEditor && (
        <ThemeEditor baseThemeId={activeThemeId} onClose={() => setShowEditor(false)} />
      )}

      {showImportModal && (
        <ThemeImportModal onClose={() => setShowImportModal(false)} onImported={handleImported} />
      )}
    </div>
  )
}
