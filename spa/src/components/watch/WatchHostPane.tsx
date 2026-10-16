// spa/src/components/watch/WatchHostPane.tsx — one host's tab of the watcher area (watcher area spec §6): the header, 資源 and
// 行程. The queue is W5b1, and 設定 W5c1; they fill in under this header.
//
// Tab-hosted checklist: the readings come from the fleet store on every mount, but the choices the reader makes (the range now;
// 狀態｜設定 and the queue sub-tab in W5b1/W5c1) live outside the component (`lib/watch/view-memory`), because the pane unmounts
// when another tab is shown. W5a2 adds the 資源 section (gauges, charts) and the range switch.
import { useEffect, useMemo, useState } from 'react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { useFleetStore, fleetHostsView, type FleetHostView } from '../../lib/fleet/store'
import type { FleetProc } from '../../lib/fleet/types'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostLookResolver, type HostLook } from '../../lib/host-look'
import { resolveHostColors } from '../../lib/host-color'
import { HostBadge } from '../HostBadge'
import { Section } from '../hosts/form-fields'
import { SegmentControl } from '../settings/SegmentControl'
import { fleetHistory, type HistoryRange } from '../../lib/fleet/history'
import { METRICS } from '../../lib/watch/metrics'
import { RANGES, stepMs } from '../../lib/watch/series'
import { readRange, writeRange } from '../../lib/watch/view-memory'
import { Gauge } from './Gauge'
import { MetricRow, TimeAxis } from './MetricChart'

const RANGE_OPTS = (t: (k: string) => string) => RANGES.map((r) => ({ value: r, label: t(`watch.host.range.${r}`) }))

/** 資源: five gauges from the latest frame, one chart row per reading from the App's in-memory history, one shared axis. */
function Resources({ view, range, now }: { view: FleetHostView; range: HistoryRange; now: number }) {
  const offline = isOffline(view)
  // Memoised per step (1 s for 5 分, 10 s otherwise, the pace at which the history can change): the rows only redraw when their points do.
  const at = Math.floor(now / stepMs(range)) * stepMs(range)
  const hostId = view.id
  const series = useMemo(() => METRICS.map((d) => ({ def: d, points: fleetHistory.series(hostId, d.k, range, at) })), [hostId, range, at])
  return (
    <div data-testid="metrics">
      <div data-testid="gauges" className="flex flex-wrap gap-x-10 gap-y-4 mb-5">
        {METRICS.map((d) => <Gauge key={d.k} def={d} fast={view.fast} offline={offline} />)}
      </div>
      <ChartNote />
      {series.map((s) => <MetricRow key={s.def.k} def={s.def} points={s.points} now={at} range={range} offline={offline} />)}
      <TimeAxis points={series[0].points} now={at} range={range} />
    </div>
  )
}

function ChartNote() {
  const t = useI18nStore((s) => s.t)
  return <p className="text-[11px] text-text-muted mb-1">{t('watch.host.chart_note')}</p>
}

const ZONE_CLASS: Record<string, string> = {
  normal: 'text-status-success-text', warning: 'text-status-warning-text', danger: 'text-status-error-text', offline: 'text-text-muted', nodata: 'text-text-muted',
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
  // The range outlives this component (the pane unmounts on a tab switch): the memory is the source, the state only re-renders.
  const [range, setRangeState] = useState<HistoryRange>(() => readRange(fleetHostId))
  const setRange = (r: HistoryRange) => { writeRange(fleetHostId, r); setRangeState(r) }

  const view = useMemo(
    () => (snapshot ? fleetHostsView(snapshot, { hosts, hostOrder }).find((v) => v.id === fleetHostId) : undefined),
    [snapshot, hosts, hostOrder, fleetHostId],
  )

  // The charts end at "now": it moves with the snapshot, whose 1 s pushes redraw the pane (the history has no subscription).
  // A 5 s tick (one interval per pane, cleared on unmount) keeps the axis and 「累積中」 moving while no snapshot arrives (a stale fleet).
  const [tick, setTick] = useState(0)
  useEffect(() => {
    const id = setInterval(() => setTick((n) => n + 1), 5000)
    return () => clearInterval(id)
  }, [])
  // eslint-disable-next-line react-hooks/purity, react-hooks/exhaustive-deps
  const now = useMemo(() => Date.now(), [snapshot, tick])

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
          <span className="flex-1" />
          <span data-testid="range-switch" role="group" aria-label={t('watch.host.range_label')}>
            <SegmentControl options={RANGE_OPTS(t)} value={range} onChange={setRange} />
          </span>
        </div>
        <Section title={t('watch.host.resources', { range: t(`watch.host.range.${range}`) })}>
          <Resources view={view} range={range} now={now} />
        </Section>
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
