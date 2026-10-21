// spa/src/components/watch/host/ObservationTables.tsx — the 行程 tab's tables of what the host's wwd observed (monitoring tabs spec M45, plan MON-8c): the heavy commands that ran with no job declared, grouped by command signature over 7 days.
// The observation records table joins in MON-8d. A table that cannot be drawn says why instead of looking empty: the admin-only refusal, an older wwd, a daemon without the route, a host that is not ok, a failed read.
import { useI18nStore } from '../../../stores/useI18nStore'
import type { ObservationLoad } from '../../../lib/pool/host-observations'
import type { ObservationsAvailability } from '../../../lib/pool/use-host-observations'
import type { UndeclaredGroup, UndeclaredPage } from '../../../lib/pool/observations-api'
import { clock } from '../../../lib/pool/words'

const mb = (v: number) => `${v.toLocaleString(undefined, { maximumFractionDigits: 1 })} MB`
const secs = (v: number) => v.toLocaleString(undefined, { maximumFractionDigits: 1 })

function Last({ g }: { g: UndeclaredGroup }) {
  const t = useI18nStore((s) => s.t)
  const who = g.lastSession === 'orphan' ? t('watch.host.owner.orphan') : g.lastSession
  return (
    <span data-testid="undeclared-last" title={g.lastCommand}>
      {clock(g.lastAt)}・{who}{g.lastVia === 'codex' && <span className="ml-1 px-1 py-0.5 rounded bg-surface-tertiary text-[10px] text-text-secondary">{t('watch.host.obs.via_codex')}</span>}
    </span>
  )
}

export function UndeclaredTable({ availability, page, load, stateWord }: { availability: ObservationsAvailability; page: UndeclaredPage | null; load: ObservationLoad; stateWord: string }) {
  const t = useI18nStore((s) => s.t)
  // Why the table cannot show a fresh reading (a page kept from an earlier read still shows, under the note).
  const note = availability === 'no_route' ? t('watch.host.obs.no_route')
    : availability === 'host_down' ? t('watch.host.obs.host_down', { state: stateWord })
    : load === 'forbidden' ? t('watch.host.obs.forbidden')
    : page !== null && !page.known ? t('watch.host.obs.too_old')
    : load === 'error' ? t('watch.host.obs.error')
    : page === null && load !== 'idle' ? t('watch.host.obs.loading')
    : null
  const groups = page && page.known ? page.groups : []
  return (
    <div data-testid="undeclared" className="min-w-0">
      <div className="text-xs text-text-secondary mb-1">
        {t('watch.host.obs.undeclared_title')}
        <span className="ml-2 text-text-muted">{t('watch.host.obs.undeclared_hint')}</span>
      </div>
      {note !== null && <p data-testid="undeclared-note" className="py-1 text-xs text-text-muted">{note}</p>}
      {groups.length > 0 ? (
        <table className="w-full border-collapse text-xs">
          <thead>
            <tr className="text-text-muted text-left">
              <th className="font-normal py-1 pr-3 border-b border-border-subtle">{t('watch.host.obs.col_signature')}</th>
              <th className="font-normal py-1 pr-3 border-b border-border-subtle text-right">{t('watch.host.obs.col_count')}</th>
              <th className="font-normal py-1 pr-3 border-b border-border-subtle text-right">{t('watch.host.obs.col_cpu_s')}</th>
              <th className="font-normal py-1 pr-3 border-b border-border-subtle text-right">{t('watch.host.obs.col_mem_peak')}</th>
              <th className="font-normal py-1 border-b border-border-subtle">{t('watch.host.obs.col_last')}</th>
            </tr>
          </thead>
          <tbody>
            {groups.map((g) => (
              <tr key={g.signature} data-testid={`undeclared-row-${g.signature}`}>
                <td className="py-1 pr-3 border-b border-border-subtle/50 font-mono text-text-primary truncate">{g.signature}</td>
                <td data-testid="undeclared-count" className="py-1 pr-3 border-b border-border-subtle/50 tabular-nums text-text-secondary text-right">{g.count}</td>
                <td data-testid="undeclared-cpu" className="py-1 pr-3 border-b border-border-subtle/50 tabular-nums text-text-secondary text-right">{secs(g.cpuSeconds)}</td>
                <td data-testid="undeclared-mem" className="py-1 pr-3 border-b border-border-subtle/50 tabular-nums text-text-secondary text-right">{mb(g.maxMemMb)}</td>
                <td className="py-1 border-b border-border-subtle/50 text-text-secondary"><Last g={g} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : note === null && page !== null && page.known && (
        <p className="py-3 text-xs text-text-muted">{t('watch.host.obs.undeclared_none')}</p>
      )}
      {page?.truncated && <p data-testid="undeclared-truncated" className="pt-1 text-[11px] text-text-muted">{t('watch.host.obs.truncated')}</p>}
    </div>
  )
}
