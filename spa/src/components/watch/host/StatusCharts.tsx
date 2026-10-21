// spa/src/components/watch/host/StatusCharts.tsx — the full charts under the facts table on the host page's 狀態 tab (monitoring tabs spec M42, M43, M44; plan MON-7c-2): the zone strip, then six groups of charts (CPU, memory, disk, the pool,
// processes, the host and wwd) with the two number cards at the end of the last. The charts are `lib/pool/host-charts-full.ts`'s; the samples are `useHostSeries`' for the range, read together as the 監控 tab does. There is ONE hovered time for all
// of them (M42), kept as a time, not a place: the samples move under a pointer that has not. Another host or another range drops it. A host that is not ok has the strip and the charts stop at the last reading and run slanted to the right edge (M22).
import { useMemo, useState } from 'react'
import { Cpu, HardDrives, ListBullets, Memory, Stack, Wrench } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { fullChartsOf, type ChartGroupId, type TileDef } from '../../../lib/pool/host-charts-full'
import type { HostRange } from '../../../lib/pool/host-history'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import { useHostSeries } from '../../../lib/pool/use-host-series'
import { MiniChart } from './MiniChart'
import { ZoneStrip } from './ZoneStrip'

const DEAD_TONE = { wwd_down: 'warning', host_down: 'error', unknown: 'muted' } as const
const GROUP_ICON = { cpu: Cpu, mem: Memory, disk: HardDrives, pool: Stack, proc: ListBullets, host: Wrench } as const satisfies Record<ChartGroupId, unknown>

function Tile({ tile }: { tile: TileDef }) {
  const t = useI18nStore((s) => s.t)
  return (
    <div data-testid={`tile-${tile.id}`} className="rounded-lg border border-border-default bg-surface-secondary px-3 py-2">
      <div className="text-xs text-text-secondary">{t(tile.titleKey)}</div>
      <div className="mt-1 text-sm font-semibold text-text-primary">{tile.text === null ? '—' : t(tile.text.key, tile.text.params)}</div>
    </div>
  )
}

export function StatusCharts({ host, range, now, sourceAlias }: { host: MonitorHost; range: HostRange; now: number; /** The pool host the answering daemon reads as its own: whose read of this host the latency card tells. */ sourceAlias: string | null }) {
  const t = useI18nStore((s) => s.t)
  const st = host.state
  const facts = st.state === 'ok' ? st.facts : null
  const { samples } = useHostSeries(host.hostId, host.alias, range, facts)
  const [hoveredAt, setHoveredAt] = useState<number | null>(null)
  const scope = `${host.hostId}|${range}`
  const [seenScope, setSeenScope] = useState(scope)
  if (seenScope !== scope) { setSeenScope(scope); setHoveredAt(null) }
  const found = hoveredAt === null || seenScope !== scope ? -1 : samples.findIndex((s) => s.at === hoveredAt)
  const hoverIndex = found >= 0 ? found : null
  const onHover = (i: number | null) => setHoveredAt(i === null ? null : (samples[i]?.at ?? null))

  const pool = st.pool
  const groups = useMemo(
    () => fullChartsOf(samples, facts, range, { platform: pool.platform, rttMs: pool.rtt_ms ?? null, local: host.local, sourceAlias }),
    [samples, facts, range, pool.platform, pool.rtt_ms, host.local, sourceAlias],
  )
  const dead = st.state === 'ok' ? null : { fromAt: st.lastFactsAt ?? 0, tone: DEAD_TONE[st.state] }

  return (
    <div data-testid="status-charts" className="flex flex-col gap-4">
      <ZoneStrip samples={samples} range={range} now={now} dead={dead} />
      {groups.map((g) => {
        const Icon = GROUP_ICON[g.id]
        return (
          <section key={g.id} data-testid={`chart-group-${g.id}`}>
            <h3 className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-text-secondary"><Icon size={14} />{t(`watch.host.full.group.${g.id}`)}</h3>
            <div className="grid grid-cols-2 gap-2 xl:grid-cols-3">
              {g.charts.map((d) => <MiniChart key={d.id} variant="full" def={d} samples={samples} range={range} now={now} hoverIndex={hoverIndex} onHover={onHover} dead={dead} />)}
              {g.tiles.map((tile) => <Tile key={tile.id} tile={tile} />)}
            </div>
          </section>
        )
      })}
    </div>
  )
}
