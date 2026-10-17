import { useFleetStore } from '../../lib/fleet/store'
import type { ProbeEdit } from '../../lib/fleet/config-store'
import { useFleetDraftStore } from '../../lib/fleet/config-draft'
import { ProbeHostEditor } from './ProbeHostEditor'
import { ProbeBadge, ProbeStatusDot } from './probe-rows'
import { useProbeRows } from './use-probe-rows'
import { useProbeEditor } from './useProbeEditor'

/**
 * One probe host's own entry on the Hosts page (fleet spec §8.2): a header (badge, name, the zone word or 離線, which daemon
 * measures it, the last good reading when offline), 「樣式與連線」 (the form, written to BOTH daemons), 「連線檢查」, and 刪除.
 * The draft, the confirmations and the delete prompt live in `useFleetDraftStore`, so a tab switch keeps them. A new probe host
 * (the sidebar's `+`) starts as a blank draft here.
 */
export function ProbeHostPage({ probeId }: { probeId: string }) {
  const { t, entries, writable, run } = useProbeEditor()
  const primaryHostId = useFleetStore((s) => s.primaryHostId)
  const session = useFleetDraftStore((s) => s.probe)
  const deleting = useFleetDraftStore((s) => s.deleting)
  const busy = useFleetDraftStore((s) => s.ui.busy)
  const drafts = useFleetDraftStore.getState
  const row = useProbeRows().find((r) => r.id === probeId)
  const entry = entries.find((e) => e.id === probeId)
  const mine = session && session.initial.id === probeId ? session : null

  const remove = async () => {
    drafts().setDeleting(null)
    const edit: ProbeEdit = { kind: 'remove', id: probeId }
    const ok = await run(edit, 'page')
    if (ok) drafts().selectProbe(null)
  }

  const name = entry?.name || row?.name || mine?.draft.name || t('fleet_hosts.entry.new_name')
  const host = row?.host ?? null
  const offline = !host || host.status !== 'ok'
  const zoneWord = !host ? t('fleet_hosts.status.unknown') : host.status !== 'ok' ? t('fleet.zone.offline') : host.fast ? t(`fleet.zone.${host.fast.zone}`) : t('fleet.no_data')
  const primaryAlias = (primaryHostId && entry?.aliases[primaryHostId]) || host?.alias || ''

  return (
    <div data-testid="probe-page" data-probe-id={probeId}>
      <div className="flex items-center gap-3 mb-4">
        <ProbeBadge row={row ?? { icon: mine?.draft.icon ?? '', iconWeight: mine?.draft.icon_weight ?? '', colors: mine?.draft.colors }} box={28} />
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h2 data-testid="probe-page-name" className="text-base font-semibold truncate">{name}</h2>
            <ProbeStatusDot status={row?.status ?? 'unknown'} />
            <span data-testid="probe-page-zone" className="text-xs text-text-secondary">{zoneWord}</span>
          </div>
          <div className="text-xs text-text-muted">
            {primaryAlias && <span data-testid="probe-page-measured">{t('fleet_hosts.entry.measured_by', { alias: primaryAlias })}</span>}
            {offline && host?.last_ok_at ? <span data-testid="probe-page-last-ok"> · {t('fleet_hosts.entry.last_ok', { time: new Date(host.last_ok_at).toLocaleString() })}</span> : null}
          </div>
        </div>
      </div>

      <ProbeHostEditor probeId={probeId} />

      {entry && (
        <div className="mt-6">
          {deleting === probeId ? (
            <div data-testid="probe-delete-prompt" className="flex items-center gap-2 text-xs">
              <span className="text-text-secondary">{t('fleet_hosts.entry.delete_confirm', { name })}</span>
              <button type="button" data-testid="probe-delete-confirm" disabled={busy || !writable} onClick={() => void remove()}
                className="px-3 py-1 rounded bg-red-500 text-white cursor-pointer disabled:opacity-50">{t('common.delete')}</button>
              <button type="button" data-testid="probe-delete-cancel" onClick={() => drafts().setDeleting(null)}
                className="px-3 py-1 rounded bg-surface-tertiary text-text-secondary cursor-pointer">{t('common.cancel')}</button>
            </div>
          ) : (
            <button type="button" data-testid="probe-delete" disabled={!writable || busy} onClick={() => drafts().setDeleting(probeId)}
              className="px-3 py-1.5 rounded text-xs text-red-400 border border-red-400/40 cursor-pointer disabled:opacity-50">{t('common.delete')}</button>
          )}
        </div>
      )}
    </div>
  )
}
