import { useEffect, useMemo, useRef, useState } from 'react'
import { Check, PencilSimple, Plus, Trash, WarningCircle, X } from '@phosphor-icons/react'
import { useFleetStore } from '../../lib/fleet/store'
import { mergedProbeEntries, missingAliases, probeBaseOf, useFleetConfigStore, type DaemonOutcome, type MissingAlias, type ProbeBase, type ProbeEdit, type ProbeEntry } from '../../lib/fleet/config-store'
import { useFleetDraftStore } from '../../lib/fleet/config-draft'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { hostLabel, hostLookOf } from '../../lib/host-look'
import { WorkspaceIcon } from '../../features/workspace/components/WorkspaceIcon'
import type { IconWeight } from '../../types/tab'
import { ProbeHostDialog } from './ProbeHostDialog'

const HIGHLIGHT_MS = 2000
const MAX_PROBE_HOSTS = 16

/** A fresh id that no row (on either daemon) already uses. */
function newProbeId(taken: Set<string>): string {
  for (;;) {
    const b = new Uint8Array(4)
    crypto.getRandomValues(b)
    const id = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
    if (!taken.has(id)) return id
  }
}

/**
 * The daemon hosts that carry `fleet.v1` (`known`) and the ones of them that are online (`reachable`). The probe list is
 * written to every known daemon, so writing is only offered while all of them are reachable.
 */
function useFleetDaemons(): { known: string[]; reachable: string[] } {
  const hostOrder = useHostStore((s) => s.hostOrder)
  const online = useHostStore((s) => s.runtime)
  const byHost = useFleetStore((s) => s.byHost)
  return useMemo(() => {
    const known = hostOrder.filter((id) => byHost[id]?.support === 'yes')
    return { known, reachable: known.filter((id) => online[id]?.status === 'connected') }
  }, [hostOrder, online, byHost])
}

const labelOf = (id: string) => hostLabel(id, hostLookOf(id))
const labels = (ids: string[]) => ids.map(labelOf).join(', ')

type T = (k: string, p?: Record<string, string>) => string

/** What each daemon answered to a write, in words: where it was written, where it was not and why. */
function outcomeText(t: T, outcomes: DaemonOutcome[]): string {
  const lines: string[] = []
  const saved = outcomes.filter((o) => o.ok).map((o) => o.hostId)
  const conflicted = outcomes.filter((o) => o.conflict).map((o) => o.hostId)
  if (saved.length) lines.push(t('fleet_hosts.probe.outcome_saved', { hosts: labels(saved) }))
  if (conflicted.length) lines.push(t('fleet_hosts.probe.outcome_conflict', { hosts: labels(conflicted) }))
  for (const o of outcomes) if (!o.ok && !o.conflict) lines.push(t('fleet_hosts.probe.outcome_failed', { host: labelOf(o.hostId), text: o.text ?? '' }))
  if (saved.length && saved.length < outcomes.length) lines.push(t('fleet_hosts.probe.outcome_diverged'))
  return lines.join('\n')
}

function missingText(t: T, rows: MissingAlias[]): string {
  return rows.map((r) => t('fleet_hosts.probe.only_on', { name: r.name || r.id, hosts: labels(r.presentOn), missing: labels(r.missing) })).join('\n')
}

/**
 * 「只探查的主機」 (fleet spec §8.2): the probe hosts once, written to BOTH daemons. Shown on the overview of a `fleet.v1`
 * host. Writing is offered only while every known fleet.v1 daemon is reachable and read, and a row must carry an alias for each
 * of them. A 409 or a failure on one daemon keeps the dialog and its draft, reloads both and says what each daemon took; a
 * daemon whose copy of the row moved since the edit began has to be confirmed before it is overwritten; a GET that dropped rows
 * must be confirmed before the list is written back. The dialog, the confirmations, the delete prompt and the request state
 * live in `useFleetDraftStore` (a tab switch unmounts this section). `useFleetConfigStore.focusProbe` (F-2b's click)
 * highlights one row once.
 */
