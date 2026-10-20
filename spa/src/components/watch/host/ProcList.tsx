// spa/src/components/watch/host/ProcList.tsx — the 行程 tab's process table (monitoring tabs spec M45, plan MON-8a): the top 12 by CPU or by memory, each with a bar relative to the table's largest value, the other reading dimmed, and who owns the process.
// The 監控 tab keeps its own, smaller `ProcTable`.
import { useI18nStore } from '../../../stores/useI18nStore'
import type { PoolProc } from '../../../lib/pool/types'
import { ownerWords } from '../ProcTable'

export type ProcBy = 'cpu' | 'mem'

/** The facts list a host's top 12 by CPU and its top 12 by memory; this takes the first `n` by one of them, largest first. */
// eslint-disable-next-line react-refresh/only-export-components
export function procRowsOf(procs: readonly PoolProc[], by: ProcBy, n = 12): PoolProc[] {
  const key = (p: PoolProc) => (by === 'cpu' ? p.cpu_pct : p.mem_mb)
  return [...procs].sort((a, b) => key(b) - key(a)).slice(0, n)
}

const mb = (v: number) => `${v.toLocaleString(undefined, { maximumFractionDigits: 1 })} MB`
const pct = (v: number) => `${Math.round(v * 10) / 10}%`

export function ProcList({ testId, title, rows, by, offline }: { testId: string; title: string; rows: readonly PoolProc[]; by: ProcBy; offline: boolean }) {
  const t = useI18nStore((s) => s.t)
  const main = (p: PoolProc) => (by === 'cpu' ? p.cpu_pct : p.mem_mb)
  const max = rows.reduce((m, p) => Math.max(m, main(p)), 0)
  const empty = offline ? t('watch.host.offline_no_data') : rows.length === 0 ? t('fleet.no_data') : null
  return (
    <div className="min-w-0" data-testid={testId}>
      <div className="text-xs text-text-secondary mb-1">{title}</div>
      <table className="w-full border-collapse text-xs">
        <thead>
          <tr className="text-text-muted text-left">
            <th className="font-normal py-1 pr-3 border-b border-border-subtle w-16">{t('watch.host.col_pid')}</th>
            <th className="font-normal py-1 pr-3 border-b border-border-subtle">{t('watch.host.col_process')}</th>
            <th className="font-normal py-1 pr-3 border-b border-border-subtle text-right">{by === 'cpu' ? t('watch.host.col_cpu') : t('watch.host.col_mem')}</th>
            <th className="font-normal py-1 border-b border-border-subtle text-right">{by === 'cpu' ? t('watch.host.col_mem') : t('watch.host.col_cpu')}</th>
          </tr>
        </thead>
        <tbody>
          {empty !== null ? (
            <tr><td colSpan={4} className="py-3 text-text-muted">{empty}</td></tr>
          ) : rows.map((p) => (
            <tr key={p.pid}>
              <td className="py-1 pr-3 border-b border-border-subtle/50 tabular-nums text-text-muted">{p.pid}</td>
              <td className="py-1 pr-3 border-b border-border-subtle/50 font-mono text-text-primary truncate">
                {p.name}
                {p.owner && <span data-testid={`${testId}-owner-${p.pid}`} className="ml-2 px-1 py-0.5 rounded bg-surface-tertiary text-[10px] text-text-secondary font-sans">{ownerWords(p.owner, t)}</span>}
              </td>
              <td className="py-1 pr-3 border-b border-border-subtle/50 text-right">
                <div className="flex items-center justify-end gap-2">
                  <span className="relative inline-block h-1.5 w-16 rounded-sm bg-surface-tertiary overflow-hidden" aria-hidden="true">
                    <span data-testid={`${testId}-bar-${p.pid}`} className={`absolute inset-y-0 left-0 ${by === 'cpu' ? 'bg-accent' : 'bg-queue-light'}`} style={{ width: `${max > 0 ? (main(p) / max) * 100 : 0}%` }} />
                  </span>
                  <span data-testid={`${testId}-main-${p.pid}`} className="tabular-nums text-text-primary">
                    {by === 'mem' && p.mem_kind === 'rss' && (
                      <span className="mr-1.5 text-[10px] text-text-muted/70 uppercase tracking-wide cursor-help" title={t('watch.host.mem_rss_hint')} aria-label={t('watch.host.mem_rss_hint')}>RSS</span>
                    )}
                    {by === 'cpu' ? pct(p.cpu_pct) : mb(p.mem_mb)}
                  </span>
                </div>
              </td>
              <td data-testid={`${testId}-sub-${p.pid}`} className="py-1 border-b border-border-subtle/50 tabular-nums text-text-muted text-right">{by === 'cpu' ? mb(p.mem_mb) : pct(p.cpu_pct)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
