// spa/src/components/watch/WatchHostPane.tsx — one host's tab of the watcher area (watcher area spec §6): the header, 可接工作 (on the pool), 資源 and
// 重指令排隊 (W5b1, daemon hosts) and 行程. 設定 is W5c1; it fills in under this header.
//
// Tab-hosted checklist: the readings come from the fleet store on every mount, but the choices the reader makes (the range and the queue sub-tab now;
// 狀態｜設定 in W5c1) live outside the component (`lib/watch/view-memory`), because the pane unmounts
// when another tab is shown. W5a2 adds the 資源 section (gauges, charts) and the range switch.
import { useEffect, useMemo, useState } from 'react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { useFleetStore, fleetHostsView, type FleetHostView } from '../../lib/fleet/store'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostLookResolver, type HostLook } from '../../lib/host-look'
import { resolveHostColors } from '../../lib/host-color'
import { HostBadge } from '../HostBadge'
import { Section } from '../hosts/form-fields'
import { SegmentControl } from '../settings/SegmentControl'
import { fleetHistory, type HistoryRange } from '../../lib/fleet/history'
import { METRICS, gaugeOf } from '../../lib/watch/metrics'
import { RANGES, stepMs } from '../../lib/watch/series'
import { readHostPage, readRange, writeHostPage, writeRange, type HostPage } from '../../lib/watch/view-memory'
import { usePoolCellModels } from '../../lib/pool/fleet-bridge'
import { fleetViewOfPoolHost, matchPoolHost } from '../../lib/pool/fleet-bridge'
import { usePoolHostDaemons } from '../../lib/pool/daemons'
import { usePoolStore } from '../../lib/pool/store'
import { useFactsHistory } from '../../lib/pool/use-facts-history'
import { useSeriesHistory } from '../../lib/pool/use-series-history'
import type { ZonePoint } from '../../lib/pool/history'
import type { PoolProc } from '../../lib/pool/types'
import { Gauge } from './Gauge'
import { AvailRow } from './AvailRow'
import { ZoneBand } from './ZoneBand'
import { ProcTable, type ProcRow } from './ProcTable'
import { ResidentsTable } from './ResidentsTable'
import { MetricRow, TimeAxis } from './MetricChart'
import { QueueSection } from './QueueSection'
import { WatchSettings } from './WatchSettings'
import { HostJobs } from './jobs/HostJobs'
import { openWatchArea } from '../../features/workspace/lib/open-watch-area'

const RANGE_OPTS = (t: (k: string) => string) => RANGES.map((r) => ({ value: r, label: t(`watch.host.range.${r}`) }))

/** 資源: five gauges from the latest frame, one chart row per reading from the App's in-memory history, one shared axis. */
function Resources({ view, range, now, zones, notLocal, rev }: { view: FleetHostView; range: HistoryRange; now: number; zones: ZonePoint[]; notLocal: boolean; rev: number }) {
  const t = useI18nStore((s) => s.t)
  const offline = isOffline(view)
  // Memoised per step (1 s for 5 分, 10 s otherwise, the pace at which the history can change): the rows only redraw when their points do.
  const at = Math.floor(now / stepMs(range)) * stepMs(range)
  const hostId = view.id
  // `rev` is not read inside: a finished series load changes the history without changing anything else, so it is what makes this recompute.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const series = useMemo(() => METRICS.map((d) => ({ def: d, points: fleetHistory.series(hostId, d.k, range, at) })), [hostId, range, at, rev])
  return (
    <div data-testid="metrics">
      <div data-testid="gauges" className="flex flex-wrap gap-x-10 gap-y-4 mb-5">
        {METRICS.map((d) => <Gauge key={d.k} label={t(d.shortKey)} reading={offline || !view.fast ? null : gaugeOf(d.k, view.fast, t)} testId={`gauge-${d.k}`} />)}
      </div>
      {notLocal ? (
        // The daemon cannot read this host's history (it does not forward): the gauges above stay, the 5 minute chart does not.
        <p data-testid="history-not-local" className="text-[11px] text-text-muted mb-1">{t('watch.host.history_not_local')}</p>
      ) : (<>
        {range === '5m' && <ZoneBand points={zones} now={at} />}
        <ChartNote />
        {series.map((s) => <MetricRow key={s.def.k} def={s.def} points={s.points} now={at} range={range} offline={offline} />)}
        <TimeAxis points={series[0].points} now={at} range={range} />
      </>)}
    </div>
  )
}

