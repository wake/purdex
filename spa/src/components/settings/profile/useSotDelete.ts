// spa/src/components/settings/profile/useSotDelete.ts — deleting a profile on a SOT host, for the two places that
// offer it: Settings › Profile's host block (SotProfilesBlock, while a master is attached) and the wizard's step 2
// (issue #1325: after every device has stopped syncing there is no master, and no other way to delete one).
//
// THE RULES, IN ONE PLACE:
//   - offered only where the FETCHED index shows nobody attached, and never for the profile this device syncs with
//     (`sotDeleteBlocked`). The list can be old: a 409 `attached` is kept as the devices it names, for that row;
//   - after any answer but a failure the list is fetched again (deleted, or refused because the index was old);
//   - AN ACTION BELONGS TO THE SCOPE IT WAS OPENED UNDER. The caller says what the scope is (the host and the
//     attached profile; in the wizard, the host and the step). A render under another scope drops every open
//     action — in that render, before anything can be clicked; a send is checked once more against the scope and
//     the list as they are NOW (`sotActionStillValid`); an answer that arrives after the scope moved sets nothing.
//
// `busy` and `status` are the caller's too: Settings' rename shares them (and `stillValid` / `isLive`), and says a
// failure in its own words. What a failed delete SAYS is the caller's (`failedText`): Settings shows the host's
// own words, the wizard a sentence chosen by the failure's class.
import { useRef, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { deleteProfile } from '../../../lib/profile/api'
import type { Attachment, FailureReason, ProfileIndexEntry } from '../../../lib/profile/api'
import { sotActionStillValid, sotDeleteBlocked } from './profile-rules'
import type { SotProfilesView } from './useSotProfiles'

export interface SotDeleteFailure {
  reason: FailureReason | 'thrown'
  /** The transport's text or the thrown error's message: the caller decides whether it is shown. */
  message: string
}

export interface SotDeleteOptions {
  hostId: string
  /** The profile this device syncs with; null when none. */
  attachedProfileId: string | null
  /** What an open action belongs to (`sotScopeOf`, `wizardSotScopeOf`). */
  scope: string
  view: SotProfilesView | null
  reload: () => void
  failedText: (failure: SotDeleteFailure) => string
  /** The caller's own open actions are dropped: the scope moved (called DURING render), or a send was stale. */
  onDrop?: () => void
  /** The host answered that `id` is deleted (under the scope it was asked in). */
  onDeleted?: (id: string) => void
}

const errorMessage = (e: unknown): string => (e instanceof Error ? e.message : String(e))

export function useSotDelete({ hostId, attachedProfileId, scope, view, reload, failedText, onDrop, onDeleted }: SotDeleteOptions) {
  const t = useI18nStore((s) => s.t)
  const [busy, setBusy] = useState(false)
  const [status, setStatus] = useState<string | null>(null)
  const [confirm, setConfirm] = useState<{ under: string; row: ProfileIndexEntry } | null>(null)
  /** A delete the daemon refused: who it says is still attached. */
  const [refused, setRefused] = useState<{ id: string; attachments: Attachment[] } | null>(null)

  // Everything above was opened under ONE scope. A render under another drops it all (render-phase adjust, as
  // `HostColorLayerEditor` does for an outside colour change): no frame shows host A's confirmation over host B.
  const [openedUnder, setOpenedUnder] = useState(scope)
  if (openedUnder !== scope) {
    setOpenedUnder(scope)
    setBusy(false)
    setStatus(null)
    setConfirm(null)
    setRefused(null)
    onDrop?.()
  }
  /** The scope and the list as of the LAST render: what an `await` comes back to, and what a send is checked against. */
  const live = useRef({ scope, view })
  live.current = { scope, view }

  /** Is `under` still the scope? (An answer for a scope that is gone sets nothing.) */
  const isLive = (under: string): boolean => live.current.scope === under

  /** May an action opened under `under` for profile `id` still be sent? If not, it is dropped and said. */
  const stillValid = (under: string, id: string): boolean => {
    const now = live.current
    if (sotActionStillValid(under, now.scope, id, now.view?.kind === 'rows' ? now.view.rows : null)) return true
    onDrop?.()
    setConfirm(null)
    setStatus(t('settings.profile.sot.stale_action'))
    return false
  }

  const blocked = (row: ProfileIndexEntry) => sotDeleteBlocked(row, attachedProfileId)

  const ask = (row: ProfileIndexEntry): void => {
    if (busy || blocked(row) !== null) return
    setConfirm({ under: scope, row })
  }

  const cancel = (): void => setConfirm(null)

  const remove = async (): Promise<void> => {
    if (confirm === null || busy) return
    const { under, row } = confirm
    if (!stillValid(under, row.id)) return
    setBusy(true)
    setStatus(null)
    setRefused(null)
    try {
      const r = await deleteProfile(hostId, row.id)
      if (!isLive(under)) return
      if (r.kind === 'attached') setRefused({ id: row.id, attachments: r.attachments })
      else if (r.kind === 'failed') setStatus(failedText({ reason: r.reason, message: r.message }))
      else onDeleted?.(row.id)
      // Deleted, or refused because the index was out of date: either way it is asked again.
      if (r.kind !== 'failed') reload()
    } catch (e) {
      if (isLive(under)) setStatus(failedText({ reason: 'thrown', message: errorMessage(e) }))
    } finally {
      if (isLive(under)) {
        setBusy(false)
        setConfirm(null)
      }
    }
  }

  return { busy, setBusy, status, setStatus, confirm, refused, blocked, ask, cancel, remove, stillValid, isLive }
}

export type SotDelete = ReturnType<typeof useSotDelete>
