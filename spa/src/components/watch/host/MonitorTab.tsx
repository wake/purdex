// spa/src/components/watch/host/MonitorTab.tsx — the host page's 監控 tab (monitoring tabs spec M41 1–3, M42, M22, #3785; plan MON-6e). From top to bottom, only three parts: a row of six gauges, six mini charts (three to a row) that share one
// hovered time, and one block of jobs | CPU top 5 | memory top 5. It reads the pool only: the facts of a host that is ok, the samples of `useHostSeries` for the range it was given, the jobs store. A host that is not ok has its gauges
// as dashes, its charts stop at the last reading and run slanted to the right edge, and its process tables say it is offline — it never reads a fleet view's `slow` (#3785).
//
// `range` and the samples go together: a hole in a line is "more than 3 steps apart" and a step is 5 s for 30 minutes and under, a minute above, so a range must be drawn with the samples read for it (`useHostSeries` gives exactly those).
// Tab-hosted checklist: the hovered time is not the reader's own choice to keep (it is gone with the pointer), so nothing here must outlive a tab switch.
import { useMemo, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { GAUGES, gaugesOf, miniChartsOf } from '../../../lib/pool/host-charts'
import type { HostRange } from '../../../lib/pool/host-history'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import { useHostSeries } from '../../../lib/pool/use-host-series'
import type { PoolProc } from '../../../lib/pool/types'
import { useAlertsStore } from '../../../lib/pool/alerts-store'
import { alertRowsOf } from '../../../lib/pool/events-model'
import { usePoolStore } from '../../../lib/pool/store'
import { writeEventFilters } from '../../../lib/watch/view-memory'
import { openWatchArea } from '../../../features/workspace/lib/open-watch-area'
import { Gauge } from '../Gauge'
import { ProcTable, type ProcRow } from '../ProcTable'
import { HostJobs } from '../jobs/HostJobs'
import { MiniChart } from './MiniChart'

const DEAD_TONE = { wwd_down: 'warning', host_down: 'error', unknown: 'muted' } as const

/** The facts' processes as the tables' rows: the top five by CPU and by memory, each with its owner. */
function topProcs(procs: readonly PoolProc[]): { cpu: ProcRow[]; mem: ProcRow[] } {
  const row = (p: PoolProc): ProcRow => ({ pid: p.pid, name: p.name, cpu: Math.round(p.cpu_pct * 10) / 10, mem_mb: p.mem_mb, mem_kind: p.mem_kind === 'footprint' || p.mem_kind === 'rss' ? p.mem_kind : undefined, owner: p.owner })
  return {
    cpu: [...procs].sort((x, y) => y.cpu_pct - x.cpu_pct).slice(0, 5).map(row),
    mem: [...procs].sort((x, y) => y.mem_mb - x.mem_mb).slice(0, 5).map(row),
  }
}

export function MonitorTab({ host, range, now }: { host: MonitorHost; range: HostRange; now: number }) {
  const t = useI18nStore((s) => s.t)
  const st = host.state
  const facts = st.state === 'ok' ? st.facts : null
  const { samples } = useHostSeries(host.hostId, host.alias, range, facts)
  // The hovered time is one for the six cards (M42). It is kept as a time, not a place in the list: new samples arrive and the oldest leave the window under a pointer that has not moved. Another host or another range is another
  // set of samples, so the time is dropped there (reset while rendering, so going away and coming back does not bring it back).
  const [hoveredAt, setHoveredAt] = useState<number | null>(null)
  const scope = `${host.hostId}|${range}`
  const [seenScope, setSeenScope] = useState(scope)
  if (seenScope !== scope) { setSeenScope(scope); setHoveredAt(null) }
  const found = hoveredAt === null || seenScope !== scope ? -1 : samples.findIndex((s) => s.at === hoveredAt)
  const hoverIndex = found >= 0 ? found : null
  const onHover = (i: number | null) => setHoveredAt(i === null ? null : (samples[i]?.at ?? null))

  const gauges = useMemo(() => gaugesOf(facts, t), [facts, t])
  const defs = useMemo(() => miniChartsOf(samples, facts, range), [samples, facts, range])
  const dead = st.state === 'ok' ? null : { fromAt: st.lastFactsAt ?? 0, tone: DEAD_TONE[st.state] }
  const procs = useMemo(() => topProcs(facts?.procs ?? []), [facts])

  // This host's alerts in progress, owned the way the events tab owns them (a silence about this host that another host's list holds counts here, once; M41-1, 88 10-20).
  const alertsByHost = useAlertsStore((s) => s.byHost)
  const poolHosts = usePoolStore((s) => s.hosts)
  const liveAlerts = useMemo(
    () => {
      // The pool's list is empty while the pool cannot be read, though the page stays open on a kept host: this host is always among them.
      const known = poolHosts.map((h) => ({ hostId: h.host_id, alias: h.alias }))
      const all = known.some((h) => h.hostId === host.hostId) ? known : [...known, { hostId: host.hostId, alias: host.alias }]
      return alertRowsOf(alertsByHost, all).filter((r) => r.hostId === host.hostId && r.live)
    },
    [alertsByHost, poolHosts, host.hostId, host.alias],
  )
  const openAlerts = (): void => {
    writeEventFilters({ host: host.hostId, source: 'all', status: 'all' })
    openWatchArea({ tab: 'events' })
  }

  return (
    <div data-testid="monitor-tab" className="flex flex-col gap-3">
      <div data-testid="monitor-gauges" className="flex flex-wrap items-start gap-x-6 gap-y-2">
        {GAUGES.map((g, i) => <Gauge key={g.id} testId={`monitor-gauge-${g.id}`} label={t(g.labelKey)} reading={gauges[i]} />)}
        {liveAlerts.length > 0 && (
          <button
            type="button" data-testid="monitor-alerts" onClick={openAlerts} title={liveAlerts.map((r) => r.title).join('\n')}
            className={`ml-auto cursor-pointer text-xs hover:underline ${liveAlerts.some((r) => r.level === 'push') ? 'text-status-error-text' : 'text-status-warning-text'}`}
          >
            {t('watch.host.monitor.alerts', { n: liveAlerts.length })}
          </button>
        )}
      </div>
      <div data-testid="monitor-charts" className="grid grid-cols-3 gap-2">
        {defs.map((d) => <MiniChart key={d.id} def={d} samples={samples} range={range} now={now} hoverIndex={hoverIndex} onHover={onHover} dead={dead} />)}
      </div>
      <section data-testid="monitor-work">
        <h2 className="mb-2 flex items-baseline gap-2 text-sm font-medium text-text-primary">
          {t('watch.host.monitor.work')}
          <span className="text-[11px] font-normal text-text-muted">{t('watch.host.monitor.procs_every')}</span>
        </h2>
        <div data-testid="monitor-work-cols" className="grid grid-cols-3 gap-4">
          <HostJobs host={st.pool} />
          <ProcTable testId="procs-cpu" title={t('watch.host.top_cpu')} rows={procs.cpu} unit="cpu" offline={facts === null} />
          <ProcTable testId="procs-mem" title={t('watch.host.top_mem')} rows={procs.mem} unit="mem" offline={facts === null} />
        </div>
      </section>
    </div>
  )
}
