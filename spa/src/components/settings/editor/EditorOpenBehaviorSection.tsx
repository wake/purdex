import { useEditorSettingsStore } from '../../../stores/useEditorSettingsStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { SettingItem } from '../SettingItem'
import { ToggleSwitch } from '../ToggleSwitch'

/**
 * Editor "open behavior" preferences (P5).
 *
 * Lives under `useEditorSettingsStore` rather than `useUISettingsStore`
 * because per the project's `feedback_core_vs_module_settings` rule
 * Editor-owned UI preferences belong to the editor settings slice; only
 * truly cross-cutting UI prefs (theme / layout / i18n) sit in the global
 * UI store.
 */
export function EditorOpenBehaviorSection() {
  const popupOnMissingFile = useEditorSettingsStore((s) => s.popupOnMissingFile)
  const setPopupOnMissingFile = useEditorSettingsStore((s) => s.setPopupOnMissingFile)
  const autoSearchLayer1 = useEditorSettingsStore((s) => s.autoSearchLayer1)
  const setAutoSearchLayer1 = useEditorSettingsStore((s) => s.setAutoSearchLayer1)
  const t = useI18nStore((s) => s.t)

  return (
    <div>
      <h3 className="text-sm text-text-primary mt-6 mb-1">{t('settings.editor.open_behavior.title')}</h3>
      <p className="text-xs text-text-secondary mb-3">{t('settings.editor.open_behavior.desc')}</p>

      <SettingItem
        label={t('settings.editor.open_behavior.popup.label')}
        description={t('settings.editor.open_behavior.popup.desc')}
      >
        <ToggleSwitch
          label={t('settings.editor.open_behavior.popup.label')}
          checked={popupOnMissingFile}
          onChange={setPopupOnMissingFile}
        />
      </SettingItem>

      <SettingItem
        label={t('settings.editor.open_behavior.auto_layer1.label')}
        description={t('settings.editor.open_behavior.auto_layer1.desc')}
      >
        <ToggleSwitch
          label={t('settings.editor.open_behavior.auto_layer1.label')}
          checked={autoSearchLayer1}
          onChange={setAutoSearchLayer1}
        />
      </SettingItem>
    </div>
  )
}
