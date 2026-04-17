import { useUISettingsStore, type TerminalRenderer, KEEPALIVE_MAX_WEBGL, KEEPALIVE_MAX_DOM } from '../../stores/useUISettingsStore'
import { useAgentStore, type TabIndicatorStyle, type CcIconVariant } from '../../stores/useAgentStore'
import { CC_ICON_VARIANTS } from '../../lib/agent-icons'
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

  const tabIndicatorStyle = useAgentStore((s) => s.tabIndicatorStyle)
  const setTabIndicatorStyle = useAgentStore((s) => s.setTabIndicatorStyle)
  const ccIconVariant = useAgentStore((s) => s.ccIconVariant)
  const setCcIconVariant = useAgentStore((s) => s.setCcIconVariant)

  const t = useI18nStore((s) => s.t)

  const RENDERER_OPTIONS = [
    { value: 'webgl' as TerminalRenderer, label: t('settings.terminal.renderer.webgl') },
    { value: 'dom' as TerminalRenderer, label: t('settings.terminal.renderer.dom') },
  ]

  const TAB_INDICATOR_OPTIONS: { value: TabIndicatorStyle; label: string }[] = [
    { value: 'iconOnly', label: t('settings.terminal.tab_indicator.icon_only') },
    { value: 'dotOnly', label: t('settings.terminal.tab_indicator.dot_only') },
    { value: 'sideBySide', label: t('settings.terminal.tab_indicator.side_by_side') },
    { value: 'overlay', label: t('settings.terminal.tab_indicator.overlay') },
  ]

  const CC_ICON_OPTIONS: { value: CcIconVariant; label: string }[] = [
    { value: 'bot', label: t('settings.terminal.cc_icon.bot') },
    { value: 'star', label: t('settings.terminal.cc_icon.star') },
  ]

  // Atomic: renderer + version + optional keepAlive clamp in one setState()
  const handleRenderer = (r: TerminalRenderer) => {
    useUISettingsStore.setState((s) => ({
      terminalRenderer: r,
      terminalSettingsVersion: s.terminalSettingsVersion + 1,
      ...(r === 'webgl' && s.keepAliveCount > KEEPALIVE_MAX_WEBGL
        ? { keepAliveCount: KEEPALIVE_MAX_WEBGL }
        : {}),
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
        <div className="flex items-center gap-2">
          <input
            type="number"
            aria-label={t('settings.terminal.keepalive.aria')}
            min={0}
            max={renderer === 'webgl' ? KEEPALIVE_MAX_WEBGL : KEEPALIVE_MAX_DOM}
            step={1}
            value={keepAliveCount}
            onChange={(e) => {
              const max = renderer === 'webgl' ? KEEPALIVE_MAX_WEBGL : KEEPALIVE_MAX_DOM
              setKeepAliveCount(clamp(Number(e.target.value) || 0, 0, max))
            }}
            className="bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 w-20 hover:border-text-muted focus:border-border-active focus:outline-none"
          />
          {renderer === 'webgl' && (
            <span className="text-xs text-text-muted">{t('settings.terminal.keepalive.webgl_hint')}</span>
          )}
        </div>
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

      <SettingItem label={t('settings.terminal.tab_indicator.label')} description={t('settings.terminal.tab_indicator.desc')}>
        <SegmentControl options={TAB_INDICATOR_OPTIONS} value={tabIndicatorStyle} onChange={setTabIndicatorStyle} />
      </SettingItem>

      <SettingItem label={t('settings.terminal.cc_icon.label')} description={t('settings.terminal.cc_icon.desc')}>
        <div className="flex gap-2">
          {CC_ICON_OPTIONS.map((opt) => {
            const VariantIcon = CC_ICON_VARIANTS[opt.value]
            const isActive = opt.value === ccIconVariant
            return (
              <button
                key={opt.value}
                type="button"
                onClick={() => { if (!isActive) setCcIconVariant(opt.value) }}
                className={`flex items-center gap-2 px-3 py-1.5 rounded-md border text-xs transition-colors cursor-pointer ${
                  isActive
                    ? 'bg-surface-elevated border-border-active text-text-primary'
                    : 'bg-transparent border-border-default text-text-muted hover:text-text-primary hover:border-text-muted'
                }`}
                aria-pressed={isActive}
              >
                <VariantIcon size={16} />
                <span>{opt.label}</span>
              </button>
            )
          })}
        </div>
      </SettingItem>
    </div>
  )
}