export function ProbeHostsSection() {
  const t = useI18nStore((s) => s.t) as T
  const { known, reachable: hostIds } = useFleetDaemons()
  const key = hostIds.join('|')
  const byHost = useFleetConfigStore((s) => s.byHost)
  const focusProbe = useFleetConfigStore((s) => s.focusProbe)
  const entries = useMemo(() => mergedProbeEntries(hostIds, byHost), [hostIds, byHost])
  const ready = hostIds.length > 0 && hostIds.every((id) => byHost[id]?.status === 'ready')
  const unreachable = known.filter((id) => !hostIds.includes(id))
  const failed = hostIds.filter((id) => byHost[id]?.status === 'error')
  const writable = ready && unreachable.length === 0
  const halfRows = useMemo(() => new Map(missingAliases(hostIds, entries).map((m) => [m.id, m])), [hostIds, entries])

  const session = useFleetDraftStore((s) => s.probe)
  const deleting = useFleetDraftStore((s) => s.deleting)
  const confirm = useFleetDraftStore((s) => s.probeConfirm)
  const { busy, dialogError, listError, changed } = useFleetDraftStore((s) => s.ui)
  const drafts = useFleetDraftStore.getState
  const [highlight, setHighlight] = useState<string | null>(null)
  const rowRefs = useRef<Record<string, HTMLLIElement | null>>({})
  const trigger = useRef<HTMLElement | null>(null)

  useEffect(() => {
    if (hostIds.length) void useFleetConfigStore.getState().loadMany(hostIds)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `key` is the host id list
  }, [key])

  // Highlight once: take the request when the row is there, show it for a moment, forget it. Once everything is read and the
  // row is not there, the request is dropped (it would otherwise wait for a row that never comes).
  useEffect(() => {
    if (!focusProbe) return
    if (entries.some((e) => e.id === focusProbe)) {
      setHighlight(focusProbe)
      useFleetConfigStore.getState().clearProbeFocus()
      rowRefs.current[focusProbe]?.scrollIntoView?.({ block: 'center' })
    } else if (ready) {
      useFleetConfigStore.getState().clearProbeFocus()
    }
  }, [focusProbe, entries, ready])
  useEffect(() => {
    if (!highlight) return
    const timer = setTimeout(() => setHighlight(null), HIGHLIGHT_MS)
    return () => clearTimeout(timer)
  }, [highlight])

  // The dialog gives the focus back to what opened it.
  const wasOpen = useRef(false)
  useEffect(() => {
    if (wasOpen.current && !session && trigger.current?.isConnected) trigger.current.focus()
    wasOpen.current = !!session
  }, [session])

  if (known.length === 0) return null

  const loadError = failed.length > 0
    ? t('fleet_hosts.probe.load_error', { hosts: labels(failed), text: failed.map((id) => byHost[id]?.error ?? '').filter(Boolean).join('; ') })
    : null
  /** Why nothing can be written right now, in words (null: it can). */
  const blocked = unreachable.length > 0
    ? t('fleet_hosts.probe.unreachable', { hosts: labels(unreachable) })
    : loadError ?? (!ready ? t('fleet_hosts.probe.loading') : null)

  const run = async (edit: ProbeEdit, from: 'dialog' | 'list', opts: { confirmDropped?: boolean; confirmChanged?: boolean; base?: ProbeBase } = {}) => {
    if (!writable || drafts().ui.busy) return
    const ui = drafts().setUi
    ui({ busy: true, dialogError: null, listError: null, changed: null })
    const show = (text: string) => ui(from === 'dialog' ? { dialogError: text } : { listError: text })
    try {
      const r = await useFleetConfigStore.getState().saveProbe(hostIds, edit, opts)
      if (r.kind === 'saved') { drafts().endProbe(); drafts().setProbeConfirm(null); drafts().setDeleting(null) }
      else if (r.kind === 'confirm') drafts().setProbeConfirm({ edit, notices: r.notices, base: opts.base })
      else if (r.kind === 'missing_alias') show(missingText(t, r.rows))
      else if (r.kind === 'changed') { ui({ changed: r.hosts }); show(t('fleet_hosts.probe.changed_on', { hosts: labels(r.hosts) })) }
      // The dialog (and its draft) stays: both daemons were read again, and the text says what each one took.
      else if (r.kind === 'conflict') { drafts().setProbeConfirm(null); show(`${t('fleet_hosts.probe.conflict')}\n${outcomeText(t, r.outcomes)}`) }
      else show(r.outcomes.length ? outcomeText(t, r.outcomes) : r.text)
    } finally {
      ui({ busy: false })
    }
  }

  const saveEntry = (entry: ProbeEntry, confirmChanged = false) =>
    void run({ kind: 'upsert', entry }, 'dialog', { base: session?.base, confirmChanged })
  const begin = (entry: ProbeEntry, isNew: boolean, from: HTMLElement | null) => {
    trigger.current = from
    drafts().setUi({ dialogError: null, changed: null })
    drafts().beginProbe(entry, isNew, probeBaseOf(hostIds, byHost, isNew ? null : entry.id))
  }
  const iconBtn = 'p-1 rounded hover:bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed'

  return (
    <div data-testid="probe-hosts-section" className="mt-6">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm font-semibold">{t('fleet_hosts.probe.title')}</h3>
        <button type="button" data-testid="probe-add" disabled={!writable || busy || entries.length >= MAX_PROBE_HOSTS}
          onClick={(ev) => begin({ id: newProbeId(new Set(entries.map((e) => e.id))), name: '', icon: '', icon_weight: '', aliases: {} }, true, ev.currentTarget)}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50">
          <Plus size={14} />{t('fleet_hosts.probe.add')}
        </button>
      </div>
      <p className="text-xs text-text-muted mb-3">{t('fleet_hosts.probe.hint')}</p>
      {loadError && (
        <div data-testid="probe-load-error" className="mb-3 flex items-center gap-2 text-xs text-status-warning-text">
          <span className="whitespace-pre-wrap">{loadError}</span>
          <button type="button" data-testid="probe-reload" onClick={() => void useFleetConfigStore.getState().loadMany(hostIds)}
            className="px-2 py-0.5 rounded bg-surface-tertiary text-text-secondary cursor-pointer">{t('fleet_hosts.probe.reload')}</button>
        </div>
      )}
      {unreachable.length > 0 && (
        <p data-testid="probe-unreachable" className="mb-3 text-xs text-status-warning-text">{t('fleet_hosts.probe.unreachable', { hosts: labels(unreachable) })}</p>
      )}
      {listError && <p data-testid="probe-list-error" className="mb-3 text-xs text-status-warning-text whitespace-pre-wrap">{listError}</p>}
      {confirm && (
        <div data-testid="probe-dropped-confirm" className="mb-3 p-3 rounded border border-border-default bg-surface-secondary text-xs">
          <p className="flex items-center gap-1.5 font-medium text-text-primary"><WarningCircle size={14} />{t('fleet_hosts.dropped.title')}</p>
          <ul className="mt-1 text-text-secondary list-disc pl-5">
            {confirm.notices.map((n) => (
              <li key={n.hostId}>
                {t('fleet_hosts.dropped.on', { host: labelOf(n.hostId), count: String(n.count) })}
                {n.invalid ? ` ${t('fleet_hosts.dropped.invalid')}` : ''}
                {n.reasons.length ? ` — ${n.reasons.join('; ')}` : ''}
              </li>
            ))}
          </ul>
          <p className="mt-1 text-text-muted">{t('fleet_hosts.dropped.warn')}</p>
          <div className="flex gap-2 mt-2">
            <button type="button" data-testid="probe-dropped-save" disabled={busy || !writable}
              onClick={() => void run(confirm.edit, 'list', { confirmDropped: true, base: confirm.base })}
              className="px-3 py-1 rounded bg-accent text-white cursor-pointer disabled:opacity-50">{t('fleet_hosts.dropped.save_anyway')}</button>
            <button type="button" data-testid="probe-dropped-cancel" onClick={() => drafts().setProbeConfirm(null)}
              className="px-3 py-1 rounded bg-surface-tertiary text-text-secondary cursor-pointer">{t('common.cancel')}</button>
          </div>
        </div>
      )}
      {session && (
        <ProbeHostDialog key={session.initial.id} draft={session.draft} hostIds={hostIds} isNew={session.isNew} busy={busy}
          blocked={writable ? null : blocked} error={dialogError} onChange={(fn) => drafts().setProbeDraft(fn)} onSave={(e) => saveEntry(e)}
          onCancel={() => { drafts().endProbe(); drafts().setUi({ dialogError: null, changed: null }) }}>
          {changed && (
            <button type="button" data-testid="probe-overwrite" disabled={busy || !writable} onClick={() => saveEntry(session.draft, true)}
              className="px-3 py-1.5 rounded text-xs bg-status-warning text-text-inverse cursor-pointer disabled:opacity-50">{t('fleet_hosts.probe.overwrite')}</button>
          )}
        </ProbeHostDialog>
      )}
      {!ready ? (
        <p data-testid="probe-state" className="text-sm text-text-muted">{failed.length > 0 || unreachable.length > 0 ? t('fleet_hosts.probe.unreadable') : t('fleet_hosts.probe.loading')}</p>
      ) : entries.length === 0 ? (
        <p className="text-sm text-text-muted">{t('fleet_hosts.probe.empty')}</p>
      ) : (
        <ul className="border border-border-subtle rounded-lg divide-y divide-border-subtle">
          {entries.map((e) => {
            const half = halfRows.get(e.id)
            return (
              <li key={e.id} data-testid={`probe-row-${e.id}`} data-highlighted={highlight === e.id ? 'true' : undefined}
                ref={(el) => { rowRefs.current[e.id] = el }}
                className={`px-3 py-2 ${highlight === e.id ? 'bg-accent/15' : ''}`}>
                <div className="flex items-center gap-3">
                  {e.icon ? <WorkspaceIcon icon={e.icon} name={e.name} size={16} weight={(e.icon_weight || 'bold') as IconWeight} /> : null}
                  <span className="w-40 shrink-0 truncate text-sm text-text-primary" title={e.name}>{e.name}</span>
                  <span className="min-w-0 flex-1 truncate font-mono text-xs text-text-muted">
                    {hostIds.map((id) => `${labelOf(id)}: ${e.aliases[id] ? `${e.aliases[id]}${byHost[id]?.aliasesLoaded === false ? ` (${t('fleet_hosts.probe.alias_unverified')})` : ''}` : '—'}`).join('  ·  ')}
                  </span>
                  <div className="flex items-center gap-1">
                    <button type="button" data-testid={`probe-edit-${e.id}`} title={t('common.edit')} aria-label={`${t('common.edit')} ${e.name}`} disabled={!writable || busy}
                      onClick={(ev) => begin(e, false, ev.currentTarget)} className={iconBtn}><PencilSimple size={14} /></button>
                    {deleting === e.id ? (
                      <span className="flex items-center gap-1">
                        <button type="button" data-testid={`probe-delete-confirm-${e.id}`} aria-label={`${t('common.delete')} ${e.name}`} disabled={busy || !writable}
                          onClick={() => { drafts().setDeleting(null); void run({ kind: 'remove', id: e.id }, 'list') }}
                          className="p-1 text-red-400 cursor-pointer disabled:opacity-40"><Check size={14} /></button>
                        <button type="button" aria-label={t('common.cancel')} onClick={() => drafts().setDeleting(null)} className="p-1 text-text-muted cursor-pointer"><X size={14} /></button>
                      </span>
                    ) : (
                      <button type="button" data-testid={`probe-delete-${e.id}`} title={t('common.delete')} aria-label={`${t('common.delete')} ${e.name}`} disabled={!writable || busy}
                        onClick={() => drafts().setDeleting(e.id)} className={`${iconBtn} hover:text-red-400`}><Trash size={14} /></button>
                    )}
                  </div>
                </div>
                {half && (
                  <p data-testid={`probe-half-${e.id}`} className="mt-1 text-xs text-status-warning-text">
                    {t('fleet_hosts.probe.only_on', { name: e.name || e.id, hosts: labels(half.presentOn), missing: labels(half.missing) })}
                  </p>
                )}
                {e.mismatch && (
                  <p data-testid={`probe-mismatch-${e.id}`} className="mt-1 flex items-center gap-2 text-xs text-status-warning-text">
                    {t('fleet_hosts.probe.mismatch')}
                    <button type="button" data-testid={`probe-rewrite-${e.id}`} disabled={!writable || busy || !!half}
                      onClick={() => void run({ kind: 'rewrite', id: e.id }, 'list')}
                      className="px-2 py-0.5 rounded bg-surface-tertiary text-text-secondary cursor-pointer disabled:opacity-50">{t('fleet_hosts.probe.rewrite')}</button>
                  </p>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