function ChartNote() {
  const t = useI18nStore((s) => s.t)
  return <p className="text-[11px] text-text-muted mb-1">{t('watch.host.chart_note')}</p>
}

const ZONE_CLASS: Record<string, string> = {
  normal: 'text-status-success-text', warning: 'text-status-warning-text', danger: 'text-status-error-text', unknown: 'text-text-muted', offline: 'text-text-muted', nodata: 'text-text-muted',
}

function isOffline(v: FleetHostView): boolean {
  return v.status !== 'ok'
}

/** The facts' processes as the table's rows: the top five by CPU and by memory, each with its owner. */
function topProcs(procs: PoolProc[]): { cpu: ProcRow[]; mem: ProcRow[] } {
  const row = (p: PoolProc): ProcRow => ({ pid: p.pid, name: p.name, cpu: Math.round(p.cpu_pct * 10) / 10, rss_mb: p.mem_mb, mem_mb: p.mem_mb, mem_kind: p.mem_kind === 'footprint' || p.mem_kind === 'rss' ? p.mem_kind : undefined, owner: p.owner })
  return {
    cpu: [...procs].sort((x, y) => y.cpu_pct - x.cpu_pct).slice(0, 5).map(row),
    mem: [...procs].sort((x, y) => y.mem_mb - x.mem_mb).slice(0, 5).map(row),
  }
}

