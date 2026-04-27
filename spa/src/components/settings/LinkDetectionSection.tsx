import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { SettingItem } from './SettingItem'
import { ToggleSwitch } from './ToggleSwitch'

export function LinkDetectionSection() {
  const linkDetectBareFilename = useUISettingsStore((s) => s.linkDetectBareFilename)
  const setLinkDetectBareFilename = useUISettingsStore((s) => s.setLinkDetectBareFilename)
  const t = useI18nStore((s) => s.t)

  return (
    <div>
      <h3 className="text-sm text-text-primary mt-6 mb-1">{t('settings.terminal.link_detect.title')}</h3>
      <p className="text-xs text-text-secondary mb-3">{t('settings.terminal.link_detect.desc')}</p>

      <SettingItem label={t('settings.terminal.link_detect.bare.label')} description={t('settings.terminal.link_detect.bare.desc')}>
        <ToggleSwitch label={t('settings.terminal.link_detect.bare.label')} checked={linkDetectBareFilename} onChange={setLinkDetectBareFilename} />
      </SettingItem>
    </div>
  )
}
