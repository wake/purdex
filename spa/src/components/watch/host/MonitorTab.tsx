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
  // The hovered time is one for the six cards (M42); a new range is a new set of samples, so it does not carry over.
  const [hover, setHover] = useState<{ range: HostRange; index: number } | null>(null)
  const hoverIndex = hover !== null && hover.range === range ? hover.index : null
  const onHover = (i: number | null) => setHover(i === null ? null : { range, index: i })

  const gauges = useMemo(() => gaugesOf(facts, t), [facts, t])
  const defs = useMemo(() => miniChartsOf(samples, facts, range), [samples, facts, range])
  const dead = st.state === 'ok' ? null : { fromAt: st.lastFactsAt ?? 0, tone: DEAD_TONE[st.state] }
  const procs = useMemo(() => topProcs(facts?.procs ?? []), [facts])

  return (
    <div data-testid="monitor-tab" className="flex flex-col gap-3">
      <div data-testid="monitor-gauges" className="flex flex-wrap items-start gap-x-6 gap-y-2">
        {GAUGES.map((g, i) => <Gauge key={g.id} testId={`monitor-gauge-${g.id}`} label={t(g.labelKey)} reading={gauges[i]} />)}
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
