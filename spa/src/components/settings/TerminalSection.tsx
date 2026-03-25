import { useUISettingsStore, type TerminalRenderer } from '../../stores/useUISettingsStore'
import { SettingItem } from './SettingItem'
import { SegmentControl } from './SegmentControl'
import { ToggleSwitch } from './ToggleSwitch'
import { useI18nStore } from '../../stores/useI18nStore'

function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, value))
}

export function TerminalSection() {
  const renderer = useUISettingsStore((s) => s.terminalRenderer)

  const keepAliveCount = useUISettingsStore((s) => s.keepAliveCount)
  const setKeepAliveCount = useUISettingsStore((s) => s.setKeepAliveCount)
  const keepAlivePinned = useUISettingsStore((s) => s.keepAlivePinned)
  const setKeepAlivePinned = useUISettingsStore((s) => s.setKeepAlivePinned)

  const revealDelay = useUISettingsStore((s) => s.terminalRevealDelay)
  const setRevealDelay = useUISettingsStore((s) => s.setTerminalRevealDelay)

  const t = useI18nStore((s) => s.t)

  const RENDERER_OPTIONS = [
    { value: 'webgl' as TerminalRenderer, label: t('settings.terminal.renderer.webgl') },
    { value: 'dom' as TerminalRenderer, label: t('settings.terminal.renderer.dom') },
  ]

  const handleRenderer = (r: TerminalRenderer) => {
    // Atomic: update renderer + bump version in single set() to avoid intermediate state
    useUISettingsStore.setState((s) => ({
      terminalRenderer: r,
      terminalSettingsVersion: s.terminalSettingsVersion + 1,
    }))
  }

  return (
    <div>
      <h2 className="text-lg text-text-primary">{t('settings.terminal.title')}</h2>
      <p className="text-xs text-text-secondary mb-6">{t('settings.terminal.desc')}</p>

      <SettingItem label={t('settings.terminal.renderer.label')} description={t('settings.terminal.renderer.desc')}>
        <SegmentControl options={RENDERER_OPTIONS} value={renderer} onChange={handleRenderer} />
      </SettingItem>

      <SettingItem label={t('settings.terminal.keepalive.label')} description={t('settings.terminal.keepalive.desc')}>
        <input
          type="number"
          aria-label={t('settings.terminal.keepalive.aria')}
          min={0}
          max={10}
          step={1}
          value={keepAliveCount}
          onChange={(e) => setKeepAliveCount(clamp(Number(e.target.value) || 0, 0, 10))}
          className="bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 w-20 hover:border-text-muted focus:border-border-active focus:outline-none"
        />
      </SettingItem>

      <SettingItem label={t('settings.terminal.keepalive_pinned.label')} description={t('settings.terminal.keepalive_pinned.desc')}>
        <ToggleSwitch label={t('settings.terminal.keepalive_pinned.label')} checked={keepAlivePinned} onChange={setKeepAlivePinned} />
      </SettingItem>

      <SettingItem label={t('settings.terminal.reveal_delay.label')} description={t('settings.terminal.reveal_delay.desc')}>
        <input
          type="number"
          aria-label={t('settings.terminal.reveal_delay.aria')}
          min={0}
          max={2000}
          step={50}
          value={revealDelay}
          onChange={(e) => setRevealDelay(clamp(Number(e.target.value) || 0, 0, 2000))}
          className="bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 w-20 hover:border-text-muted focus:border-border-active focus:outline-none"
        />
      </SettingItem>
    </div>
  )
}
