// spa/src/components/watch/host/StatusTab.tsx — the host page's 狀態 tab (monitoring tabs spec M44; plan MON-7b-2): the six cards, then every field of the facts. The full charts (MON-7c-2) join below. The tab has no choice of its own to keep:
// the range and the scroll are the page's memory (6f-1), and the pause card reads again each time it is mounted.
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import { StatusCards } from './StatusCards'
import { FactsTable } from './FactsTable'

export function StatusTab({ host }: { host: MonitorHost }) {
  const st = host.state
  return (
    <div data-testid="status-tab" className="flex flex-col gap-4">
      <StatusCards host={host} />
      <FactsTable pool={st.pool} facts={st.state === 'ok' ? st.facts : null} />
    </div>
  )
}
