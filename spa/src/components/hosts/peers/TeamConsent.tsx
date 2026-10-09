// spa/src/components/hosts/peers/TeamConsent.tsx — one paired peer's cross-host team consent (spec §5.4):
// the allow_team switch and the team_roots folder list, written with PUT /api/peers/hosts/{alias}. The daemon's
// fields are pointers (absent = unchanged, `team_roots: []` clears), so each control sends only what it changed.
// Nothing is mirrored locally: after a write the page's flow runner re-reads the row, and the controls show whatever
// the daemon says now (a failed write therefore "reverts" by simply not having changed anything).
//
// Turning the switch off can strand members that peer already opened here. The daemon does not refuse that — it
// only flips the flag; ending the members is a separate admin call (`pdx peers host allow-team … off --end-members`
// does PUT off, then POST /api/team/remote-members/end per member, 409 = already ended). So the choice is asked
// BEFORE the write: list this host's live remote members, keep those whose lead is this peer, and confirm.
import { useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import {
  HostApiError, endRemoteMember, listRemoteMembers, updatePeerHost, type PeerHostRow, type RemoteMemberView,
} from '../../../lib/host-api'
import { ConfirmDialog } from '../../ConfirmDialog'
import { errText, type BoundRunFlow } from './flow'

interface Props {
  hostId: string
  row: PeerHostRow
  busy: boolean
  runFlow: BoundRunFlow
}

export function TeamConsent({ hostId, row, busy, runFlow }: Props) {
  const t = useI18nStore((s) => s.t)
  const [toggleError, setToggleError] = useState('')
  const [rootsError, setRootsError] = useState('')
  const [draft, setDraft] = useState('')
  const [pending, setPending] = useState<RemoteMemberView[] | null>(null)
  const alias = row.alias
  const allowed = row.allow_team === true
  // Lenient: a daemon without the field (or a null) means off / no folders.
  const roots = Array.isArray(row.team_roots) ? row.team_roots.filter((x): x is string => typeof x === 'string') : []

  const setAllow = (on: boolean) => {
    setToggleError('')
    void runFlow(async () => {
      try {
        if (!on) {
          const mine = row.host_id
            ? (await listRemoteMembers(hostId)).filter((m) => m.lead_host_id === row.host_id)
            : []
          if (mine.length > 0) { setPending(mine); return {} }
        }
        await updatePeerHost(hostId, alias, { allow_team: on })
      } catch (e) {
        setToggleError(errText(e))
      }
      return {}
    })
  }

  const turnOff = (members: RemoteMemberView[], end: boolean) => {
    setPending(null)
    setToggleError('')
    void runFlow(async () => {
      try {
        await updatePeerHost(hostId, alias, { allow_team: false })
      } catch (e) {
        setToggleError(errText(e))
        return {}
      }
      if (!end) return {}
      const failed: string[] = []
      for (const m of members) {
        try {
          await endRemoteMember(hostId, m.mk)
        } catch (e) {
          if (e instanceof HostApiError && e.status === 409) continue // no longer live: already what was asked
          failed.push(`${m.title || m.mk}: ${errText(e)}`)
        }
      }
      if (failed.length) setToggleError(t('peers.team.end_failed', { details: failed.join('; ') }))
      return {}
    })
  }

  const writeRoots = (next: string[], onOk?: () => void) => {
    setRootsError('')
    void runFlow(async () => {
      try {
        await updatePeerHost(hostId, alias, { team_roots: next })
        onOk?.()
      } catch (e) {
        setRootsError(errText(e))
      }
      return {}
    })
  }

  const add = () => {
    const p = draft.trim()
    if (!p) return
    writeRoots([...roots, p], () => setDraft(''))
  }

  return (
    <div data-testid="peer-team" className="mt-2 pt-2 border-t border-border-subtle space-y-1.5">
      <label className="flex items-center gap-2 text-xs text-text-primary">
        <input type="checkbox" data-testid="peer-team-toggle" checked={allowed} disabled={busy}
          onChange={(e) => setAllow(e.target.checked)} />
        {t('peers.team.allow', { alias })}
      </label>
      {toggleError && <p data-testid="peer-team-error" className="text-xs text-status-error whitespace-pre-wrap">{toggleError}</p>}

      <div className="text-xs text-text-secondary">{t('peers.team.roots')}</div>
      {roots.length === 0
        ? <p data-testid="peer-team-roots-empty" className="text-xs text-text-muted">{t('peers.team.roots_empty')}</p>
        : (
          <ul className="space-y-0.5">
            {roots.map((r, i) => (
              <li key={`${i}:${r}`} className="flex items-center gap-2">
                <span data-testid="peer-team-root" className="font-mono text-xs text-text-secondary break-all">{r}</span>
                <button type="button" data-testid="peer-team-root-remove" disabled={busy}
                  onClick={() => writeRoots(roots.filter((_, j) => j !== i))}
                  className="text-xs px-1.5 py-0.5 rounded bg-surface-tertiary text-text-secondary hover:text-status-error cursor-pointer disabled:opacity-50 disabled:cursor-default">
                  {t('peers.team.root_remove')}
                </button>
              </li>
            ))}
          </ul>
        )}
      <div className="flex items-center gap-2">
        <input data-testid="peer-team-root-input" value={draft} disabled={busy} placeholder={t('peers.team.root_placeholder')}
          onChange={(e) => setDraft(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter') add() }}
          className="flex-1 px-2 py-1 rounded bg-surface-secondary border border-border-subtle text-xs font-mono" />
        <button type="button" data-testid="peer-team-root-add" disabled={busy || !draft.trim()} onClick={add}
          className="text-xs px-2 py-1 rounded bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer disabled:opacity-50 disabled:cursor-default">
          {t('peers.team.root_add')}
        </button>
      </div>
      {rootsError && <p data-testid="peer-team-roots-error" className="text-xs text-status-error whitespace-pre-wrap">{rootsError}</p>}

      {pending && (
        <ConfirmDialog testIdPrefix="peer-team-end" busy={busy}
          title={t('peers.team.end_title', { alias })}
          body={t('peers.team.end_body', { alias, count: pending.length })}
          confirmLabel={t('peers.team.end_confirm')}
          onCancel={() => setPending(null)} onConfirm={() => turnOff(pending, true)}>
          <ul className="mt-2 text-xs text-text-secondary list-disc pl-4">
            {pending.map((m) => <li key={m.mk} className="break-all">{m.title || m.mk}</li>)}
          </ul>
          <button type="button" data-testid="peer-team-end-keep" onClick={() => turnOff(pending, false)}
            className="mt-2 text-xs px-2 py-1 rounded bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer">
            {t('peers.team.end_keep')}
          </button>
        </ConfirmDialog>
      )}
    </div>
  )
}
