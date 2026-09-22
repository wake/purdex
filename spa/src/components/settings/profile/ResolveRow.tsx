// spa/src/components/settings/profile/ResolveRow.tsx — one locked section: why it is locked, the ways out its kind
// has, and the confirmation every one of them goes through (P3d-4 plan: the Resolve block, R1, R2).
//
//   locked:conflict  both changed it since they last agreed   Keep this device's / Take the host's
//   locked:reset     the host's copy was recreated            Keep this device's / Take the host's
//   locked:invalid   why this device refuses the host's copy  Keep this device's ONLY (the reducer refuses the other)
//
// THE CONFIRMATION IS BOUND TO THE LOCK IT WAS OPENED WITH. The row's `SectionLock` is frozen at the click; the
// counts are read for it once (resolve-counts.ts); confirming hands `requestResolve` that very lock — which whoever
// leads executes only if its lock is still that one (sync-status.ts). When the live lock stops being `sameLock` with
// the frozen one, the dialog closes ITSELF and the row says "this changed while you were deciding": the leader would
// drop the stale command anyway, and this makes that visible instead of silent.
//
// "SENT" ENDS (R2). The channel has no answer: `requestResolve` says only whether the command was handed over. A
// command that was not → "could not be sent". One that was → "sent" until the lock for this key changes (the answer),
// or until the command's own TTL (`COMMAND_TTL_MS`) has passed → "no answer; try again". That is the only timer on
// the page: one per sent row, cleared when the lock changes or the row goes.
import { useEffect, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import type { InvalidReason } from '../../../lib/profile/apply-to-stores'
import type { SectionLock } from '../../../lib/profile/executor'
import { requestResolve } from '../../../lib/profile/start'
import { COMMAND_TTL_MS, sameLock } from '../../../lib/profile/sync-status'
import type { SectionView } from '../../../lib/profile/sync-view'
import { ConfirmDialog } from '../../ConfirmDialog'
import { readHostSide, readLocalSide, type HostSide, type LocalSide, type SideCount } from './resolve-counts'

const BTN =
  'shrink-0 flex items-center gap-1.5 rounded-md border border-border-default px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed'
const BADGE = 'rounded bg-surface-secondary px-1.5 py-0.5 text-[10px] text-text-secondary'
/** A state the user should know of, not a fault: the tone of the Current block's notices. */
const NOTICE = 'text-xs text-yellow-500'

type Keep = 'local' | 'sot'

interface Props {
  master: { hostId: string; profileId: string }
  sectionKey: string
  kind: SectionView['kind']
  label: string
  lock: SectionLock
  invalidReason: InvalidReason | null
  fromLeader: boolean
  disabled: boolean
}

type Outcome = { state: 'sent' | 'no-answer' | 'not-sent'; lock: SectionLock }

export function ResolveRow({ master, sectionKey, kind, label, lock, invalidReason, fromLeader, disabled }: Props) {
  const t = useI18nStore((s) => s.t)
  /** The confirmation, and the lock it was opened with — frozen. */
  const [open, setOpen] = useState<{ keep: Keep; lock: SectionLock } | null>(null)
  const [local, setLocal] = useState<LocalSide | null>(null)
  const [host, setHost] = useState<HostSide | null>(null)
  const [changed, setChanged] = useState(false)
  const [outcome, setOutcome] = useState<Outcome | null>(null)

  // The live lock moved: a confirmation of the old one closes itself, and a "sent" for it is over (it was answered —
  // or overtaken; either way the row now shows the lock as it is). Adjusted during render: no frame shows the old one.
  if (open !== null && !sameLock(open.lock, lock)) {
    setOpen(null)
    setChanged(true)
  }
  if (outcome !== null && !sameLock(outcome.lock, lock)) setOutcome(null)

  // Each side read ONCE per confirmation, for its frozen lock. An answer after it closed sets nothing.
  useEffect(() => {
    if (open === null) return
    let live = true
    const abort = new AbortController()
    void readLocalSide(master.profileId, sectionKey, open.lock).then((side) => {
      if (live) setLocal(side)
    })
    void readHostSide(master.hostId, master.profileId, sectionKey, open.lock, abort.signal).then((side) => {
      if (live) setHost(side)
    })
    return () => {
      live = false
      abort.abort()
    }
  }, [open, master.hostId, master.profileId, sectionKey])

  // The one timer: a "sent" that nothing answered by the command's TTL.
  useEffect(() => {
    if (outcome === null || outcome.state !== 'sent') return
    const sent = outcome
    const timer = setTimeout(() => setOutcome((now) => (now === sent ? { ...sent, state: 'no-answer' } : now)), COMMAND_TTL_MS)
    return () => clearTimeout(timer)
  }, [outcome])

  const ask = (keep: Keep): void => {
    setChanged(false)
    setLocal(null)
    setHost(null)
    setOpen({ keep, lock })
  }

  const confirm = (): void => {
    if (open === null) return
    const handed = requestResolve(sectionKey, open.keep, open.lock)
    setOutcome({ state: handed ? 'sent' : 'not-sent', lock: open.lock })
    setOpen(null)
  }

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
          onCancel={() => setOpen(null)}
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
          {open.keep === 'local' && undoesOf(open.lock) && (
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
