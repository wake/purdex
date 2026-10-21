// spa/src/components/watch/host/FactsTable.tsx — every field of the facts record, in two columns (monitoring tabs spec M44; plan MON-7b-2). The rows are `factRowsOf`'s; a field that is not there is a faint `null`, never 0.
import { useI18nStore } from '../../../stores/useI18nStore'
import { factRowsOf, type FactRow } from '../../../lib/pool/host-status'
import { clock } from '../../../lib/pool/words'
import type { PoolFacts, PoolHost } from '../../../lib/pool/types'

function Row({ row }: { row: FactRow }) {
  const t = useI18nStore((s) => s.t)
  const word = row.wordKey ? t(row.wordKey, row.wordParams) : null
  return (
    <tr>
      <td className="py-0.5 pr-3 align-top font-mono text-[11px] text-text-muted whitespace-nowrap">{row.key}</td>
      <td data-testid={`fact-${row.key}`} data-null={row.value === null ? 'true' : undefined} className={`py-0.5 text-xs break-all ${row.value === null ? 'text-text-muted opacity-60' : 'text-text-primary'}`}>
        {row.value === null ? 'null' : row.value}
        {word !== null && <span className="ml-1 text-text-muted">({word})</span>}
      </td>
    </tr>
  )
}

/** `facts` null (the host is not ok): one line saying there is no reading. */
export function FactsTable({ pool, facts }: { pool: PoolHost; facts: PoolFacts | null }) {
  const t = useI18nStore((s) => s.t)
  const rows = facts ? factRowsOf(pool, facts) : []
  const half = Math.ceil(rows.length / 2)
  return (
    <section data-testid="facts-table">
      <h3 data-testid="facts-title" className="mb-1.5 text-xs font-medium text-text-secondary">
        {facts ? t('watch.host.status.facts.title', { time: clock(facts.at) }) : t('watch.host.status.facts.title_none')}
      </h3>
      {facts === null
        ? <p className="text-xs text-text-muted">{t('watch.host.status.facts.none')}</p>
        : (
          <div className="grid grid-cols-1 gap-x-6 lg:grid-cols-2">
            {[rows.slice(0, half), rows.slice(half)].map((col, i) => (
              <table key={i} data-testid="facts-col" className="w-full self-start"><tbody>{col.map((r) => <Row key={r.key} row={r} />)}</tbody></table>
            ))}
          </div>
        )}
    </section>
  )
}
