import { useEffect, useMemo, useRef, useState } from 'react'
import { Check, PencilSimple, Plus, Trash, WarningCircle, X } from '@phosphor-icons/react'
import { useFleetStore } from '../../lib/fleet/store'
import { mergedProbeEntries, useFleetConfigStore, type DroppedNotice, type ProbeEntry } from '../../lib/fleet/config-store'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { hostLabel, hostLookOf } from '../../lib/host-look'
import { WorkspaceIcon } from '../../features/workspace/components/WorkspaceIcon'
import type { IconWeight } from '../../types/tab'
import { ProbeHostDialog } from './ProbeHostDialog'

const HIGHLIGHT_MS = 2000
const MAX_PROBE_HOSTS = 16

function newProbeId(): string {
  const b = new Uint8Array(4)
  crypto.getRandomValues(b)
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
}

/** The daemon hosts that carry `fleet.v1` and are online: the probe list is written to every one of them. */
function useFleetDaemonHostIds(): string[] {
  const hostOrder = useHostStore((s) => s.hostOrder)
  const online = useHostStore((s) => s.runtime)
  const byHost = useFleetStore((s) => s.byHost)
  return useMemo(
    () => hostOrder.filter((id) => byHost[id]?.support === 'yes' && online[id]?.status === 'connected'),
    [hostOrder, online, byHost],
  )
}

/**
 * 「只探查的主機」 (fleet spec §8.2): the probe hosts once, written to BOTH daemons. Shown on the overview of a `fleet.v1`
 * host. A 409 from either daemon reloads both; a GET that dropped rows must be confirmed before the list is written back.
 * `useFleetConfigStore.focusProbe` (F-2b's click) highlights one row once.
 */
