// spa/src/components/watch/host/ObservationTables.tsx — the 行程 tab's tables of what the host's wwd observed (monitoring tabs spec M45, plan MON-8c): the heavy commands that ran with no job declared, grouped by command signature over 7 days.
// The observation records (MON-8d) are the second table: 24 hours, the newest 10 unless the reader expands, older pages on request. A table that cannot be drawn says why instead of looking empty: the admin-only refusal, an older wwd, a daemon without the route, a host that is not ok, a failed read.
import { useI18nStore } from '../../../stores/useI18nStore'
import type { ObservationLoad, ObservationRecords, OlderLoad } from '../../../lib/pool/host-observations'
import type { ObservationsAvailability } from '../../../lib/pool/use-host-observations'
import type { PoolObservation, UndeclaredGroup, UndeclaredPage } from '../../../lib/pool/observations-api'
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

/** Why a table cannot show a fresh reading (what was read earlier still shows, under the note). `known` is null while nothing was read. */
function noteOf(t: ReturnType<typeof useI18nStore.getState>['t'], availability: ObservationsAvailability, load: ObservationLoad, known: boolean | null, stateWord: string): string | null {
  return availability === 'no_route' ? t('watch.host.obs.no_route')
    : availability === 'host_down' ? t('watch.host.obs.host_down', { state: stateWord })
    : load === 'forbidden' ? t('watch.host.obs.forbidden')
    : known === false ? t('watch.host.obs.too_old')
    : load === 'error' ? t('watch.host.obs.error')
    : known === null && load !== 'idle' ? t('watch.host.obs.loading')
    : null
}

