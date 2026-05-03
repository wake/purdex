import { useI18nStore } from '../../stores/useI18nStore'

// Spec §I5 — view-only placeholder for disableable modules that have no
// global (purdex-scope) settings to expose. Browser and Files use this so
// every disableable module carries an entry in the Settings sidebar
// (spec §I1), keeping the Modules Switchboard ↔ sidebar mental model
// consistent. Subscribes only to `t`; no store writes, no other store
// subscriptions.
export function PlaceholderSettingsSection() {
  const t = useI18nStore((s) => s.t)
  return (
    <div className="space-y-4">
      <p className="text-sm text-text-secondary">
        {t('settings.module.no_purdex_settings')}
      </p>
    </div>
  )
}
