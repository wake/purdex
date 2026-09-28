import { SettingItem } from './SettingItem'
import { useI18nStore } from '../../stores/useI18nStore'
import { useWorkerSettingsStore } from '../../stores/useWorkerSettingsStore'
import { listWorkerThemes } from '../../lib/worker-theme/registry'

// Worker pane spec §4.2 — "Worker → Appearance": today just the theme select
// (one option, `purdex`); the icon options (§8.3) are added in phase C.
export function WorkerSettingsSection() {
  const t = useI18nStore((s) => s.t)
  const theme = useWorkerSettingsStore((s) => s.theme)
  const setTheme = useWorkerSettingsStore((s) => s.setTheme)
  const themes = listWorkerThemes()

  return (
    <div>
      <h2 className="text-lg text-text-primary">{t('settings.worker.title')}</h2>
      <p className="text-xs text-text-secondary mb-6">{t('settings.worker.desc')}</p>

      <SettingItem label={t('worker.theme.label')} description={t('worker.theme.desc')}>
        <select
          aria-label={t('worker.theme.label')}
          value={theme}
          onChange={(e) => setTheme(e.target.value)}
          className="bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 w-40 hover:border-text-muted focus:border-border-active focus:outline-none"
        >
          {themes.map((th) => (
            <option key={th.id} value={th.id}>{t(th.labelKey)}</option>
          ))}
        </select>
      </SettingItem>
    </div>
  )
}
