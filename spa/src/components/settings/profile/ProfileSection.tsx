// spa/src/components/settings/profile/ProfileSection.tsx — Settings › Profile (Profile Sync spec §4.9, P3 plan
// P3d-2): the profiles of this device, and the state of the one that syncs.
//
// OPENING THIS PAGE WRITES NOTHING AND STARTS NOTHING. With no master and no local profile it is static text and
// one list row; every store it reads is one the app has already created (THE IRON RULE, lib/profile/start.ts).
import { useI18nStore } from '../../../stores/useI18nStore'

export function ProfileSection() {
  const t = useI18nStore((s) => s.t)
  return (
    <div data-testid="profile-section">
      <h2 className="text-lg text-text-primary">{t('settings.section.profile')}</h2>
      <p className="text-xs text-text-secondary mb-6">{t('settings.profile.description')}</p>
    </div>
  )
}