export function WatchHostPane({ pane }: PaneRendererProps) {
  const t = useI18nStore((s) => s.t)
  const poolHostId = pane.content.kind === 'watch-host' ? pane.content.poolHostId : undefined
  const contentFleetId = pane.content.kind === 'watch-host' ? pane.content.fleetHostId ?? '' : ''
  // The view memory is keyed by what names the tab: the pool host id, else (a tab from before the pool) the fleet id.
  const memoryKey = poolHostId ?? contentFleetId
  const snapshot = useFleetStore((s) => s.snapshot)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const lookOf = useHostLookResolver()
  // The range outlives this component (the pane unmounts on a tab switch): the memory is the source, the state only re-renders.
  const [range, setRangeState] = useState<HistoryRange>(() => readRange(memoryKey))
  const setRange = (r: HistoryRange) => { writeRange(memoryKey, r); setRangeState(r) }
  const [page, setPageState] = useState<HostPage>(() => readHostPage(memoryKey))
  const setPage = (p: HostPage) => { writeHostPage(memoryKey, p); setPageState(p) }

  const all = useMemo(() => (snapshot ? fleetHostsView(snapshot, { hosts, hostOrder }) : []), [snapshot, hosts, hostOrder])
  const poolHosts = usePoolStore((s) => s.hosts)
  const daemons = usePoolHostDaemons()
  // A tab with a poolHostId finds its fleet host through the pool host (transitional, until MON-6f-2 drops the fleet here); an older tab names the fleet host itself.
  const fleetHostId = useMemo(() => {
    if (!poolHostId) return contentFleetId
    const ph = poolHosts.find((h) => h.host_id === poolHostId)
    return (ph && fleetViewOfPoolHost(all, ph, daemons)?.id) || ''
  }, [poolHostId, contentFleetId, poolHosts, all, daemons])
  const view = useMemo(() => all.find((v) => v.id === fleetHostId), [all, fleetHostId])
  // The other daemon hosts: 設定 reads their `watch` rows to compare `targets`.
  const peers = useMemo(() => all.filter((v) => v.kind === 'daemon'), [all])
  // On the resource pool the numbers are wwd's facts (the model's view carries them in the fleet's shape); before it answers, the fleet's as ever.
  const views = useMemo(() => (view ? [view] : []), [view])
  const { models, active: poolActive } = usePoolCellModels(views)
  const model = poolActive && view ? models[0] : null
  const pool = model?.pool ?? null
  const poolHost = model && view ? matchPoolHost(poolHosts, view, daemons) : null
  const live = pool?.state === 'ok' ? (poolHost?.facts ?? null) : null
  // The 5 minute range on a live host: wwd's own last 5 minutes (read once), and each sample's zone for the band.
  const hist = useFactsHistory(fleetHostId, poolHost?.alias ?? null, live !== null && range === '5m', live)
  // 1 h / 6 h / 24 h: the daemon's 1 minute series, read once; `seriesRev` moves when a load finishes so the charts redraw at once.
  const seriesRev = useSeriesHistory(fleetHostId, poolHost?.alias ?? null, range, live !== null, live?.mem.total_mb ?? null)

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

  const shown = model ? model.view : view
  const offline = isOffline(shown)
  const zone = offline ? 'offline' : pool?.state === 'ok' && pool.zone ? pool.zone : shown.fast ? shown.fast.zone : 'nodata'
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
            {live && poolHost
              ? t('watch.host.wwd_age', { version: poolHost.version, s: Math.max(0, Math.round((now - live.at) / 1000)) })
              : <>{t('watch.host.measured', { alias: primaryAlias })}{lastOk !== null && t('watch.host.last_ok', { time: lastOk })}</>}
          </span>
          <span className="flex-1" />
          {page === 'status' && (
            <span data-testid="range-switch" role="group" aria-label={t('watch.host.range_label')}>
              <SegmentControl options={RANGE_OPTS(t)} value={range} onChange={setRange} />
            </span>
          )}
          <span data-testid="page-switch" role="group" aria-label={t('watch.host.page_label')}>
            <SegmentControl options={[{ value: 'status', label: t('watch.host.page.status') }, { value: 'settings', label: t('watch.host.page.settings') }]} value={page} onChange={setPage} />
          </span>
        </div>
        {page === 'settings' ? (
          <WatchSettings kind={view.kind === 'probe' ? 'probe' : 'daemon'} fleetHostId={view.id} hostConfigId={view.hostConfigId} poolAlias={view.kind === 'daemon' ? alias : undefined} offline={offline} onGotoProbe={(id) => openWatchArea({ host: id })}
            peerIds={peers.filter((v) => v.id !== view.id).flatMap((v) => (v.hostConfigId ? [v.hostConfigId] : []))} />
        ) : (<>
        {pool && <AvailRow pool={pool} exceptions={poolHost?.facts?.exceptions ?? []} at={poolHost?.facts?.at ?? 0} now={now} />}
        <Section title={t('watch.host.resources', { range: t(`watch.host.range.${range}`) })}>
          <Resources view={shown} range={range} now={now} zones={hist.zones} notLocal={live !== null && range === '5m' && hist.load === 'host_not_local'} rev={seriesRev} />
        </Section>
        {view.kind === 'daemon' && (offline || shown.slow?.queue) && <QueueSection fleetHostId={view.id} hostConfigId={view.hostConfigId} queue={shown.slow?.queue} offline={offline} />}
        {view.kind === 'daemon' && poolHost && <HostJobs host={poolHost} />}
        <Section title={t('watch.host.procs')}>
          <div className="grid grid-cols-2 gap-8">
            <ProcTable testId="procs-cpu" title={t('watch.host.top_cpu')} rows={live ? topProcs(live.procs).cpu : (view.slow?.top_cpu ?? [])} unit="cpu" offline={offline} />
            <ProcTable testId="procs-mem" title={t('watch.host.top_mem')} rows={live ? topProcs(live.procs).mem : (view.slow?.top_mem ?? [])} unit="mem" offline={offline} />
          </div>
          {live && <ResidentsTable residents={live.residents} />}
        </Section>
        </>)}
      </div>
    </div>
  )
}
