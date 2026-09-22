// spa/src/components/settings/profile/ResolveRow.tsx — one locked section: why it is locked, the ways out its kind
// has, and the confirmation every one of them goes through (P3d-4 plan: the Resolve block, R1, R2).
//
//   locked:conflict  both changed it since they last agreed   Keep this device's / Take the host's
//   locked:reset     the host's copy was recreated            Keep this device's / Take the host's
//   locked:invalid   why this device refuses the host's copy  Keep this device's ONLY (the reducer refuses the other)
//
// WHAT IS FROZEN AND CHECKED — the master's tag, its endpoint, the lock — and everything that must go through that
// check (the counts' host read, the send, "sent" and its TTL) is `useResolveContext`'s (review A4). This file only
// displays what the hook answers.
import { useI18nStore } from '../../../stores/useI18nStore'
import type { InvalidReason } from '../../../lib/profile/apply-to-stores'
import type { SectionLock } from '../../../lib/profile/executor'
import type { SectionView } from '../../../lib/profile/sync-view'
import { ConfirmDialog } from '../../ConfirmDialog'
import type { HostSide, LocalSide, SideCount } from './resolve-counts'
import { useResolveContext } from './useResolveContext'

const BTN =
  'shrink-0 flex items-center gap-1.5 rounded-md border border-border-default px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed'
const BADGE = 'rounded bg-surface-secondary px-1.5 py-0.5 text-[10px] text-text-secondary'
/** A state the user should know of, not a fault: the tone of the Current block's notices. */
const NOTICE = 'text-xs text-yellow-500'

interface Props {
  sectionKey: string
  kind: SectionView['kind']
  label: string
  lock: SectionLock
  invalidReason: InvalidReason | null
  fromLeader: boolean
  disabled: boolean
}

export function ResolveRow({ sectionKey, kind, label, lock, invalidReason, fromLeader, disabled }: Props) {
  const t = useI18nStore((s) => s.t)
  const { open, local, host, changed, outcome, ask, cancel, confirm } = useResolveContext(sectionKey, lock)

  const why = (): { reason?: string; text: string } => {
    if (lock.status === 'locked:conflict') return { text: t('settings.profile.resolve.why.conflict') }
    if (lock.status === 'locked:reset') return { text: t('settings.profile.resolve.why.reset') }
    const reason = invalidReason ?? 'unknown'
    return { reason, text: `${t(`settings.profile.resolve.why.invalid.${reason.replace(/-/g, '_')}`)} ${t('settings.profile.resolve.invalid_only')}` }
  }

  const countText = (side: LocalSide | HostSide | null): { state: string; text: string } => {
    if (side === null) return { state: 'loading', text: t('settings.profile.resolve.count_loading') }
    const count: SideCount = side.count
    if (count.state === 'unreadable' || kind === 'other') return { state: 'unreadable', text: t('settings.profile.resolve.count_unreadable') }
    return { state: 'read', text: t(`settings.profile.resolve.unit.${kind}`, { count: count.count }) }
  }

  // A conflict whose local side moved since it arose: keeping this device's restores what was SENT.
  const undoesOf = (l: SectionLock): boolean => l.status === 'locked:conflict' && l.conflict !== null && l.currentHash !== l.conflict.localHash
  const undoes = undoesOf(lock)
  const pending = outcome?.state === 'sent'
  const reason = why()
  const localCount = countText(local)
  const hostCount = countText(host)

  return (
    <li
      data-testid={`profile-resolve-row-${sectionKey}`}
      data-section={sectionKey}
      data-lock={lock.status}
      data-source={fromLeader ? 'leader' : 'this-window'}
      className="flex flex-col gap-1 border-t border-border-default py-1.5 text-xs"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="flex items-center gap-2">
          <span title={sectionKey} className="text-text-primary">{label}</span>
          {fromLeader && (
            <span data-testid={`profile-resolve-source-${sectionKey}`} className={BADGE}>{t('settings.profile.current.from_leader')}</span>
          )}
        </span>
        <span className="flex items-center gap-2">
          <button type="button" data-testid={`profile-resolve-keep-local-${sectionKey}`} disabled={disabled || pending} onClick={() => ask('local')} className={BTN}>
            {t('settings.profile.resolve.keep_local')}
          </button>
          {lock.status !== 'locked:invalid' && (
            <button type="button" data-testid={`profile-resolve-take-sot-${sectionKey}`} disabled={disabled || pending} onClick={() => ask('sot')} className={BTN}>
              {t('settings.profile.resolve.take_sot')}
            </button>
          )}
        </span>
      </div>
      <p data-testid={`profile-resolve-why-${sectionKey}`} data-reason={reason.reason} className="text-text-secondary">{reason.text}</p>
      {undoes && <p data-testid={`profile-resolve-undoes-${sectionKey}`} className={NOTICE}>{t('settings.profile.resolve.undoes')}</p>}
      {changed && <p data-testid={`profile-resolve-changed-${sectionKey}`} className={NOTICE}>{t('settings.profile.resolve.changed')}</p>}
      {outcome !== null && (
        <p data-testid={`profile-resolve-sent-${sectionKey}`} data-state={outcome.state} className={outcome.state === 'sent' ? 'text-text-secondary' : NOTICE}>
          {t(`settings.profile.resolve.${outcome.state.replace('-', '_')}`)}
        </p>
      )}
      {open !== null && (
        <ConfirmDialog
          testIdPrefix="profile-resolve"
          title={t(open.keep === 'local' ? 'settings.profile.resolve.keep_local_title' : 'settings.profile.resolve.take_sot_title', { section: label })}
          body={t(open.keep === 'local' ? 'settings.profile.resolve.keep_local_body' : 'settings.profile.resolve.take_sot_body')}
          confirmLabel={t(open.keep === 'local' ? 'settings.profile.resolve.keep_local' : 'settings.profile.resolve.take_sot')}
          onCancel={cancel}
          onConfirm={confirm}
        >
          <ul className="mt-2 flex flex-col gap-0.5 text-xs text-text-secondary">
            <li data-testid="profile-resolve-count-local" data-state={localCount.state}>
              {t('settings.profile.resolve.count_local', { what: localCount.text })}
            </li>
            <li data-testid="profile-resolve-count-sot" data-state={hostCount.state}>
              {t('settings.profile.resolve.count_sot', { what: hostCount.text })}
            </li>
          </ul>
          {open.keep === 'local' && undoesOf(open.ctx.lock) && (
            <p data-testid="profile-resolve-dialog-undoes" className={`mt-2 ${NOTICE}`}>{t('settings.profile.resolve.dialog_undoes')}</p>
          )}
          {local !== null && local.changedSince && (
            <p data-testid="profile-resolve-local-moved" className={`mt-2 ${NOTICE}`}>{t('settings.profile.resolve.local_moved')}</p>
          )}
          {host !== null && host.movedOn && (
            <p data-testid="profile-resolve-sot-moved" className={`mt-2 ${NOTICE}`}>{t('settings.profile.resolve.sot_moved')}</p>
          )}
        </ConfirmDialog>
      )}
    </li>
  )
}
