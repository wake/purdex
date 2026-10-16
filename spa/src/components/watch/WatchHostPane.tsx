// spa/src/components/watch/WatchHostPane.tsx — one host's tab of the watcher area (watcher area spec §6). W5a1: the header and
// 行程. The 資源 section (gauges, charts) is W5a2, the queue W5b1, and 設定 W5c1; they fill in under this header.
//
// Tab-hosted checklist: nothing here is state worth keeping across a tab switch (the readings come from the fleet store on every
// mount); the choices that are (the range, 狀態｜設定, the queue sub-tab) arrive with their controls in W5a2/W5b1/W5c1 and live
// outside the component (`lib/watch/view-memory`).
import { useMemo } from 'react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { useFleetStore, fleetHostsView, type FleetHostView } from '../../lib/fleet/store'
import type { FleetProc } from '../../lib/fleet/types'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostLookResolver, type HostLook } from '../../lib/host-look'
import { resolveHostColors } from '../../lib/host-color'
import { HostBadge } from '../HostBadge'
import { Section } from '../hosts/form-fields'

const ZONE_CLASS: Record<string, string> = {
  normal: 'text-status-success', warning: 'text-status-warning', danger: 'text-status-error', offline: 'text-text-muted', nodata: 'text-text-muted',
}

function isOffline(v: FleetHostView): boolean {
  return v.status !== 'ok'
}

function ProcTable({ testId, title, rows, unit, offline }: { testId: string; title: string; rows: FleetProc[]; unit: 'cpu' | 'mem'; offline: boolean }) {
  const t = useI18nStore((s) => s.t)
  const empty = offline ? t('watch.host.offline_no_data') : rows.length === 0 ? t('fleet.no_data') : null
  return (
    <div className="min-w-0" data-testid={testId}>
      <div className="text-xs text-text-secondary mb-1">{title}</div>
      <table className="w-full border-collapse text-xs">
        <thead>
          <tr className="text-text-muted text-left">
            <th className="font-normal py-1 pr-3 border-b border-border-subtle w-16">{t('watch.host.col_pid')}</th>
            <th className="font-normal py-1 pr-3 border-b border-border-subtle">{t('watch.host.col_process')}</th>
            <th className="font-normal py-1 border-b border-border-subtle text-right">{unit === 'cpu' ? t('watch.host.col_cpu') : t('watch.host.col_mem')}</th>
          </tr>
        </thead>
        <tbody>
          {empty !== null ? (
            <tr><td colSpan={3} className="py-3 text-text-muted">{empty}</td></tr>
          ) : rows.map((p) => (
            <tr key={p.pid}>
              <td className="py-1 pr-3 border-b border-border-subtle/50 tabular-nums text-text-muted">{p.pid}</td>
              <td className="py-1 pr-3 border-b border-border-subtle/50 font-mono text-text-primary truncate">{p.name}</td>
              <td className="py-1 border-b border-border-subtle/50 tabular-nums text-text-secondary text-right">
                {unit === 'cpu' ? `${p.cpu}%` : `${p.rss_mb.toLocaleString()} MB`}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function WatchHostPane({ pane }: PaneRendererProps) {
  const t = useI18nStore((s) => s.t)
  const fleetHostId = pane.content.kind === 'watch-host' ? pane.content.fleetHostId : ''
  const snapshot = useFleetStore((s) => s.snapshot)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const lookOf = useHostLookResolver()

  const view = useMemo(
    () => (snapshot ? fleetHostsView(snapshot, { hosts, hostOrder }).find((v) => v.id === fleetHostId) : undefined),
    [snapshot, hosts, hostOrder, fleetHostId],
  )

  if (!snapshot) return <p className="p-6 text-sm text-text-muted">{t('watch.host.no_fleet')}</p>
  if (!view) return <p className="p-6 text-sm text-text-muted">{t('watch.host.no_host')}</p>

  // The cell's look: a daemon host's comes from the App's host config, a probe host's from the wire.
  const look: Pick<HostLook, 'colors' | 'color' | 'icon' | 'iconWeight'> = view.hostConfigId
    ? lookOf(view.hostConfigId)
    : { colors: view.colors, icon: view.icon, iconWeight: view.icon_weight as HostLook['iconWeight'] }
  const colors = resolveHostColors({ colors: look.colors, color: look.color }, 'console')

  const offline = isOffline(view)
  const zone = offline ? 'offline' : view.fast ? view.fast.zone : 'nodata'
  const zoneWord = zone === 'nodata' ? t('fleet.no_data') : t(`fleet.zone.${zone}`)

  // "量測：" names the host the numbers come from: the primary (spec §6).
  const primaryConfig = hosts[snapshot.hostId]
  const primaryFleetHost = primaryConfig?.daemonId ? snapshot.hosts[primaryConfig.daemonId] : undefined
  const primaryAlias = primaryFleetHost?.alias || lookOf(snapshot.hostId).name || snapshot.hostId

  const alias = view.alias || view.name || view.id
  const lastOk = offline && view.last_ok_at !== null ? new Date(view.last_ok_at).toLocaleString() : null

  return (
    <div className="flex-1 min-h-0 h-full overflow-y-auto" data-testid={`watch-host-${view.id}`}>
      <div className="p-6 max-w-6xl">
        <div className="flex items-center gap-3 mb-5">
          <HostBadge colors={colors} icon={look.icon} iconWeight={look.iconWeight} box={20} inset={3} radius={5} lineColor="host" />
          <h2 className="text-lg font-semibold text-text-primary">{alias}</h2>
          <span className={`text-sm ${ZONE_CLASS[zone]}`} data-testid="zone-text">{zoneWord}</span>
          <span className="text-xs text-text-muted" data-testid="measure-source">
            {t('watch.host.measured', { alias: primaryAlias })}{lastOk !== null && t('watch.host.last_ok', { time: lastOk })}
          </span>
        </div>
        <Section title={t('watch.host.procs')}>
          <div className="grid grid-cols-2 gap-8">
            <ProcTable testId="procs-cpu" title={t('watch.host.top_cpu')} rows={view.slow?.top_cpu ?? []} unit="cpu" offline={offline} />
            <ProcTable testId="procs-mem" title={t('watch.host.top_mem')} rows={view.slow?.top_mem ?? []} unit="mem" offline={offline} />
          </div>
        </Section>
      </div>
    </div>
  )
}
