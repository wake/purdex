// spa/src/components/settings/profile/LocalProfileRow.tsx — one local profile in Settings › Profile: what it
// looks like, whether it is the master, whether it is the one on screen, and what can be done with it.
import { useState } from 'react'
import { ArrowDown, ArrowUp, ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { MASTER_PROFILE_ID, type ProfileAppearance } from '../../../stores/useLocalProfilesStore'
import { ProfileIcon } from '../../../features/workspace/components/ProfileSwitcher'
import { ConfirmDialog } from '../../ConfirmDialog'
import { ProfileAppearanceEditor } from './ProfileAppearanceEditor'

const BTN =
  'shrink-0 rounded-md border border-border-default px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed'
const ICON_BTN = `${BTN} flex items-center`
const BADGE = 'rounded bg-surface-secondary px-1.5 py-0.5 text-[10px] text-text-secondary'

export interface LocalProfileRowProps {
  id: string
  /** As shown: the master's is `Home` until it has one. */
  name: string
  look: ProfileAppearance
  onScreen: boolean
  /** A switch is under way (any target): no second one. */
  switchPending: boolean
  /** … and it is to this profile. */
  switchingHere: boolean
  /** Slaves only: where it may still move. */
  canMoveUp: boolean
  canMoveDown: boolean
  onSwitch: (id: string) => void
  onMove: (id: string, by: -1 | 1) => void
  onDelete: (id: string) => void
}

export function LocalProfileRow({ id, name, look, onScreen, switchPending, switchingHere, canMoveUp, canMoveDown, onSwitch, onMove, onDelete }: LocalProfileRowProps) {
  const t = useI18nStore((s) => s.t)
  const [editing, setEditing] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const isMaster = id === MASTER_PROFILE_ID

  return (
    <li
      data-testid={`profile-row-${id}`}
      data-master={isMaster ? 'true' : 'false'}
      data-on-screen={onScreen ? 'true' : 'false'}
      className="border-t border-border-default py-2 text-xs"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-1.5 text-text-primary">
          <ProfileIcon appearance={look} size={14} />
          {/* A name is the user's own text: shown as is, never through t(). */}
          <span data-testid={`profile-row-name-${id}`} title={name} className="truncate">{name}</span>
          {isMaster && <span data-testid="profile-row-master-badge" className={BADGE}>{t('profile.master')}</span>}
          {onScreen && <span data-testid={`profile-row-on-screen-${id}`} className={BADGE}>{t('settings.profile.local.on_screen')}</span>}
        </div>
        <div className="flex items-center gap-2">
          {!onScreen && (
            <button
              type="button"
              data-testid={`profile-row-switch-${id}`}
              aria-busy={switchingHere}
              disabled={switchPending}
              onClick={() => onSwitch(id)}
              className={`${BTN} flex items-center gap-1.5`}
            >
              {switchingHere && <ArrowsClockwise size={12} className="animate-spin" />}
              {t('settings.profile.local.switch')}
            </button>
          )}
          <button type="button" data-testid={`profile-row-edit-${id}`} aria-expanded={editing} onClick={() => setEditing((v) => !v)} className={BTN}>
            {editing ? t('common.close') : t('common.edit')}
          </button>
          {!isMaster && (
            <>
              <button type="button" data-testid={`profile-row-up-${id}`} aria-label={t('settings.profile.local.move_up')} title={t('settings.profile.local.move_up')} disabled={!canMoveUp} onClick={() => onMove(id, -1)} className={ICON_BTN}>
                <ArrowUp size={12} />
              </button>
              <button type="button" data-testid={`profile-row-down-${id}`} aria-label={t('settings.profile.local.move_down')} title={t('settings.profile.local.move_down')} disabled={!canMoveDown} onClick={() => onMove(id, 1)} className={ICON_BTN}>
                <ArrowDown size={12} />
              </button>
              {/* Never the one on screen (`deleteSlave` would refuse it): its world is in the tab stores, not parked. */}
              <button type="button" data-testid={`profile-row-delete-${id}`} disabled={onScreen} onClick={() => setConfirmDelete(true)} className={BTN}>
                {t('common.delete')}
              </button>
            </>
          )}
        </div>
      </div>

      {!isMaster && onScreen && (
        <p data-testid={`profile-row-delete-blocked-${id}`} className="mt-0.5 text-text-muted">
          {t('settings.profile.local.delete_blocked')}
        </p>
      )}

      {editing && <ProfileAppearanceEditor id={id} />}

      {confirmDelete && (
        <ConfirmDialog
          testIdPrefix="profile-delete"
          title={t('settings.profile.local.delete_title', { name })}
          body={t('settings.profile.local.delete_body')}
          confirmLabel={t('common.delete')}
          onCancel={() => setConfirmDelete(false)}
          onConfirm={() => {
            setConfirmDelete(false)
            onDelete(id)
          }}
        />
      )}
    </li>
  )
}
