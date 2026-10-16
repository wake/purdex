import { Plus } from '@phosphor-icons/react'
import { useFleetDraftStore } from '../../lib/fleet/config-draft'
import { ProbeBadge, ProbeStatusDot } from './probe-rows'
import { useProbeRows } from './use-probe-rows'
import { MAX_PROBE_HOSTS, startNewProbe, useProbeEditor } from './useProbeEditor'

/**
 * 「觀察主機」 on the overview of a `fleet.v1` host (fleet spec §8.2): a short list — badge, name, status — whose rows open
 * the host's own entry on the Hosts page, and 「新增觀察主機」. Nothing is edited here.
 */
export function ProbeHostsSection() {
  const { t, known, ready, failed, unreachable, writable } = useProbeEditor()
  const rows = useProbeRows()
  if (known.length === 0) return null

  return (
    <div data-testid="probe-hosts-section" className="mt-6">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm font-semibold">{t('fleet_hosts.probe.title')}</h3>
        <button type="button" data-testid="probe-add" disabled={!writable || rows.length >= MAX_PROBE_HOSTS} onClick={startNewProbe}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50">
          <Plus size={14} />{t('fleet_hosts.probe.add')}
        </button>
      </div>
      <p className="text-xs text-text-muted mb-3">{t('fleet_hosts.probe.hint')}</p>
      {rows.length > 0 ? (
        <ul className="border border-border-subtle rounded-lg divide-y divide-border-subtle">
          {rows.map((r) => (
            <li key={r.id}>
              <button type="button" data-testid={`probe-row-${r.id}`} onClick={() => useFleetDraftStore.getState().selectProbe({ id: r.id, isNew: r.isNew })}
                className="w-full flex items-center gap-3 px-3 py-2 text-left cursor-pointer hover:bg-surface-secondary/50">
                <ProbeBadge row={r} />
                <span className="flex-1 truncate text-sm text-text-primary">{r.name}</span>
                <ProbeStatusDot status={r.status} />
              </button>
            </li>
          ))}
        </ul>
      ) : !ready ? (
        <p data-testid="probe-state" className="text-sm text-text-muted">{failed.length > 0 || unreachable.length > 0 ? t('fleet_hosts.probe.unreadable') : t('fleet_hosts.probe.loading')}</p>
      ) : (
        <p className="text-sm text-text-muted">{t('fleet_hosts.probe.empty')}</p>
      )}
    </div>
  )
}
