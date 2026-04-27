import { useUISettingsStore } from '../../../stores/useUISettingsStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { SettingItem } from '../SettingItem'
import { ToggleSwitch } from '../ToggleSwitch'

export function EditorLinkDetectionSection() {
  const linkDetectAbsolute = useUISettingsStore((s) => s.linkDetectAbsolute)
  const setLinkDetectAbsolute = useUISettingsStore((s) => s.setLinkDetectAbsolute)
  const linkDetectTilde = useUISettingsStore((s) => s.linkDetectTilde)
  const setLinkDetectTilde = useUISettingsStore((s) => s.setLinkDetectTilde)
  const linkDetectRelativeSlash = useUISettingsStore((s) => s.linkDetectRelativeSlash)
  const setLinkDetectRelativeSlash = useUISettingsStore((s) => s.setLinkDetectRelativeSlash)
  const t = useI18nStore((s) => s.t)

  return (
    <div>
      <h3 className="text-sm text-text-primary mt-6 mb-1">{t('settings.editor.link_detect.title')}</h3>
      <p className="text-xs text-text-secondary mb-3">{t('settings.editor.link_detect.desc')}</p>

      <SettingItem label={t('settings.editor.link_detect.absolute.label')} description={t('settings.editor.link_detect.absolute.desc')}>
        <ToggleSwitch label={t('settings.editor.link_detect.absolute.label')} checked={linkDetectAbsolute} onChange={setLinkDetectAbsolute} />
      </SettingItem>

      <SettingItem label={t('settings.editor.link_detect.tilde.label')} description={t('settings.editor.link_detect.tilde.desc')}>
        <ToggleSwitch label={t('settings.editor.link_detect.tilde.label')} checked={linkDetectTilde} onChange={setLinkDetectTilde} />
      </SettingItem>

      <SettingItem label={t('settings.editor.link_detect.relative_slash.label')} description={t('settings.editor.link_detect.relative_slash.desc')}>
        <ToggleSwitch label={t('settings.editor.link_detect.relative_slash.label')} checked={linkDetectRelativeSlash} onChange={setLinkDetectRelativeSlash} />
      </SettingItem>
    </div>
  )
}
