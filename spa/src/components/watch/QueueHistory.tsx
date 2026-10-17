// spa/src/components/watch/QueueHistory.tsx — 歷史: the last 3 hours of one class's commands from the gate's record (watcher area spec §6.1).
// One row per command, newest first; the stacked bar of every row is on ONE shared scale (hatched = waited, solid = ran, red = failed).
// The class switch lives in view memory (the pane unmounts on a tab switch); the data is read by `useGateHistory`.
import { useRef, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { SegmentControl } from '../settings/SegmentControl'
import { useGateHistory } from '../../lib/watch/history-read'
import { barParts, HEAVY_FAULTS, LONG_WAIT_S, NO_WAIT_S, scaleMinutes, toRows, type HistoryRow } from '../../lib/watch/history-view'
import { readHistoryClass, writeHistoryClass, type HistoryClass } from '../../lib/watch/view-memory'

const TH = 'font-normal py-1 pr-3 border-b border-border-subtle text-left whitespace-nowrap align-bottom'
const TD = 'py-1 pr-3 border-b border-border-subtle/50'
const STRIPES = 'repeating-linear-gradient(135deg, color-mix(in srgb, var(--text-secondary) 60%, transparent) 0 1.5px, transparent 1.5px 4px)'
const RUN = 'color-mix(in srgb, var(--text-secondary) 55%, transparent)'
const BAR_W = 150
const DASH = '—'

type T = (k: string, p?: Record<string, string | number>) => string

const pad = (n: number) => String(n).padStart(2, '0')
const clock = (ms: number) => { const d = new Date(ms); return `${pad(d.getHours())}:${pad(d.getMinutes())}` }
const clockS = (ms: number) => { const d = new Date(ms); return `${clock(ms)}:${pad(d.getSeconds())}` }
const dur = (t: T, s: number) => (s < 60 ? t('watch.history.sec', { n: Math.round(s) }) : t('watch.history.min', { n: +(s / 60).toFixed(1) }))
const mem = (t: T, mb: number) => (mb >= 1024 ? t('watch.history.gb', { n: +(mb / 1024).toFixed(1) }) : t('watch.history.mb', { n: Math.round(mb).toLocaleString() }))

function Swatch({ bg, className = '' }: { bg?: string; className?: string }) {
  return <span className={`inline-block w-4 h-2 rounded-sm align-middle ${className}`} style={bg ? { background: bg } : undefined} />
}

function Bar({ row, scaleMin, t }: { row: HistoryRow; scaleMin: number; t: T }) {
  const { wait, run } = barParts(row, scaleMin)
  const waited = row.waitS >= NO_WAIT_S
  return (
    <span className="flex items-center gap-2 whitespace-nowrap">
      <span className="relative h-2 shrink-0 rounded-sm bg-border-subtle/50" style={{ width: BAR_W }}>
        <span className="absolute inset-y-0 left-0 flex" style={{ width: `${(wait + run) * 100}%` }} data-testid="hist-bar" data-wait={wait} data-run={run}>
          {wait > 0 && <span className="h-full rounded-l-sm" data-testid="hist-bar-wait" style={{ width: `${(wait / (wait + run || 1)) * 100}%`, background: STRIPES }} />}
          {run > 0 && (
            <span
              className={`h-full flex-1 ${wait > 0 ? 'rounded-r-sm' : 'rounded-sm'} ${row.failed ? 'bg-status-error' : ''}`}
              data-testid="hist-bar-run" data-failed={row.failed || undefined} style={row.failed ? undefined : { background: RUN }}
            />
          )}
        </span>
      </span>
      <span className="text-[11px] tabular-nums">
        <span className={waited ? (row.waitS > LONG_WAIT_S ? 'text-status-warning-text' : 'text-text-secondary') : 'text-text-muted'}>
          {waited ? t('watch.history.wait', { t: dur(t, row.waitS) }) : t('watch.history.no_wait')}
        </span>
        <span className="text-text-muted"> · </span>
        <span className={row.started ? (row.failed ? 'text-status-error' : 'text-text-secondary') : 'text-text-muted'}>
          {row.started ? t('watch.history.run', { t: dur(t, row.runS) }) : t('watch.history.never_ran')}
        </span>
      </span>
    </span>
  )
}

function Tip({ row, top, t }: { row: HistoryRow; top: number; t: T }) {
  const r = row.record
  const why = r.waitReasons.map((w) => { const k = `watch.history.why.${w}`; const s = t(k); return s === k ? w : s }).join('、')
  const params = Object.entries(r.applied).sort(([a], [b]) => a.localeCompare(b))
  const src = (s: string) => { const k = `watch.history.source.${s}`; const v = t(k); return v === k ? s : v }
  const where = [row.project, row.session].filter((x) => x !== '').join(' · ')
  return (
    <div data-testid="hist-tip" className="absolute z-30 left-24 mt-1 w-[360px] rounded-lg border border-border-default bg-surface-elevated shadow-xl px-3 py-2 text-xs pointer-events-none" style={{ top }}>
      <div data-testid="hist-tip-command" className="font-mono text-text-primary break-all">{row.command}</div>
      {where !== '' && <div data-testid="hist-tip-where" className="mt-0.5 text-text-secondary break-all">{where}</div>}
      <div data-testid="hist-tip-times" className="mt-1 text-text-muted tabular-nums">
        {t('watch.history.tip.queued', { t: clockS(row.arriveMs) })}
        {row.started && <>{' · '}{t('watch.history.tip.started', { t: clockS(row.startMs) })}{' · '}{t('watch.history.tip.ended', { t: clockS(row.endMs) })}</>}
      </div>
      {why !== '' && <div data-testid="hist-tip-why" className="text-text-muted">{t('watch.history.tip.why', { why })}</div>}
      {params.length > 0 && (
        <div data-testid="hist-tip-params" className="mt-1.5 pt-1.5 border-t border-border-subtle">
          <div className="text-text-muted">{t('watch.history.tip.params')}</div>
          {params.map(([k, p]) => (
            <div key={k} className="font-mono text-text-secondary">{k} = {p.value}{p.source !== '' && <span className="font-sans text-text-muted"> ({src(p.source)})</span>}</div>
          ))}
        </div>
      )}
    </div>
  )
}

export function QueueHistory({ fleetHostId, hostConfigId }: { fleetHostId: string; hostConfigId: string | undefined }) {
  const t = useI18nStore((s) => s.t)
  // The class outlives this component (the pane unmounts on a tab switch): the memory is the source, the state only re-renders.
  const [cls, setClsState] = useState<HistoryClass>(() => readHistoryClass(fleetHostId))
  const setCls = (c: HistoryClass) => { writeHistoryClass(fleetHostId, c); setClsState(c) }
  const state = useGateHistory(hostConfigId, cls, true)
  const [tip, setTip] = useState<{ key: string; top: number } | null>(null)
  const wrap = useRef<HTMLDivElement>(null)

  const rows = state.kind === 'ok' ? toRows(state.records) : []
  const scaleMin = scaleMinutes(rows)
  const tipRow = tip ? rows.find((r) => r.key === tip.key) : undefined
  const kind = t(`watch.history.kind.${cls}`)
  const classWord = (c: string) => (c === 'heavy' ? t('watch.queue.class.heavy') : t('watch.queue.class.light'))

  return (
    <div data-testid="queue-history" ref={wrap} className="relative">
      <div className="flex items-center gap-3 mb-2 text-xs">
        <span data-testid="history-title" className="text-text-secondary">{t('watch.history.title', { kind, n: rows.length })}</span>
        <span className="flex-1" />
        <span data-testid="history-class" role="group" aria-label={t('watch.history.class_label')}>
          <SegmentControl options={[{ value: 'heavy', label: t('watch.history.kind.heavy') }, { value: 'light', label: t('watch.history.kind.light') }]} value={cls} onChange={setCls} />
        </span>
      </div>
      {state.kind === 'loading' && <p data-testid="history-loading" className="text-xs text-text-muted">{t('watch.history.loading')}</p>}
      {state.kind === 'error' && <p data-testid="history-error" data-reason={state.reason} className="text-xs text-status-error-text">{t(`watch.history.err_${state.reason}`)}</p>}
      {state.kind === 'ok' && (
        <>
          {rows.length === 0 ? <p data-testid="history-empty" className="text-xs text-text-muted">{t('watch.history.empty')}</p> : (
            <table className="w-full border-collapse text-xs">
              <thead>
                <tr className="text-text-muted">
                  <th className={TH}>{t('watch.history.col_queued')}</th><th className={TH}>{t('watch.history.col_class')}</th>
                  <th className={TH}>{t('watch.history.col_command')}</th><th className={TH}>{t('watch.history.col_session')}</th>
                  <th className={TH} data-testid="hist-bar-head">
                    <div className="text-text-secondary">{t('watch.history.col_bar')}</div>
                    <div className="mt-0.5 flex items-center gap-1.5 text-[10px]">
                      <Swatch bg={STRIPES} />{t('watch.history.legend_wait')}<Swatch bg={RUN} className="ml-1" />{t('watch.history.legend_run')}<Swatch className="ml-1 bg-status-error" />{t('watch.history.legend_failed')}
                      <span data-testid="hist-scale" className="ml-1">{t('watch.history.scale', { n: scaleMin })}</span>
                    </div>
                  </th>
                  <th className={TH}>{t('watch.history.col_cores')}</th><th className={TH}>{t('watch.history.col_mem')}</th>
                  <th className={TH}>{t('watch.history.col_faults')}</th><th className={TH}>{t('watch.history.col_exit')}</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => (
                  <tr
                    key={r.key} data-testid="hist-row" className={tip?.key === r.key ? 'bg-accent-muted' : ''}
                    onMouseEnter={(e) => setTip({ key: r.key, top: e.currentTarget.getBoundingClientRect().bottom - (wrap.current?.getBoundingClientRect().top ?? 0) })}
                    onMouseLeave={() => setTip(null)}
                  >
                    <td className={`${TD} tabular-nums text-text-muted`}>{clock(r.arriveMs)}</td>
                    <td className={TD}><span className={r.cls === 'heavy' ? 'text-text-secondary' : 'text-text-muted'}>{classWord(r.cls)}</span></td>
                    <td className={`${TD} font-mono text-text-primary whitespace-nowrap max-w-[210px] truncate`}>{r.command}</td>
                    <td className={`${TD} text-text-secondary whitespace-nowrap`}>{r.session !== '' ? r.session.slice(0, 8) : DASH}</td>
                    <td className={TD}><Bar row={r} scaleMin={scaleMin} t={t} /></td>
                    <td data-testid="hist-cores" className={`${TD} tabular-nums text-text-secondary whitespace-nowrap`}>{r.cores === null ? DASH : t('watch.history.cores', { n: r.cores.toFixed(1) })}</td>
                    <td data-testid="hist-mem" className={`${TD} tabular-nums text-text-secondary whitespace-nowrap`}>{r.maxRssMb === null ? DASH : mem(t, r.maxRssMb)}</td>
                    <td data-testid="hist-faults" className={`${TD} tabular-nums whitespace-nowrap ${r.majorFaults !== null && r.majorFaults > HEAVY_FAULTS ? 'text-status-error-text' : 'text-text-secondary'}`}>
                      {r.majorFaults === null ? DASH : t('watch.history.times', { n: r.majorFaults.toLocaleString() })}
                    </td>
                    <td data-testid="hist-exit" className={`${TD} tabular-nums ${r.failed ? 'text-status-error-text' : 'text-text-muted'}`}>{r.started ? r.exit : DASH}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {state.skipped > 0 && <p data-testid="history-skipped" className="mt-1 text-[10.5px] text-text-muted">{t('watch.history.skipped', { n: state.skipped })}</p>}
          {rows.length > 0 && <p className="mt-2 text-xs text-text-muted">{t('watch.history.hint')}</p>}
        </>
      )}
      {tip && tipRow && <Tip row={tipRow} top={tip.top} t={t} />}
    </div>
  )
}