export function ProbeHostsSection() {
  const t = useI18nStore((s) => s.t)
  const hostIds = useFleetDaemonHostIds()
  const key = hostIds.join('|')
  const byHost = useFleetConfigStore((s) => s.byHost)
  const focusProbe = useFleetConfigStore((s) => s.focusProbe)
  const entries = useMemo(() => mergedProbeEntries(hostIds, byHost), [hostIds, byHost])
  const ready = hostIds.length > 0 && hostIds.every((id) => byHost[id]?.status === 'ready')

  const [editing, setEditing] = useState<ProbeEntry | null>(null)
  const [deleting, setDeleting] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [dialogError, setDialogError] = useState<string | null>(null)
  const [listError, setListError] = useState<string | null>(null)
  const [confirm, setConfirm] = useState<{ entries: ProbeEntry[]; notices: DroppedNotice[] } | null>(null)
  const [highlight, setHighlight] = useState<string | null>(null)
  const rowRefs = useRef<Record<string, HTMLLIElement | null>>({})

  useEffect(() => {
    if (hostIds.length) void useFleetConfigStore.getState().loadMany(hostIds)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `key` is the host id list
  }, [key])

  // Highlight once: take the request when the row is there, show it for a moment, forget it.
  useEffect(() => {
    if (!focusProbe || !entries.some((e) => e.id === focusProbe)) return
    setHighlight(focusProbe)
    useFleetConfigStore.getState().clearProbeFocus()
    rowRefs.current[focusProbe]?.scrollIntoView?.({ block: 'center' })
  }, [focusProbe, entries])
  useEffect(() => {
    if (!highlight) return
    const timer = setTimeout(() => setHighlight(null), HIGHLIGHT_MS)
    return () => clearTimeout(timer)
  }, [highlight])

  if (hostIds.length === 0) return null

  const run = async (next: ProbeEntry[], confirmDropped: boolean, from: 'dialog' | 'list') => {
    setBusy(true)
    setDialogError(null)
    setListError(null)
    try {
      const r = await useFleetConfigStore.getState().saveProbeList(hostIds, next, { confirmDropped })
      if (r.kind === 'saved') { setEditing(null); setConfirm(null) }
      else if (r.kind === 'confirm') setConfirm({ entries: next, notices: r.notices })
      else if (r.kind === 'conflict') { setEditing(null); setConfirm(null); setListError(t('fleet_hosts.probe.conflict')) }
      else if (from === 'dialog') setDialogError(r.text)
      else setListError(r.text)
    } finally {
      setBusy(false)
    }
  }

  const saveEntry = (entry: ProbeEntry) => {
    const exists = entries.some((e) => e.id === entry.id)
    void run(exists ? entries.map((e) => (e.id === entry.id ? entry : e)) : [...entries, entry], false, 'dialog')
  }
  const remove = (id: string) => {
    setDeleting(null)
    void run(entries.filter((e) => e.id !== id), false, 'list')
  }
  const isNew = !!editing && !entries.some((e) => e.id === editing.id)
  const iconBtn = 'p-1 rounded hover:bg-surface-tertiary text-text-secondary hover:text-text-primary cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed'

  return (
    <div data-testid="probe-hosts-section" className="mt-6">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm font-semibold">{t('fleet_hosts.probe.title')}</h3>
        <button type="button" data-testid="probe-add" disabled={!ready || busy || entries.length >= MAX_PROBE_HOSTS}
          onClick={() => { setDialogError(null); setEditing({ id: newProbeId(), name: '', icon: '', icon_weight: '', aliases: {} }) }}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50">
          <Plus size={14} />{t('fleet_hosts.probe.add')}
        </button>
      </div>
      <p className="text-xs text-text-muted mb-3">{t('fleet_hosts.probe.hint')}</p>
      {listError && <p data-testid="probe-list-error" className="mb-3 text-xs text-status-warning-text whitespace-pre-wrap">{listError}</p>}
      {confirm && (
        <div data-testid="probe-dropped-confirm" className="mb-3 p-3 rounded border border-border-default bg-surface-secondary text-xs">
          <p className="flex items-center gap-1.5 font-medium text-text-primary"><WarningCircle size={14} />{t('fleet_hosts.dropped.title')}</p>
          <ul className="mt-1 text-text-secondary list-disc pl-5">
            {confirm.notices.map((n) => (
              <li key={n.hostId}>
                {t('fleet_hosts.dropped.on', { host: hostLabel(n.hostId, hostLookOf(n.hostId)), count: String(n.count) })}
                {n.invalid ? ` ${t('fleet_hosts.dropped.invalid')}` : ''}
                {n.reasons.length ? ` — ${n.reasons.join('; ')}` : ''}
              </li>
            ))}
          </ul>
          <p className="mt-1 text-text-muted">{t('fleet_hosts.dropped.warn')}</p>
          <div className="flex gap-2 mt-2">
            <button type="button" data-testid="probe-dropped-save" disabled={busy} onClick={() => void run(confirm.entries, true, 'list')}
              className="px-3 py-1 rounded bg-accent text-white cursor-pointer disabled:opacity-50">{t('fleet_hosts.dropped.save_anyway')}</button>
            <button type="button" data-testid="probe-dropped-cancel" onClick={() => setConfirm(null)}
              className="px-3 py-1 rounded bg-surface-tertiary text-text-secondary cursor-pointer">{t('common.cancel')}</button>
          </div>
        </div>
      )}
      {editing && (
        <ProbeHostDialog key={editing.id} initial={editing} hostIds={hostIds} isNew={isNew} busy={busy}
          error={dialogError} onSave={saveEntry} onCancel={() => { setEditing(null); setDialogError(null) }} />
      )}
      {entries.length === 0 ? (
        <p className="text-sm text-text-muted">{t('fleet_hosts.probe.empty')}</p>
      ) : (
        <ul className="border border-border-subtle rounded-lg divide-y divide-border-subtle">
          {entries.map((e) => (
            <li key={e.id} data-testid={`probe-row-${e.id}`} data-highlighted={highlight === e.id ? 'true' : undefined}
              ref={(el) => { rowRefs.current[e.id] = el }}
              className={`flex items-center gap-3 px-3 py-2 ${highlight === e.id ? 'bg-accent/15' : ''}`}>
              {e.icon ? <WorkspaceIcon icon={e.icon} name={e.name} size={16} weight={(e.icon_weight || 'bold') as IconWeight} /> : null}
              <span className="w-40 shrink-0 truncate text-sm text-text-primary" title={e.name}>{e.name}</span>
              <span className="min-w-0 flex-1 truncate font-mono text-xs text-text-muted">
                {hostIds.map((id) => `${hostLabel(id, hostLookOf(id))}: ${e.aliases[id] ?? '—'}`).join('  ·  ')}
              </span>
              <div className="flex items-center gap-1">
                <button type="button" data-testid={`probe-edit-${e.id}`} title={t('common.edit')} disabled={!ready || busy}
                  onClick={() => { setDialogError(null); setEditing(e) }} className={iconBtn}><PencilSimple size={14} /></button>
                {deleting === e.id ? (
                  <span className="flex items-center gap-1">
                    <button type="button" data-testid={`probe-delete-confirm-${e.id}`} disabled={busy} onClick={() => remove(e.id)}
                      className="p-1 text-red-400 cursor-pointer disabled:opacity-40"><Check size={14} /></button>
                    <button type="button" onClick={() => setDeleting(null)} className="p-1 text-text-muted cursor-pointer"><X size={14} /></button>
                  </span>
                ) : (
                  <button type="button" data-testid={`probe-delete-${e.id}`} title={t('common.delete')} disabled={!ready || busy}
                    onClick={() => setDeleting(e.id)} className={`${iconBtn} hover:text-red-400`}><Trash size={14} /></button>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
