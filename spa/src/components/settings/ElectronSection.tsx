import { useEffect, useRef, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { SettingItem } from './SettingItem'
import { ToggleSwitch } from './ToggleSwitch'

/** Offered "away after" values; main rejects anything else (PA-2 D6). */
const AWAY_MINUTES_OPTIONS = [1, 2, 3, 5, 10, 15, 30, 60]

export function ElectronSection() {
  const t = useI18nStore((s) => s.t)

  // Tray visibility is owned by the main process (app-prefs.json); null until
  // the first IPC read resolves, shown as "on" in the meantime.
  // The IPC may be missing when the dev server hot-loads a newer SPA into an
  // older shell whose preload predates it — disable the row rather than show
  // a switch that cannot do anything.
  const trayApi = window.electronAPI?.tray
  const [showTray, setShowTray] = useState<boolean | null>(null)
  // Rapid clicks fire one IPC each; responses can come back out of order, so
  // only the newest request may write state. Older ones — resolved or
  // rejected — are dropped instead of overwriting the latest intent. 0 means
  // the user has not toggled yet.
  const latestToggleId = useRef(0)
  useEffect(() => {
    let cancelled = false
    // The initial read is stale too if the user toggled before it resolved.
    trayApi?.getVisible()
      .then((v) => { if (!cancelled && latestToggleId.current === 0) setShowTray(v) })
      .catch(() => { /* IPC unavailable — keep the default */ })
    // The main process broadcasts to every window after applying a change,
    // so a toggle in another window's Settings page shows up here too.
    // Optional-chained: a shell whose preload predates this method still
    // gets a working (just non-synced) switch.
    const unsubscribe = trayApi?.onVisibilityChanged?.((v) => { if (!cancelled) setShowTray(v) })
    return () => { cancelled = true; unsubscribe?.() }
  }, [trayApi])

  const toggleTray = async (v: boolean) => {
    if (!trayApi) return
    const id = ++latestToggleId.current
    const prev = showTray
    setShowTray(v)
    try {
      const applied = await trayApi.setVisible(v)
      if (id === latestToggleId.current) setShowTray(applied)
    } catch {
      if (id === latestToggleId.current) setShowTray(prev)
    }
  }

  // Push-only-when-away threshold, owned by the main process (PA-2 D11). Hidden when the shell predates it.
  const presenceApi = window.electronAPI?.presence
  const hasAwayRow = typeof presenceApi?.getAwayMinutes === 'function'
  const [awayMinutes, setAwayMinutes] = useState<number | null>(null)
  const latestAwayId = useRef(0)
  useEffect(() => {
    if (!presenceApi?.getAwayMinutes) return
    let cancelled = false
    presenceApi.getAwayMinutes()
      .then((v) => { if (!cancelled && latestAwayId.current === 0) setAwayMinutes(v) })
      .catch(() => { /* IPC unavailable — the select stays disabled */ })
    return () => { cancelled = true }
  }, [presenceApi])

  const changeAway = async (v: number) => {
    if (!presenceApi?.setAwayMinutes) return
    const id = ++latestAwayId.current
    const prev = awayMinutes
    setAwayMinutes(v)
    try {
      const saved = await presenceApi.setAwayMinutes(v)
      if (id === latestAwayId.current) setAwayMinutes(saved)
    } catch {
      if (id === latestAwayId.current) setAwayMinutes(prev)
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
        {hasAwayRow && (
          <SettingItem label={t('settings.electron.away.label')} description={t('settings.electron.away.desc')}>
            <select
              aria-label={t('settings.electron.away.aria')}
              value={awayMinutes ?? ''}
              disabled={awayMinutes === null}
              onChange={(e) => { void changeAway(Number(e.target.value)) }}
              className="bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 w-28 hover:border-text-muted focus:border-border-active focus:outline-none"
            >
              {awayMinutes === null && <option value="" />}
              {AWAY_MINUTES_OPTIONS.map((m) => (
                <option key={m} value={m}>{t('settings.electron.away.option', { n: m })}</option>
              ))}
            </select>
          </SettingItem>
        )}
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
