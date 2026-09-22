// spa/src/components/settings/profile/SotProfilesBlock.tsx — Settings › Profile › the profiles on the master's
// host (P3 plan Task 7, block 3): rename, delete. Shown only while a master is attached — it takes a host to
// ask, and until the wizard (P3d-3) the attached one is the only host this page knows of.
//
// DELETE IS ENABLED BY THE FETCHED INDEX, AND DECIDED BY THE DAEMON. The button is on only where the list that
// was fetched shows nobody attached — and never for the profile THIS device syncs with, listed or not (its
// attachment may not have been written yet): stop sync first. The list can be old, so the daemon has the last
// word: a 409 `attached` is rendered as the devices it names, and the list is fetched again.
//
// AN ACTION BELONGS TO THE MASTER IT WAS STARTED UNDER. The master is every window's to change, so `hostId` can
// move under an open confirmation — and `p2` on host B is not the `p2` the user was asked about on host A. So:
//   - the moment `hostId` or the attached profile changes, every open action (the delete confirmation, the rename
//     editor, the refusal and status lines) is dropped — in the same render, before anything can be clicked;
//   - what is sent is checked once more against the page as it is NOW: the scope it was opened under, and that the
//     profile is still in the fetched list. Otherwise nothing is sent, and that is said;
//   - an answer that arrives after the scope moved is for a page that is gone: it sets nothing.
// The delete's half of this lives in useSotDelete.ts, shared with the wizard's step 2 (#1325).
import { useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { renameProfile } from '../../../lib/profile/api'
import type { Attachment, ProfileIndexEntry } from '../../../lib/profile/api'
import { ConfirmDialog } from '../../ConfirmDialog'
import { sotScopeOf } from './profile-rules'
import { useSotDelete } from './useSotDelete'
import type { SotProfilesView } from './useSotProfiles'

const BTN =
  'shrink-0 rounded-md border border-border-default px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed'
const BADGE = 'rounded bg-surface-secondary px-1.5 py-0.5 text-[10px] text-text-secondary'

const message = (e: unknown): string => (e instanceof Error ? e.message : String(e))
const deviceNames = (attachments: Attachment[]): string => attachments.map((a) => a.deviceName).join(', ')

export interface SotProfilesBlockProps {
  hostId: string
  /** The profile this device syncs with. */
  attachedProfileId: string
  view: SotProfilesView
  reload: () => void
}

export function SotProfilesBlock({ hostId, attachedProfileId, view, reload }: SotProfilesBlockProps) {
  const t = useI18nStore((s) => s.t)
  const [renaming, setRenaming] = useState<{ under: string; id: string; name: string } | null>(null)
  // The delete — its confirmation, refusal, the scope guard — is `useSotDelete`'s; `busy`, `status` and the scope
  // checks are shared with the rename, and a scope change or a stale send drops the rename editor with the rest.
  const scope = sotScopeOf(hostId, attachedProfileId)
  const del = useSotDelete({
    hostId,
    attachedProfileId,
    scope,
    view,
    reload,
    failedText: (f) => t('settings.profile.sot.delete_failed', { message: f.message }),
    onDrop: () => setRenaming(null),
  })
  const { busy, setBusy, status, setStatus, stillValid, isLive } = del

  const state = view.kind === 'rows' && view.rows.length === 0 ? 'empty' : view.kind

  const rename = async (row: ProfileIndexEntry) => {
    const name = renaming?.name.trim() ?? ''
    if (renaming === null || busy || name === '' || name === row.name) return
    // The scope the editor was OPENED under — not this render's, which is by construction the current one.
    const under = renaming.under
    if (!stillValid(under, row.id)) return
    setBusy(true)
    setStatus(null)
    try {
      const r = await renameProfile(hostId, row.id, name)
      if (!isLive(under)) return
      if (r.kind === 'ok') {
        setRenaming(null)
        reload()
      } else setStatus(t('settings.profile.sot.rename_failed', { message: r.message }))
    } catch (e) {
      if (isLive(under)) setStatus(t('settings.profile.sot.rename_failed', { message: message(e) }))
    } finally {
      if (isLive(under)) setBusy(false)
    }
  }

  return (
    <section data-testid="profile-sot-block" data-state={state} className="mt-8">
      <div className="mb-2 flex items-center justify-between">
        <h3 className="text-sm text-text-primary">{t('settings.profile.sot.title')}</h3>
        <button
          type="button"
          data-testid="profile-sot-refresh"
          onClick={reload}
          className="flex items-center gap-1.5 rounded-md border border-border-default px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer"
        >
          <ArrowsClockwise size={14} />
          {t('settings.profile.sot.refresh')}
        </button>
      </div>
      <p className="mb-2 text-xs text-text-secondary">{t('settings.profile.sot.desc')}</p>

      {view.kind === 'loading' && (
        <p data-testid="profile-sot-loading" className="text-xs text-text-muted">{t('settings.profile.sot.loading')}</p>
      )}

      {view.kind === 'error' && (
        <p data-testid="profile-sot-error" className="flex flex-wrap items-center gap-2 text-xs text-red-500">
          <span>{t('settings.profile.sot.load_failed', { message: view.message })}</span>
          <button type="button" data-testid="profile-sot-retry" onClick={reload} className={BTN}>
            {t('settings.profile.sot.refresh')}
          </button>
        </p>
      )}

      {state === 'empty' && (
        <p data-testid="profile-sot-empty" className="text-xs text-text-muted">{t('settings.profile.sot.empty')}</p>
      )}

      {view.kind === 'rows' && view.rows.length > 0 && (
        <ul className="flex flex-col">
          {view.rows.map((row) => {
            const isCurrent = row.id === attachedProfileId
            const blocked = del.blocked(row)
            const draft = renaming?.id === row.id ? renaming.name : null
            return (
              <li key={row.id} data-testid={`profile-sot-row-${row.id}`} className="border-t border-border-default py-2 text-xs">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex min-w-0 items-center gap-1.5 text-text-primary">
                    <span data-testid={`profile-sot-name-${row.id}`} title={row.name} className="truncate">{row.name}</span>
                    {isCurrent && <span data-testid={`profile-sot-current-${row.id}`} className={BADGE}>{t('settings.profile.sot.current')}</span>}
                  </div>
                  <div className="flex items-center gap-2">
                    <button
                      type="button"
                      data-testid={`profile-sot-rename-${row.id}`}
                      disabled={busy}
                      onClick={() => setRenaming(draft === null ? { under: scope, id: row.id, name: row.name } : null)}
                      className={BTN}
                    >
                      {t('settings.profile.sot.rename')}
                    </button>
                    <button
                      type="button"
                      data-testid={`profile-sot-delete-${row.id}`}
                      disabled={busy || blocked !== null}
                      onClick={() => del.ask(row)}
                      className={BTN}
                    >
                      {t('common.delete')}
                    </button>
                  </div>
                </div>

                {row.attachments.length > 0 && (
                  <p data-testid={`profile-sot-devices-${row.id}`} className="mt-0.5 text-text-muted">
                    {t('settings.profile.sot.devices', { names: deviceNames(row.attachments) })}
                  </p>
                )}
                {blocked !== null && (
                  <p data-testid={`profile-sot-delete-blocked-${row.id}`} className="mt-0.5 text-text-muted">
                    {t(`settings.profile.sot.delete_blocked_${blocked}`)}
                  </p>
                )}
                {del.refused?.id === row.id && (
                  <p data-testid={`profile-sot-attached-${row.id}`} className="mt-0.5 text-status-warning">
                    {del.refused.attachments.length > 0
                      ? t('settings.profile.sot.attached', { names: deviceNames(del.refused.attachments) })
                      : t('settings.profile.sot.attached_none')}
                  </p>
                )}

                {draft !== null && (
                  <div role="group" className="mt-2 flex flex-wrap items-center gap-2">
                    <input
                      type="text"
                      autoFocus
                      aria-label={t('settings.profile.sot.rename')}
                      data-testid="profile-sot-rename-input"
                      spellCheck={false}
                      value={draft}
                      onChange={(e) => setRenaming({ under: renaming?.under ?? scope, id: row.id, name: e.target.value })}
                      onKeyDown={(e) => {
                        if (e.nativeEvent.isComposing) return
                        if (e.key === 'Enter') void rename(row)
                        else if (e.key === 'Escape') setRenaming(null)
                      }}
                      className="bg-surface-secondary border border-border-default rounded px-2 py-1 text-xs text-text-primary w-48"
                    />
                    <button
                      type="button"
                      data-testid="profile-sot-rename-save"
                      disabled={busy || draft.trim() === '' || draft.trim() === row.name}
                      onClick={() => void rename(row)}
                      className={BTN}
                    >
                      {t('common.save')}
                    </button>
                    <button type="button" data-testid="profile-sot-rename-cancel" onClick={() => setRenaming(null)} className={BTN}>
                      {t('common.cancel')}
                    </button>
                  </div>
                )}
              </li>
            )
          })}
        </ul>
      )}

      {status !== null && (
        <p data-testid="profile-sot-status" className="mt-2 text-xs text-red-500">{status}</p>
      )}

      {del.confirm && (
        <ConfirmDialog
          testIdPrefix="profile-sot-delete"
          busy={busy}
          title={t('settings.profile.sot.delete_title', { name: del.confirm.row.name })}
          body={t('settings.profile.sot.delete_body')}
          confirmLabel={t('common.delete')}
          onCancel={del.cancel}
          onConfirm={() => void del.remove()}
        />
      )}
    </section>
  )
}
