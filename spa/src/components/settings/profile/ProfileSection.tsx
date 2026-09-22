// spa/src/components/settings/profile/ProfileSection.tsx — Settings › Profile (Profile Sync spec §4.9, P3 plan
// P3d-2): the profiles of this device, and the state of the one that syncs.
//
// OPENING THIS PAGE WRITES NOTHING AND STARTS NOTHING. With no master and no local profile it is static text and
// one list row; every store it reads is one the app has already created (THE IRON RULE, lib/profile/start.ts).
// Only with a master attached does it ask anybody anything: that master's host, once, for its profiles.
import { useI18nStore } from '../../../stores/useI18nStore'
import { useProfileStore } from '../../../stores/useProfileStore'
import { CurrentBlock } from './CurrentBlock'
import { DeviceNameField } from './DeviceNameField'
import { LocalProfilesBlock } from './LocalProfilesBlock'
import { SotProfilesBlock } from './SotProfilesBlock'
import { useSotProfiles } from './useSotProfiles'

export function ProfileSection() {
  const t = useI18nStore((s) => s.t)
  // `selectMaster`'s rule, field by field (as a selector it would hand over a fresh object every time): a master
  // is the three together.
  const hostId = useProfileStore((s) => (s.masterProfileId !== null && s.masterEndpoint !== null ? s.masterHostId : null))
  const profileId = useProfileStore((s) => s.masterProfileId)
  const sot = useSotProfiles(hostId)
  // The snapshot names the master by id; its NAME is the host's to tell, and only this list asks the host.
  const masterName = sot.view?.kind === 'rows' ? (sot.view.rows.find((row) => row.id === profileId)?.name ?? null) : null

  return (
    <div data-testid="profile-section">
      <h2 className="text-lg text-text-primary">{t('settings.section.profile')}</h2>
      <p className="text-xs text-text-secondary">{t('settings.profile.description')}</p>
      <DeviceNameField />
      <CurrentBlock masterName={masterName} />
      <LocalProfilesBlock />
      {hostId !== null && profileId !== null && sot.view !== null && (
        <SotProfilesBlock hostId={hostId} attachedProfileId={profileId} view={sot.view} reload={sot.reload} />
      )}
    </div>
  )
}
