import { useEffect, useRef, useState } from 'react'
import { PencilSimple, Plus, Trash } from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useFleetEntry, useFleetStore } from '../../lib/fleet/store'
import { draftOf, newProbeDraft, useProbeDraftStore } from '../../lib/fleet/probe-draft-memory'
import { MAX_PROBE_HOSTS, removeProbe, saveProbeHosts } from '../../lib/fleet/probe-hosts-save'
import type { FleetProbe, ProbeHostDef } from '../../lib/fleet/types'
import { Section } from './form-fields'
import { ProbeHostBadge } from './ProbeHostBadge'
import { ProbeHostDialog } from './ProbeHostDialog'

const HIGHLIGHT_MS = 2000

/** One short line for a row's state, from the live fleet payload (never the daemon's own words). */
function statusKey(p: FleetProbe | undefined): string {
  if (!p) return 'hosts.probe.status.unknown'
  if (p.status === 'ok') return 'hosts.probe.status.ok'
  if (p.status === 'offline') return 'hosts.probe.status.offline'
  return 'hosts.probe.status.error'
}

/**
 * 「只探查的主機」 (fleet spec §7.2): the hosts this daemon probes over SSH although none has a daemon connection. Shown
 * under a host whose daemon carries `fleet.v1`, nowhere else. Rows come from the prober's host config
 * (`probeHosts`); the live state beside each comes from the fleet payload.
 */
export function ProbeHostsSection({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const online = useHostStore((s) => s.runtime[hostId]?.status === 'connected')
  const fleet = useFleetEntry(hostId)
  const defs = useFleetStore((s) => s.defs[hostId])
  const draft = useProbeDraftStore((s) => s.byHost[hostId])
  const focusProbe = useFleetStore((s) => s.focusProbe)
  const [deleting, setDeleting] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const rowRefs = useRef<Record<string, HTMLDivElement | null>>({})

  const supported = online && fleet.support === 'yes'
  const probeIds = fleet.probes.map((p) => p.id).join(',')

  // The list is read when the section appears and again whenever the daemon's own set of probe ids moved (another client
  // added or removed one): the push carries the ids, not the collection's revision.
  useEffect(() => {
    if (supported) void useFleetStore.getState().loadDefs(hostId)
  }, [hostId, supported, probeIds])

  // A bottom-block row click names a probe: the row is highlighted for a moment (derived from the request itself, so no
  // state is set in an effect), scrolled to once, and the request is cleared when the highlight ends.
  const highlight = focusProbe && focusProbe.proberHostId === hostId && defs?.items.some((d) => d.id === focusProbe.probeId)
    ? focusProbe.probeId
    : null
  useEffect(() => {
    if (highlight === null) return
    rowRefs.current[highlight]?.scrollIntoView?.({ block: 'center' })
    const timer = setTimeout(() => {
      if (useFleetStore.getState().focusProbe?.probeId === highlight) useFleetStore.getState().setFocusProbe(null)
    }, HIGHLIGHT_MS)
    return () => clearTimeout(timer)
  }, [highlight])

  if (!supported) return null

  const items: readonly ProbeHostDef[] = defs?.items ?? []
  const atLimit = items.length >= MAX_PROBE_HOSTS

  const remove = async (id: string) => {
    setDeleting(null)
    setNotice(null)
    const res = await saveProbeHosts(hostId, removeProbe(id))
    if (res.kind === 'conflict') setNotice(t('hosts.probe.conflict'))
    else if (res.kind === 'invalid') setNotice(res.text)
    else if (res.kind === 'failed') setNotice(t('hosts.probe.save_failed', { reason: res.reason }))
  }

  return (
    <Section title={t('hosts.probe.title')}>
      <div data-testid="probe-section" className="space-y-2">
        <p className="text-xs text-text-muted">{t('hosts.probe.hint')}</p>
        {notice && <p data-testid="probe-notice" className="text-xs text-status-warning-text">{notice}</p>}
        {items.length === 0 && !draft && <p data-testid="probe-empty" className="text-xs text-text-muted">{t('hosts.probe.empty')}</p>}
        {items.map((d) => {
          const live = fleet.probes.find((p) => p.id === d.id)
          return (
            <div key={d.id} ref={(el) => { rowRefs.current[d.id] = el }} data-testid={`probe-row-${d.id}`}
              data-highlight={String(highlight === d.id)}
              className={`flex items-center gap-2 rounded px-2 py-1.5 text-sm border ${highlight === d.id ? 'border-border-active bg-surface-secondary' : 'border-transparent'}`}>
              <ProbeHostBadge look={d} />
              <span className="font-medium truncate">{d.name}</span>
              <span className="font-mono text-xs text-text-muted truncate">{d.ssh_alias}</span>
              <span data-testid={`probe-status-${d.id}`} className="text-xs text-text-secondary ml-auto shrink-0">
                {t(statusKey(live))}{live?.error ? ` · ${t(`fleet.error.${live.error}`)}` : ''}
              </span>
              {deleting === d.id ? (
                <span className="flex items-center gap-1 shrink-0">
                  <button type="button" data-testid={`probe-remove-confirm-${d.id}`} onClick={() => void remove(d.id)}
                    className="px-2 py-1 rounded text-xs bg-red-500/20 text-red-400 cursor-pointer">{t('hosts.probe.remove_confirm', { name: d.name })}</button>
                  <button type="button" onClick={() => setDeleting(null)}
                    className="px-2 py-1 rounded text-xs bg-surface-tertiary text-text-secondary cursor-pointer">{t('common.cancel')}</button>
                </span>
              ) : (
                <span className="flex items-center gap-1 shrink-0">
                  <button type="button" data-testid={`probe-edit-${d.id}`} aria-label={t('hosts.probe.edit')} title={t('hosts.probe.edit')}
                    onClick={() => { setNotice(null); useProbeDraftStore.getState().open(hostId, draftOf(d)) }}
                    className="p-1 rounded text-text-muted hover:text-text-primary cursor-pointer"><PencilSimple size={14} /></button>
                  <button type="button" data-testid={`probe-remove-${d.id}`} aria-label={t('hosts.probe.remove')} title={t('hosts.probe.remove')}
                    onClick={() => setDeleting(d.id)}
                    className="p-1 rounded text-text-muted hover:text-red-400 cursor-pointer"><Trash size={14} /></button>
                </span>
              )}
            </div>
          )
        })}
        {draft && <ProbeHostDialog hostId={hostId} draft={draft} />}
        {!draft && (
          <button type="button" data-testid="probe-add" disabled={atLimit || !defs}
            onClick={() => { setNotice(null); useProbeDraftStore.getState().open(hostId, newProbeDraft()) }}
            className="inline-flex items-center gap-1 px-3 py-1.5 rounded text-xs bg-surface-secondary border border-border-default text-text-secondary hover:text-text-primary cursor-pointer disabled:opacity-50">
            <Plus size={12} />{t('hosts.probe.add')}
          </button>
        )}
      </div>
    </Section>
  )
}
