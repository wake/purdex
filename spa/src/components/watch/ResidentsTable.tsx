// spa/src/components/watch/ResidentsTable.tsx — 常駐程式: the programs a session leaves running on the host (`facts.residents`). Shown only; the
// App never ends one (wwd spec: shown, never killed), so there is no button here.
import { useI18nStore } from '../../stores/useI18nStore'
import type { PoolResident } from '../../lib/pool/types'

export function ResidentsTable({ residents, offline = false }: { residents: PoolResident[]; offline?: boolean }) {
  const t = useI18nStore((s) => s.t)
  return (
    <div data-testid="residents" className="min-w-0">
      <div className="text-xs text-text-secondary mb-1">{t('watch.host.residents')}</div>
      <table className="w-full border-collapse text-xs">
        <thead>
          <tr className="text-text-muted text-left">
            <th className="font-normal py-1 pr-3 border-b border-border-subtle">{t('watch.host.res_session')}</th>
            <th className="font-normal py-1 pr-3 border-b border-border-subtle">{t('watch.host.res_kind')}</th>
            <th className="font-normal py-1 pr-3 border-b border-border-subtle text-right">{t('watch.host.res_count')}</th>
            <th className="font-normal py-1 border-b border-border-subtle text-right">{t('watch.host.col_mem')}</th>
          </tr>
        </thead>
        <tbody>
          {offline || residents.length === 0 ? (
            <tr><td colSpan={4} className="py-3 text-text-muted">{offline ? t('watch.host.offline_no_data') : t('fleet.no_data')}</td></tr>
          ) : residents.map((r) => (
            <tr key={`${r.session}-${r.kind}`} data-testid={`resident-${r.session}-${r.kind}`}>
              <td className="py-1 pr-3 border-b border-border-subtle/50 font-mono text-text-primary">{r.session === 'orphan' ? t('watch.host.owner.orphan') : r.session}</td>
              <td className="py-1 pr-3 border-b border-border-subtle/50 font-mono text-text-primary">{r.kind}</td>
              <td className="py-1 pr-3 border-b border-border-subtle/50 tabular-nums text-text-secondary text-right">{r.count}</td>
              <td className="py-1 border-b border-border-subtle/50 tabular-nums text-text-secondary text-right">{`${r.mem_mb.toLocaleString(undefined, { maximumFractionDigits: 1 })} MB`}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
