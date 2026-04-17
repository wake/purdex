import { useState } from 'react'
import { DownloadSimple, Upload, Trash, PaintBrush, Translate } from '@phosphor-icons/react'
import { SettingItem } from './SettingItem'
import { getAllThemes } from '../../lib/theme-registry'
import type { ThemeDefinition } from '../../lib/theme-registry'
import { useThemeStore } from '../../stores/useThemeStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { ThemeEditor } from './ThemeEditor'
import { ThemeImportModal } from './ThemeImportModal'
import { getAllLocales } from '../../lib/locale-registry'
import type { LocaleDef } from '../../lib/locale-registry'
import { LocaleEditor } from './LocaleEditor'
import { LocaleImportModal } from './LocaleImportModal'
import { useLayoutStore } from '../../stores/useLayoutStore'
import type { TabPosition } from '../../stores/useLayoutStore'

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

function exportLocale(locale: LocaleDef) {
  const data = JSON.stringify(
    { name: locale.name, baseLocale: locale.id, version: 1, translations: locale.translations },
    null, 2,
  )
  const blob = new Blob([data], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${locale.name}.json`
  a.click()
  URL.revokeObjectURL(url)
}

export function AppearanceSection() {
  const activeThemeId = useThemeStore((s) => s.activeThemeId)
  const setActiveTheme = useThemeStore((s) => s.setActiveTheme)
  const deleteCustomTheme = useThemeStore((s) => s.deleteCustomTheme)
  const t = useI18nStore((s) => s.t)
  const tabPosition = useLayoutStore((s) => s.tabPosition)
  const setTabPosition = useLayoutStore((s) => s.setTabPosition)
  const activeLocaleId = useI18nStore((s) => s.activeLocaleId)
  const setLocale = useI18nStore((s) => s.setLocale)
  const deleteCustomLocale = useI18nStore((s) => s.deleteCustomLocale)
  const [showEditor, setShowEditor] = useState(false)
  const [showImportModal, setShowImportModal] = useState(false)
  const [showLocaleEditor, setShowLocaleEditor] = useState(false)
  const [showLocaleImport, setShowLocaleImport] = useState(false)
  const allThemes = getAllThemes()
  const presetThemes = allThemes.filter((th) => th.builtin)
  const customThemeList = allThemes.filter((th) => !th.builtin)

  const activeTheme = allThemes.find((th) => th.id === activeThemeId)

  const allLocales = getAllLocales()
  const builtinLocales = allLocales.filter((l) => l.builtin)
  const customLocaleList = allLocales.filter((l) => !l.builtin)
  const activeLocale = allLocales.find((l) => l.id === activeLocaleId)

  const handleThemeChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    setActiveTheme(e.target.value)
  }

  const handleDelete = (id: string) => {
    deleteCustomTheme(id)
  }

  const handleImported = (themeId: string) => {
    setActiveTheme(themeId)
  }

  const handleLocaleChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    setLocale(e.target.value)
  }

  const handleLocaleDelete = (id: string) => {
    deleteCustomLocale(id)
  }

  const handleLocaleImported = (localeId: string) => {
    setLocale(localeId)
  }

  const handleTabPositionChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setTabPosition(e.target.value as TabPosition)
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

      {/* Language selector */}
      <SettingItem label={t('settings.appearance.language.label')} description={t('settings.appearance.language.desc')}>
        <div className="flex items-center gap-2">
          <select
            aria-label={t('settings.appearance.language.aria')}
            value={activeLocaleId}
            onChange={handleLocaleChange}
            className="bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 w-40 hover:border-text-muted focus:border-border-active focus:outline-none"
          >
            <optgroup label={t('settings.appearance.language.preset')}>
              {builtinLocales.map((l) => (
                <option key={l.id} value={l.id}>{l.name}</option>
              ))}
            </optgroup>
            {customLocaleList.length > 0 && (
              <optgroup label={t('settings.appearance.language.custom')}>
                {customLocaleList.map((l) => (
                  <option key={l.id} value={l.id}>{l.name}</option>
                ))}
              </optgroup>
            )}
          </select>

          {activeLocale && !activeLocale.builtin && (
            <>
              <button
                aria-label={t('settings.appearance.language.export')}
                onClick={() => exportLocale(activeLocale)}
                className="p-1.5 rounded-md border border-border-default text-text-secondary hover:text-text-primary hover:border-border-active"
                title={t('common.export')}
              >
                <DownloadSimple size={14} />
              </button>
              <button
                aria-label={t('settings.appearance.language.delete')}
                onClick={() => handleLocaleDelete(activeLocale.id)}
                className="p-1.5 rounded-md border border-border-default text-text-secondary hover:text-status-error hover:border-status-error"
                title={t('common.delete')}
              >
                <Trash size={14} />
              </button>
            </>
          )}
        </div>
      </SettingItem>

      <SettingItem
        label={t('settings.appearance.tab_position.label')}
        description={t('settings.appearance.tab_position.desc')}
      >
        <div className="flex flex-col gap-1.5">
          <label className="flex items-center gap-2 text-xs text-text-primary cursor-pointer">
            <input
              type="radio"
              name="tab-position"
              value="top"
              checked={tabPosition === 'top'}
              onChange={handleTabPositionChange}
              className="accent-purple-500"
            />
            {t('settings.appearance.tab_position.top')}
          </label>
          <label className="flex items-center gap-2 text-xs text-text-primary cursor-pointer">
            <input
              type="radio"
              name="tab-position"
              value="left"
              checked={tabPosition === 'left'}
              onChange={handleTabPositionChange}
              className="accent-purple-500"
            />
            {t('settings.appearance.tab_position.left')}
          </label>
          <p className="text-[11px] text-text-muted mt-0.5">
            {t('settings.appearance.tab_position.left_hint')}
          </p>
        </div>
      </SettingItem>

      {/* Locale customize + import */}
      <SettingItem label={t('settings.appearance.locale_customize.label')} description={t('settings.appearance.locale_customize.desc')}>
        <div className="flex items-center gap-2">
          <button
            aria-label={t('settings.appearance.locale_customize.button')}
            onClick={() => setShowLocaleEditor(true)}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active"
          >
            <Translate size={14} />
            {t('settings.appearance.locale_customize.button')}
          </button>
          <button
            aria-label={t('settings.appearance.locale_customize.import')}
            onClick={() => setShowLocaleImport(true)}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-border-default text-text-secondary text-xs hover:text-text-primary hover:border-border-active"
          >
            <Upload size={14} />
            {t('common.import')}
          </button>
        </div>
      </SettingItem>

      {showEditor && (
        <ThemeEditor baseThemeId={activeThemeId} onClose={() => setShowEditor(false)} />
      )}

      {showImportModal && (
        <ThemeImportModal onClose={() => setShowImportModal(false)} onImported={handleImported} />
      )}

      {showLocaleEditor && (
        <LocaleEditor baseLocaleId={activeLocaleId} onClose={() => setShowLocaleEditor(false)} />
      )}

      {showLocaleImport && (
        <LocaleImportModal onClose={() => setShowLocaleImport(false)} onImported={handleLocaleImported} />
      )}
    </div>
  )
}
