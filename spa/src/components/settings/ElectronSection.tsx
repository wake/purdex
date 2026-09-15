import { useEffect, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { SettingItem } from './SettingItem'
import { ToggleSwitch } from './ToggleSwitch'

export function ElectronSection() {
  const t = useI18nStore((s) => s.t)

  // Tray visibility is owned by the main process (app-prefs.json); null until
  // the first IPC read resolves, shown as "on" in the meantime.
  // The IPC may be missing when the dev server hot-loads a newer SPA into an
  // older shell whose preload predates it — disable the row rather than show
  // a switch that cannot do anything.
  const trayApi = window.electronAPI?.tray
  const [showTray, setShowTray] = useState<boolean | null>(null)
  useEffect(() => {
    let cancelled = false
    trayApi?.getVisible()
      .then((v) => { if (!cancelled) setShowTray(v) })
      .catch(() => { /* IPC unavailable — keep the default */ })
    return () => { cancelled = true }
  }, [trayApi])

  const toggleTray = async (v: boolean) => {
    if (!trayApi) return
    const prev = showTray
    setShowTray(v)
    try {
      const applied = await trayApi.setVisible(v)
      setShowTray(applied)
    } catch {
      setShowTray(prev)
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-lg text-text-primary">{t('settings.electron.title')}</h2>
        <p className="text-xs text-text-secondary mb-6">{t('settings.electron.desc')}</p>
      </div>
      <div className="space-y-4">
        <SettingItem label={t('settings.electron.tray.label')} description={t('settings.electron.tray.desc')} disabled={!trayApi}>
          <ToggleSwitch label={t('settings.electron.tray.aria')} checked={showTray ?? true} onChange={toggleTray} />
        </SettingItem>
        <div className="flex items-center justify-between">
          <div>
            <div className="text-sm text-text-primary">{t('settings.electron.idle_timeout.label')}</div>
            <div className="text-xs text-text-secondary">{t('settings.electron.idle_timeout.desc')}</div>
          </div>
          <div className="flex items-center gap-2">
            <input type="number" defaultValue={5} min={1} max={60}
              aria-label={t('settings.electron.idle_timeout.aria')}
              className="w-16 bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-2 py-1 text-center focus:border-border-active focus:outline-none" />
            <span className="text-xs text-text-secondary">min</span>
          </div>
        </div>
        <div className="flex items-center justify-between">
          <div>
            <div className="text-sm text-text-primary">{t('settings.electron.memory_limit.label')}</div>
            <div className="text-xs text-text-secondary">{t('settings.electron.memory_limit.desc')}</div>
          </div>
          <div className="flex items-center gap-2">
            <input type="number" defaultValue={512} min={128} max={4096} step={128}
              aria-label={t('settings.electron.memory_limit.aria')}
              className="w-16 bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-2 py-1 text-center focus:border-border-active focus:outline-none" />
            <span className="text-xs text-text-secondary">MB</span>
          </div>
        </div>
        <div className="flex items-center justify-between">
          <div>
            <div className="text-sm text-text-primary">{t('settings.electron.max_bg.label')}</div>
            <div className="text-xs text-text-secondary">{t('settings.electron.max_bg.desc')}</div>
          </div>
          <div className="flex items-center gap-2">
            <input type="number" defaultValue={3} min={0} max={20}
              aria-label={t('settings.electron.max_bg.aria')}
              className="w-16 bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-2 py-1 text-center focus:border-border-active focus:outline-none" />
            <span className="text-xs text-text-secondary">views</span>
          </div>
        </div>
      </div>
    </div>
  )
}
