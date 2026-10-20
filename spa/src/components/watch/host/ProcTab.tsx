// spa/src/components/watch/host/ProcTab.tsx — the host page's 行程 tab (monitoring tabs spec M45, plan MON-8a): the host's top 12 processes by CPU and by memory, side by side, and under them the resident programs.
// It reads the facts of a host that is ok (`facts.procs` is already the top 12 by each, `facts.residents` the programs a session left running); a host that is not ok has nothing to read, so the tables say it is offline.
// The right column under the tables is for the undeclared heavy commands (MON-8c); the one-hour timeline joins the title row in MON-8b. Nothing here is the reader's own choice to keep.
import { useMemo } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import { ResidentsTable } from '../ResidentsTable'
import { ProcList, procRowsOf } from './ProcList'

export function ProcTab({ host }: { host: MonitorHost; now: number }) {
  const t = useI18nStore((s) => s.t)
  const facts = host.state.state === 'ok' ? host.state.facts : null
  const cpu = useMemo(() => procRowsOf(facts?.procs ?? [], 'cpu'), [facts])
  const mem = useMemo(() => procRowsOf(facts?.procs ?? [], 'mem'), [facts])
  const offline = facts === null
  return (
    <div data-testid="proc-tab" className="flex flex-col gap-6">
      <section>
        <h2 className="mb-2 flex items-baseline gap-2 text-sm font-medium text-text-primary">
          {t('watch.host.page.procs')}
          <span className="text-[11px] font-normal text-text-muted">{t('watch.host.proc.every')}</span>
        </h2>
        <div className="grid grid-cols-2 gap-8">
          <ProcList testId="procs-cpu" title={t('watch.host.top_cpu12')} rows={cpu} by="cpu" offline={offline} />
          <ProcList testId="procs-mem" title={t('watch.host.top_mem12')} rows={mem} by="mem" offline={offline} />
        </div>
      </section>
      <div className="grid grid-cols-2 gap-8">
        <ResidentsTable residents={facts?.residents ?? []} offline={offline} />
        <div />
      </div>
    </div>
  )
}
