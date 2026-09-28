import { useState } from 'react'
import { SettingItem } from './SettingItem'
import { useI18nStore } from '../../stores/useI18nStore'
import { useWorkerSettingsStore, type WorkerIconStyle } from '../../stores/useWorkerSettingsStore'
import { getWorkerTheme, listWorkerThemes } from '../../lib/worker-theme/registry'
import { WorkspaceIcon } from '../../features/workspace/components/WorkspaceIcon'
import { WorkspaceIconPicker } from '../../features/workspace/components/WorkspaceIconPicker'

const ICON_STYLES: WorkerIconStyle[] = ['mono', 'color', 'custom']

const SELECT_CLASS =
  'bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 w-40 hover:border-text-muted focus:border-border-active focus:outline-none'

// Worker pane spec §4.2 — "Worker → Appearance": the theme select and the
// worker tab icon (§8.3 / L2: provider logo mono / colour, or a custom
// Phosphor icon picked with the workspace icon picker).
export function WorkerSettingsSection() {
  const t = useI18nStore((s) => s.t)
  const theme = useWorkerSettingsStore((s) => s.theme)
  const setTheme = useWorkerSettingsStore((s) => s.setTheme)
  const iconStyle = useWorkerSettingsStore((s) => s.iconStyle)
  const setIconStyle = useWorkerSettingsStore((s) => s.setIconStyle)
  const customIcon = useWorkerSettingsStore((s) => s.customIcon)
  const setCustomIcon = useWorkerSettingsStore((s) => s.setCustomIcon)
  const [picking, setPicking] = useState(false)
  const themes = listWorkerThemes()
  // An unregistered persisted id (e.g. synced from a peer that has more themes) shows the theme it renders as.
  const selected = getWorkerTheme(theme).id

  return (
    <div>
      <h2 className="text-lg text-text-primary">{t('settings.worker.title')}</h2>
      <p className="text-xs text-text-secondary mb-6">{t('settings.worker.desc')}</p>

      <SettingItem label={t('worker.theme.label')} description={t('worker.theme.desc')}>
        <select
          aria-label={t('worker.theme.label')}
          value={selected}
          onChange={(e) => setTheme(e.target.value)}
          className={SELECT_CLASS}
        >
          {themes.map((th) => (
            <option key={th.id} value={th.id}>{t(th.labelKey)}</option>
          ))}
        </select>
      </SettingItem>

      <SettingItem label={t('worker.icon.label')} description={t('worker.icon.desc')}>
        <div className="flex items-center gap-2">
          {iconStyle === 'custom' && (
            <button
              type="button"
              data-testid="worker-icon-picker-toggle"
              aria-expanded={picking}
              title={t('worker.icon.pick')}
              onClick={() => setPicking((p) => !p)}
              className="w-8 h-8 rounded-md flex items-center justify-center bg-surface-tertiary text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
            >
              {customIcon
                ? <WorkspaceIcon icon={customIcon} name="" size={16} />
                : <span className="text-xs">…</span>}
            </button>
          )}
          <select
            aria-label={t('worker.icon.label')}
            value={iconStyle}
            onChange={(e) => setIconStyle(e.target.value as WorkerIconStyle)}
            className={SELECT_CLASS}
          >
            {ICON_STYLES.map((style) => (
              <option key={style} value={style}>{t(`worker.icon.${style}`)}</option>
            ))}
          </select>
        </div>
      </SettingItem>

      {iconStyle === 'custom' && picking && (
        <div className="mt-2 max-w-sm">
          <WorkspaceIconPicker
            currentIcon={customIcon || undefined}
            onSelect={(icon) => { setCustomIcon(icon); setPicking(false) }}
            onCancel={() => setPicking(false)}
            inline
          />
        </div>
      )}
    </div>
  )
}
