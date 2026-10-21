// spa/src/components/watch/host/StatusTab.tsx — the host page's 狀態 tab (monitoring tabs spec M44; plan MON-7b-2, MON-7c-2): the six cards, every field of the facts, then the full charts. The tab has no choice of its own to keep:
// the range and the scroll are the page's memory (6f-1), the hovered time is gone with the pointer (as on the 監控 tab), and the pause card reads again each time it is mounted.
import { useI18nStore } from '../../../stores/useI18nStore'
import type { HostRange } from '../../../lib/pool/host-history'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import { StatusCards } from './StatusCards'
import { FactsTable } from './FactsTable'
import { StatusCharts } from './StatusCharts'

export function StatusTab({ host, range, now, sourceAlias }: { host: MonitorHost; range: HostRange; now: number; /** The pool host the answering daemon reads as its own, for the latency card. */ sourceAlias: string | null }) {
  const t = useI18nStore((s) => s.t)
  const st = host.state
  return (
    <div data-testid="status-tab" className="flex flex-col gap-4">
      <StatusCards host={host} />
      <FactsTable pool={st.pool} facts={st.state === 'ok' ? st.facts : null} />
      <section className="flex flex-col gap-2">
        <h3 data-testid="status-charts-heading" className="flex items-baseline gap-2 text-xs font-medium text-text-secondary">
          {t('watch.host.status.charts.title')}
          <span className="text-[11px] font-normal text-text-muted">{t('watch.host.status.charts.note')}</span>
        </h3>
        <StatusCharts host={host} range={range} now={now} sourceAlias={sourceAlias} />
      </section>
    </div>
  )
}