export function UndeclaredTable({ availability, page, load, stateWord }: { availability: ObservationsAvailability; page: UndeclaredPage | null; load: ObservationLoad; stateWord: string }) {
  const t = useI18nStore((s) => s.t)
  // Why the table cannot show a fresh reading (a page kept from an earlier read still shows, under the note).
  const note = noteOf(t, availability, load, page === null ? null : page.known, stateWord)
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

export const RECORDS_SHOWN = 10

/** 1 m 05 s, 2 h 03 m: a length, to the second under a minute and to the minute over an hour. */
// eslint-disable-next-line react-refresh/only-export-components
export function lengthOf(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  if (s < 60) return `${s} s`
  if (s < 3600) return `${Math.floor(s / 60)} m ${String(s % 60).padStart(2, '0')} s`
  return `${Math.floor(s / 3600)} h ${String(Math.floor((s % 3600) / 60)).padStart(2, '0')} m`
}

function Record({ o, now }: { o: PoolObservation; now: number }) {
  const t = useI18nStore((s) => s.t)
  const who = o.session === 'orphan' ? t('watch.host.owner.orphan') : o.session
  const live = o.endedAt === null
  const cell = 'py-1 pr-3 border-b border-border-subtle/50'
  return (
    <tr data-testid={`obs-row-${o.id}`}>
      <td className={`${cell} tabular-nums text-text-secondary whitespace-nowrap`}>{clock(o.startedAt)}</td>
      <td data-testid="obs-length" className={`${cell} tabular-nums whitespace-nowrap ${live ? 'text-status-warning-text' : 'text-text-secondary'}`}>
        {live ? `${lengthOf(now - o.startedAt)} ${t('watch.host.obs.running')}` : lengthOf(o.endedAt! - o.startedAt)}
      </td>
      <td className={`${cell} text-text-secondary whitespace-nowrap`}>
        {who}{o.via === 'codex' && <span className="ml-1 px-1 py-0.5 rounded bg-surface-tertiary text-[10px] text-text-secondary">{t('watch.host.obs.via_codex')}</span>}
      </td>
      <td className={`${cell} font-mono text-text-primary whitespace-nowrap`}>{o.signature}</td>
      <td data-testid="obs-command" title={o.command} className={`${cell} font-mono text-text-secondary max-w-64 truncate`}>{o.command}</td>
      <td data-testid="obs-cpu" className={`${cell} tabular-nums text-text-secondary text-right`}>{secs(o.cpuSeconds)}</td>
      <td data-testid="obs-cores" className={`${cell} tabular-nums text-text-secondary text-right`}>{(o.cpuPeak / 100).toLocaleString(undefined, { maximumFractionDigits: 1 })}</td>
      <td data-testid="obs-mem" className={`${cell} tabular-nums text-text-secondary text-right`}>{mb(o.memPeakMb)}</td>
      <td data-testid="obs-procs" className={`${cell} tabular-nums text-text-secondary text-right`}>{o.procsPeak}</td>
    </tr>
  )
}

export function RecordsTable({ availability, records, load, olderLoad, stateWord, now, expanded, onToggle, onOlder }: {
  availability: ObservationsAvailability; records: ObservationRecords | null; load: ObservationLoad; olderLoad: OlderLoad; stateWord: string; now: number
  expanded: boolean; onToggle: () => void; onOlder: () => void
}) {
  const t = useI18nStore((s) => s.t)
  const note = noteOf(t, availability, load, records === null ? null : records.known, stateWord)
  const list = records && records.known ? records.list : []
  const shown = expanded ? list : list.slice(0, RECORDS_SHOWN)
  const head = 'font-normal py-1 pr-3 border-b border-border-subtle'
  return (
    <div data-testid="obs-records" className="min-w-0">
      <div className="text-xs text-text-secondary mb-1">
        {t('watch.host.obs.records_title')}
        <span className="ml-2 text-text-muted">{t('watch.host.obs.records_hint')}</span>
      </div>
      {note !== null && <p data-testid="obs-note" className="py-1 text-xs text-text-muted">{note}</p>}
      {shown.length > 0 ? (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-xs">
            <thead>
              <tr className="text-text-muted text-left">
                <th className={head}>{t('watch.host.obs.col_start')}</th>
                <th className={head}>{t('watch.host.obs.col_length')}</th>
                <th className={head}>{t('watch.host.obs.col_session')}</th>
                <th className={head}>{t('watch.host.obs.col_sig')}</th>
                <th className={head}>{t('watch.host.obs.col_cmd')}</th>
                <th className={`${head} text-right`}>{t('watch.host.obs.col_cpu_s')}</th>
                <th className={`${head} text-right`}>{t('watch.host.obs.col_cores')}</th>
                <th className={`${head} text-right`}>{t('watch.host.obs.col_mem')}</th>
                <th className={`${head} text-right`}>{t('watch.host.obs.col_procs')}</th>
              </tr>
            </thead>
            <tbody>{shown.map((o) => <Record key={o.id} o={o} now={now} />)}</tbody>
          </table>
        </div>
      ) : note === null && records !== null && records.known && (
        <p className="py-3 text-xs text-text-muted">{t('watch.host.obs.records_none')}</p>
      )}
      {records?.known && (list.length > RECORDS_SHOWN || records.nextUntil !== null) && (
        <p className="pt-2 flex flex-wrap items-center gap-3 text-xs">
          {list.length > RECORDS_SHOWN && (
            <button type="button" data-testid="obs-toggle" onClick={onToggle} className="text-accent hover:underline">
              {expanded ? t('watch.host.obs.show_newest', { n: RECORDS_SHOWN }) : t('watch.host.obs.show_all', { n: list.length })}
            </button>
          )}
          {records.nextUntil !== null && (
            <button type="button" data-testid="obs-older" disabled={olderLoad === 'loading'} onClick={onOlder} className="text-accent hover:underline disabled:opacity-40">
              {olderLoad === 'loading' ? t('watch.host.obs.loading_older') : t('watch.host.obs.load_older')}
            </button>
          )}
          {olderLoad === 'error' && <span data-testid="obs-older-error" className="text-text-muted">{t('watch.host.obs.older_error')}</span>}
        </p>
      )}
    </div>
  )
}
