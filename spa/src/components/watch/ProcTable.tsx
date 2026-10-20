// spa/src/components/watch/ProcTable.tsx — one process table of the host tab (top by CPU / by memory). On the resource pool each row also
// carries who owns the process (`procs[].owner`): a session, a job, codex, a resident program or the system; none → no chip.
import { useI18nStore } from '../../stores/useI18nStore'
import type { ProcOwner } from '../../lib/pool/types'

/** One row. The pool gives `mem_mb`. */
export interface ProcRow { pid: number; name: string; cpu: number; mem_mb?: number; mem_kind?: 'footprint' | 'rss'; owner?: ProcOwner | null }

const OWNER_KINDS = ['session', 'job', 'codex', 'resident', 'system']

/** The chip's words: the kind, and the id it names (a job id; for the rest the session's ref, so two sessions' processes tell apart). A kind this App does not know shows as it is. */
function ownerWords(o: ProcOwner, t: (k: string, p?: Record<string, string | number>) => string): string {
  if (!OWNER_KINDS.includes(o.kind)) return o.kind
  const id = o.kind === 'job' ? o.id : o.kind === 'system' ? undefined : (o.session ?? o.id)
  return t(`watch.host.owner.${o.kind}`) + (id ? ` ${id}` : '')
}

export function ProcTable({ testId, title, rows, unit, offline }: { testId: string; title: string; rows: ProcRow[]; unit: 'cpu' | 'mem'; offline: boolean }) {
  const t = useI18nStore((s) => s.t)
  const empty = offline ? t('watch.host.offline_no_data') : rows.length === 0 ? t('fleet.no_data') : null
  return (
    <div className="min-w-0" data-testid={testId}>
      <div className="text-xs text-text-secondary mb-1">{title}</div>
      <table className="w-full border-collapse text-xs">
        <thead>
          <tr className="text-text-muted text-left">
            <th className="font-normal py-1 pr-3 border-b border-border-subtle w-16">{t('watch.host.col_pid')}</th>
            <th className="font-normal py-1 pr-3 border-b border-border-subtle">{t('watch.host.col_process')}</th>
            <th className="font-normal py-1 border-b border-border-subtle text-right">{unit === 'cpu' ? t('watch.host.col_cpu') : t('watch.host.col_mem')}</th>
          </tr>
        </thead>
        <tbody>
          {empty !== null ? (
            <tr><td colSpan={3} className="py-3 text-text-muted">{empty}</td></tr>
          ) : rows.map((p) => (
            <tr key={p.pid}>
              <td className="py-1 pr-3 border-b border-border-subtle/50 tabular-nums text-text-muted">{p.pid}</td>
              <td className="py-1 pr-3 border-b border-border-subtle/50 font-mono text-text-primary truncate">
                {p.name}
                {p.owner && <span data-testid={`proc-owner-${p.pid}`} className="ml-2 px-1 py-0.5 rounded bg-surface-tertiary text-[10px] text-text-secondary font-sans">{ownerWords(p.owner, t)}</span>}
              </td>
              <td className="py-1 border-b border-border-subtle/50 tabular-nums text-text-secondary text-right">
                {unit === 'cpu' ? `${p.cpu}%` : (
                  <>
                    {p.mem_kind === 'rss' && (
                      <span
                        className="mr-1.5 text-[10px] text-text-muted/70 uppercase tracking-wide cursor-help"
                        title={t('watch.host.mem_rss_hint')}
                        aria-label={t('watch.host.mem_rss_hint')}
                      >RSS</span>
                    )}
                    {`${(p.mem_mb ?? 0).toLocaleString(undefined, { maximumFractionDigits: 1 })} MB`}
                  </>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
