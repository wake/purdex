// spa/src/components/settings/profile/ProfileAppearanceEditor.tsx — a local profile's name, icon and colour
// (P3 plan, "P3d-1 — As built": every profile, the master included, has the three). Everything is written
// through `setProfileAppearance`, which refuses a whole patch over one bad value and says which.
//
// The name follows `DeviceNameField`: while there is an uncommitted edit the input shows the draft, otherwise it
// mirrors the store — so after a save it shows what was KEPT (`normalizeLocalProfileName`), not what was typed.
// The icon follows `HostIconField`: the app's one icon picker, inline in a `FloatingPanel`.
import { useRef, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { MASTER_PROFILE_ID, normalizeLocalProfileName, useLocalProfilesStore, type ProfileAppearancePatch } from '../../../stores/useLocalProfilesStore'
import { HOST_COLOR_PRESETS, normalizeHostColor } from '../../../lib/host-color'
import { FloatingPanel } from '../../FloatingPanel'
import { WorkspaceIconPicker } from '../../../features/workspace/components/WorkspaceIconPicker'
import { ProfileIcon } from '../../../features/workspace/components/ProfileSwitcher'
import { canTintProfile } from './profile-rules'

type Reason = 'not-found' | 'bad-name' | 'bad-icon' | 'bad-weight' | 'bad-color'

const REASON_KEY: Record<Reason, string> = {
  'not-found': 'settings.profile.local.error.not_found',
  'bad-name': 'settings.profile.local.error.bad_name',
  'bad-icon': 'settings.profile.local.error.bad_icon',
  'bad-weight': 'settings.profile.local.error.bad_weight',
  'bad-color': 'settings.profile.local.error.bad_color',
}

const LABEL = 'w-16 shrink-0 text-xs text-text-secondary'
const BTN =
  'shrink-0 rounded-md border border-border-default px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed'
const INPUT = 'bg-surface-secondary border border-border-default rounded px-2 py-1 text-xs text-text-primary disabled:opacity-50'

export function ProfileAppearanceEditor({ id }: { id: string }) {
  const t = useI18nStore((s) => s.t)
  const isMaster = id === MASTER_PROFILE_ID
  const profile = useLocalProfilesStore((s) => (isMaster ? s.master : s.slaves[id]))
  const [draft, setDraft] = useState('')
  const [dirty, setDirty] = useState(false)
  const [hexDraft, setHexDraft] = useState<string | null>(null)
  const [pickerOpen, setPickerOpen] = useState(false)
  const [reason, setReason] = useState<Reason | null>(null)
  const iconRowRef = useRef<HTMLDivElement>(null)

  if (profile === undefined) return null

  /** True when it was kept. Read at call time: the action is the store's, whatever it is by then. */
  const apply = (patch: ProfileAppearancePatch): boolean => {
    const r = useLocalProfilesStore.getState().setProfileAppearance(id, patch)
    setReason(r.ok ? null : r.reason)
    return r.ok
  }

  const storedName = profile.name ?? ''
  const kept = normalizeLocalProfileName(draft)
  const commitName = () => {
    if (!dirty) return
    // Blank is the master's way back to `Home`; for a slave the store answers `bad-name`.
    if (apply({ name: kept })) setDirty(false)
  }

  const tintable = canTintProfile(profile)
  const commitHex = () => {
    if (hexDraft === null || hexDraft.trim() === '') return setHexDraft(null)
    // An unparsable value goes to the store as it is: `bad-color` is the store's word, not a second rule here.
    if (apply({ color: normalizeHostColor(hexDraft) ?? hexDraft })) setHexDraft(null)
  }

  return (
    <div data-testid={`profile-edit-${id}`} className="mt-2 flex flex-col gap-2 rounded-md border border-border-subtle px-3 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className={LABEL}>{t('settings.profile.local.name')}</span>
        <input
          type="text"
          aria-label={t('settings.profile.local.name')}
          data-testid="profile-edit-name"
          placeholder={isMaster ? t('nav.home') : undefined}
          spellCheck={false}
          value={dirty ? draft : storedName}
          onChange={(e) => {
            setDraft(e.target.value)
            setDirty(true)
          }}
          onKeyDown={(e) => {
            if (e.nativeEvent.isComposing) return
            if (e.key === 'Enter') commitName()
            else if (e.key === 'Escape') {
              setDirty(false)
              setReason(null)
            }
          }}
          className={`${INPUT} w-48`}
        />
        <button type="button" data-testid="profile-edit-name-save" disabled={!dirty} onClick={commitName} className={BTN}>
          {t('common.save')}
        </button>
        {dirty && kept !== null && kept !== draft && (
          <span data-testid="profile-edit-name-preview" className="text-xs text-text-muted">
            {t('settings.profile.local.name_preview', { name: kept })}
          </span>
        )}
      </div>

      <div ref={iconRowRef} className="flex flex-wrap items-center gap-2">
        <span className={LABEL}>{t('settings.profile.local.icon')}</span>
        <button
          type="button"
          data-testid="profile-edit-icon"
          aria-label={t('settings.profile.local.icon_change')}
          title={t('settings.profile.local.icon_change')}
          aria-expanded={pickerOpen}
          onClick={() => setPickerOpen((v) => !v)}
          className="w-7 h-7 rounded border border-border-default flex items-center justify-center text-text-secondary hover:text-text-primary hover:border-text-muted cursor-pointer"
        >
          <ProfileIcon appearance={profile} size={18} />
        </button>
        <button
          type="button"
          data-testid="profile-edit-icon-default"
          disabled={profile.icon === undefined}
          onClick={() => {
            apply({ icon: null })
            setPickerOpen(false)
          }}
          className={BTN}
        >
          {t('settings.profile.local.icon_default')}
        </button>
        {pickerOpen && (
          <FloatingPanel title={t('settings.profile.local.icon_change')} anchorRef={iconRowRef} onClose={() => setPickerOpen(false)} width={360}>
            <WorkspaceIconPicker
              inline
              currentIcon={profile.icon}
              currentWeight={profile.iconWeight}
              onSelect={(name) => {
                apply({ icon: name === '' ? null : name })
                setPickerOpen(false)
              }}
              // A weight belongs to an icon: with the logo showing there is none to give it to.
              onWeightChange={(weight) => {
                if (profile.icon !== undefined) apply({ iconWeight: weight })
              }}
              onCancel={() => setPickerOpen(false)}
            />
          </FloatingPanel>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <span className={LABEL}>{t('settings.profile.local.color')}</span>
        <div className="flex flex-wrap items-center gap-1.5">
          {HOST_COLOR_PRESETS.map((hex) => (
            <button
              key={hex}
              type="button"
              data-testid={`profile-edit-color-${hex}`}
              aria-label={hex}
              aria-pressed={profile.color === hex}
              disabled={!tintable}
              onClick={() => apply({ color: hex })}
              className={`w-[18px] h-[18px] rounded cursor-pointer border border-border-default disabled:opacity-50 disabled:cursor-not-allowed ${
                profile.color === hex ? 'ring-2 ring-offset-1 ring-offset-surface-primary ring-text-primary' : ''
              }`}
              style={{ background: hex }}
            />
          ))}
        </div>
        <input
          type="text"
          aria-label={t('settings.profile.local.color_hex')}
          data-testid="profile-edit-color-hex"
          placeholder="#rrggbb"
          spellCheck={false}
          disabled={!tintable}
          value={hexDraft ?? profile.color ?? ''}
          onChange={(e) => setHexDraft(e.target.value)}
          onBlur={commitHex}
          onKeyDown={(e) => {
            if (e.nativeEvent.isComposing) return
            if (e.key === 'Enter') commitHex()
            else if (e.key === 'Escape') {
              setHexDraft(null)
              setReason(null)
            }
          }}
          className={`${INPUT} w-24 font-mono`}
        />
        <button
          type="button"
          data-testid="profile-edit-color-none"
          disabled={!tintable || profile.color === undefined}
          onClick={() => {
            setHexDraft(null)
            apply({ color: null })
          }}
          className={BTN}
        >
          {t('settings.profile.local.color_none')}
        </button>
      </div>
      {!tintable && (
        <p data-testid="profile-edit-color-needs-icon" className="text-xs text-text-muted">
          {t('settings.profile.local.color_needs_icon')}
        </p>
      )}

      {reason !== null && (
        <p data-testid="profile-edit-error" data-reason={reason} className="text-xs text-red-500">
          {t(REASON_KEY[reason])}
        </p>
      )}
    </div>
  )
}
