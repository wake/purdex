// spa/src/components/team/TeamRootsEditor.tsx — a live team's roots (where its members may be started), shown under the lead's row in the
// unattended panel next to the member cap, and edited there: add a path, remove a path (`PUT /api/team/roots`, lib/team/roots.ts).
// What the list shows is the roster's `roots` — nothing is assumed before the daemon answers. The path being typed is kept in the store.
import { useState } from 'react'
import { X } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { checkRootInput, editOf, midEllipsis, saveRoots, useRootsStore, type InUseItem, type RootInputRefusal } from '../../lib/team/roots'
import { teamKey } from '../../lib/team/max-members'
import type { TeamRoster } from '../../lib/team/roster'
import { changeHostRoot, classifyRootInput, viewOf, useHostRootsStore, type HostRootsError } from '../../lib/team/host-roots'
import { hostRootsErrorText } from '../../lib/team/host-roots-text'
import { HostRootsSection } from './HostRootsSection'

const REFUSAL_KEY: Record<RootInputRefusal, string> = {
  empty: 'unattended.roots.err_empty', relative: 'unattended.roots.err_relative', duplicate: 'unattended.roots.err_duplicate', full: 'unattended.roots.err_full',
}

export function TeamRootsEditor({ hostId, team }: { hostId: string; team: TeamRoster }) {
  const t = useI18nStore((s) => s.t)
  const key = teamKey(hostId, team.id)
  const busy = useRootsStore((s) => s.inflight[key] !== undefined)
  const edit = useRootsStore((s) => editOf(s, key))
  const [localRefusal, setLocalRefusal] = useState<RootInputRefusal | null>(null)
  // `alias:/path` goes to the lead host's grants on another host (XR-3); what it was refused for shows beside the box
  const [remoteRefusal, setRemoteRefusal] = useState<HostRootsError | null>(null)
  const hostRoots = useHostRootsStore((s) => viewOf(s, hostId))
  const roots = team.roots ?? []
  const err = edit.error
  // with paired hosts read, the box says it also takes `alias:/path` (a grant on another host)
  const firstAlias = hostRoots.load === 'ok' ? hostRoots.hosts[0]?.alias : undefined
  const placeholder = firstAlias !== undefined ? t('unattended.roots.placeholder_hosts', { alias: firstAlias }) : t('unattended.roots.placeholder')

  const add = async () => {
    setRemoteRefusal(null)
    const input = classifyRootInput(edit.draft, hostRoots.load === 'ok' ? hostRoots.hosts : null)
    if (input.kind === 'unknown_host') { setLocalRefusal(null); setRemoteRefusal({ kind: 'unknown_host', name: input.name }); return }
    if (input.kind === 'not_absolute') { setLocalRefusal('relative'); return }
    if (input.kind === 'remote') {
      setLocalRefusal(null)
      const res = await changeHostRoot(hostId, input.host, 'add', input.path)
      if (res.ok) useRootsStore.getState().setDraft(key, '')
      else setRemoteRefusal(res.error)
      return
    }
    const r = checkRootInput(edit.draft, roots)
    if (!r.ok) { setLocalRefusal(r.reason); return }
    setLocalRefusal(null)
    if (await saveRoots(hostId, team.id, [...roots, r.path])) useRootsStore.getState().setDraft(key, '')
  }
  const remove = (i: number) => { void saveRoots(hostId, team.id, roots.filter((_, j) => j !== i), i) }
  const rowError = (i: number) => (err?.kind === 'bad_request' && err.index === i ? err.detail : null)
  const boxError = err?.kind === 'bad_request' && (err.index === null || err.index >= roots.length) ? err.detail : null

  return (
    <div data-testid="roots-editor" className="flex flex-col gap-1">
      <div className="text-text-muted">{t('unattended.roots.label')}</div>
      <ul className="flex flex-col gap-0.5">
        {roots.map((root, i) => (
          <li key={root} data-testid="roots-row" className="flex flex-col">
            <div className="flex items-center gap-1">
              <span data-testid="roots-path" title={root} className="min-w-0 flex-1 truncate font-mono text-text-primary">{midEllipsis(root)}</span>
              <button
                type="button" data-testid="roots-remove" aria-label={t('unattended.roots.remove', { path: root })} title={roots.length <= 1 ? t('unattended.roots.last') : undefined} disabled={busy || roots.length <= 1} onClick={() => remove(i)}
                className="inline-flex h-5 w-5 shrink-0 cursor-pointer items-center justify-center rounded text-text-secondary hover:bg-surface-hover hover:text-text-primary disabled:pointer-events-none disabled:opacity-30"
              ><X size={12} /></button>
            </div>
            {rowError(i) && <div data-testid="roots-row-error" role="alert" className="text-status-error">{rowError(i)}</div>}
          </li>
        ))}
      </ul>
      <div className="flex items-center gap-1">
        <input
          type="text" data-testid="roots-input" value={edit.draft} disabled={busy} placeholder={placeholder} aria-label={placeholder}
          onChange={(e) => { useRootsStore.getState().setDraft(key, e.target.value); setLocalRefusal(null); setRemoteRefusal(null) }}
          onKeyDown={(e) => { if (e.key === 'Enter') void add() }}
          className="min-w-0 flex-1 rounded border border-border-subtle bg-surface-primary px-2 py-0.5 font-mono text-text-primary"
        />
        <button type="button" data-testid="roots-add" disabled={busy} onClick={() => void add()} className="shrink-0 cursor-pointer rounded border border-border-subtle px-2 py-0.5 text-text-secondary hover:text-text-primary disabled:pointer-events-none disabled:opacity-40">
          {t('unattended.roots.add')}
        </button>
      </div>
      {localRefusal && <div data-testid="roots-input-error" role="alert" className="text-status-error">{t(REFUSAL_KEY[localRefusal])}</div>}
      {remoteRefusal && <div data-testid="roots-input-error" role="alert" className="text-status-error">{hostRootsErrorText(remoteRefusal, t)}</div>}
      {boxError && <div data-testid="roots-input-error" role="alert" className="text-status-error">{boxError}</div>}
      {err?.kind === 'rev_mismatch' && <div data-testid="roots-notice" role="status" className="text-status-warning-text">{t('unattended.roots.rev_mismatch')}</div>}
      {err?.kind === 'merged' && <div data-testid="roots-notice" role="status" className="text-text-muted">{t('unattended.roots.merged')}</div>}
      {err?.kind === 'ended' && <div data-testid="roots-notice" role="alert" className="text-status-error">{t('unattended.roots.ended')}</div>}
      {err?.kind === 'failed' && <div data-testid="roots-notice" role="alert" className="text-status-error">{t('unattended.roots.failed', { code: err.code })}</div>}
      {err?.kind === 'in_use' && (
        <div data-testid="roots-in-use" role="alert" className="text-status-error">
          <div>{t('unattended.roots.in_use', { path: err.removed ?? '' })}</div>
          <ul>{err.items.map((it, i) => <li key={i} data-testid="roots-in-use-item" className="font-mono">{inUseLine(it, team, t)}</li>)}</ul>
        </div>
      )}
      <HostRootsSection leadId={hostId} />
    </div>
  )
}

function inUseLine(it: InUseItem, team: TeamRoster, t: (k: string, p?: Record<string, string>) => string): string {
  if (it.kind === 'member') {
    // the member's name when the roster has it, else its ref
    const m = team.members.find((x) => x.ref === it.ref)
    return t('unattended.roots.in_use_member', { who: m?.title || m?.name || it.ref || it.cwd, cwd: it.cwd })
  }
  return t(it.kind === 'spawn' ? 'unattended.roots.in_use_spawn' : 'unattended.roots.in_use_adoption', { cwd: it.cwd })
}
