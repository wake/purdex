// spa/src/components/watch/jobs/QueueFlow.tsx — 「任務佇列」's live view (monitoring tabs spec M24, M26–M34): the summary line, the queue pool (one cell per host: the jobs it sent and why they wait) and the worker pool (one cell
// per worker: a row per slot class). Static (M28): nothing here animates. The cards and the rows are buttons that select a job; its detail opens beside the list. The data is `lib/pool/queue-flow`.
import { useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { HardDrives, Stack } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useSidebarHostBadgeGeometry } from '../../../hooks/useSidebarHostBadgeGeometry'
import { resolveHostBadge, resolveHostColors } from '../../../lib/host-color'
import { mss } from '../../../lib/watch/queue-time'
import { jobLine, type QueueEntry } from '../../../lib/pool/jobs-lanes'
import { whenText } from '../../../lib/pool/jobs-view'
import { jobKey } from '../../../lib/pool/jobs-view-memory'
import type { PoolJob } from '../../../lib/pool/jobs-types'
import type { PoolHostResolvedLook } from '../../../lib/pool/host-looks'
import { candidateWorkersOf, elapsedSince, flowSummaryOf, poolCellsOf, workerCellsOf, type Busy, type FlowHost, type PoolCell, type SlotRow, type Why, type WorkerCell } from '../../../lib/pool/queue-flow'
import { queueStripOf } from '../../../lib/pool/queue-strip'
import { livenessWordOf, reasonWord } from '../../../lib/pool/words'
import { QueueSegments } from '../../fleet/QueueStrip'
import { HostBadge } from '../../HostBadge'
import { QueueRoutes, type ActiveCard, type CandidateSpec } from './QueueRoutes'

type T = (key: string, opts?: Record<string, string | number>) => string
/** A host as the page gets it: `useMonitorHosts` gives the order, the state and the look. */
export type FlowMonitorHost = FlowHost & { look: PoolHostResolvedLook }

const CLASSES = ['release', 'merge', 'dev']
const SUMMARY_SEGMENTS = 10

function Badge({ look, size }: { look: PoolHostResolvedLook | undefined; size: number }) {
  const { lineColor } = useSidebarHostBadgeGeometry()
  const b = resolveHostBadge(look ?? {}, 'console')
  return <HostBadge colors={b.colors} icon={b.icon} iconWeight={b.iconWeight} box={size} inset={Math.max(2, Math.round(size / 6))} radius={3} lineColor={lineColor} />
}

function busyWord(b: Busy, t: T, now: number): string {
  switch (b.kind) {
    case 'worker_off': return reasonWord('worker_off', t)
    case 'code': return reasonWord(b.code, t)
    case 'full': return reasonWord('slots_full', t)
    case 'paused': return b.until !== null ? t('watch.jobs.flow.paused_until', { time: whenText(b.until, now), name: b.name }) : reasonWord(`exception:${b.name}`, t)
    case 'unreadable': return livenessWordOf(t, b.state)
  }
}

function whyWord(w: Why, t: T, now: number): string {
  switch (w.kind) {
    case 'held': return t('watch.jobs.flow.why.held')
    case 'no_host': return w.labels.length > 0 ? t('watch.jobs.flow.why.no_host', { labels: w.labels.join(' ') }) : t('watch.jobs.flow.why.no_host_any')
    case 'busy': return w.hosts.map((h) => `${h.alias} ${busyWord(h.busy, t, now)}`).join('・')
  }
}

/** What the pointer is on (a short-lived state of this view, not the reader's selection): it lights the routes that belong to it. */
type Hover =
  | { kind: 'job'; key: string }
  | { kind: 'queued'; key: string }
  | { kind: 'pool'; id: string }
  | { kind: 'worker'; id: string }
interface Lit { pools: ReadonlySet<string>; workers: ReadonlySet<string> }
interface Pick { selected: string | null; onSelect: (key: string | null) => void; onHover: (h: Hover | null) => void; lit: Lit }

const LIT_RING = 'ring-1 ring-border-active'
/** The pointer events of a thing that lights routes. */
const hoverProps = (onHover: Pick['onHover'], h: Hover) => ({ onMouseEnter: () => onHover(h), onMouseLeave: () => onHover(null), onFocus: () => onHover(h), onBlur: () => onHover(null) })

/** A card or a row the reader can press: it selects the job, the same press lets go. */
function Press({ jobId, hover, selected, onSelect, onHover, className, attrs, children }: { jobId: string; hover: Hover; className: string; attrs: Record<string, string>; children: ReactNode } & Pick) {
  const on = selected === jobId
  return (
    <button type="button" aria-pressed={on} onClick={() => onSelect(on ? null : jobId)} {...hoverProps(onHover, hover)} className={`${className} ${on ? LIT_RING : ''}`} {...attrs}>{children}</button>
  )
}

function QueueRow({ entry, now, ...pick }: { entry: QueueEntry; now: number } & Pick) {
  const t = useI18nStore((s) => s.t)
  const { job, held, long } = entry
  const cls = CLASSES.includes(job.class) ? t(`watch.jobs.class.${job.class}`) : job.class
  return (
    <li>
      <Press jobId={jobKey(job.origin, job.id)} hover={{ kind: 'queued', key: jobKey(job.origin, job.id) }} {...pick} attrs={{ 'data-q': jobKey(job.origin, job.id), title: jobLine(job) }} className={`flex w-full items-center gap-1.5 rounded px-1 py-0.5 text-left text-[11px] ${held ? 'opacity-50' : ''}`}>
        <span className="shrink-0 rounded border border-current px-1 text-[10px] leading-4 text-text-secondary">{cls}</span>
        <span className="min-w-0 flex-1 truncate font-mono text-text-primary">{jobLine(job)}</span>
        {held && <span className="shrink-0 text-[10px] text-text-muted">{t('watch.jobs.flow.held_tag')}</span>}
        <span data-testid="flow-wait" className={`shrink-0 tabular-nums ${long ? 'text-status-warning-text' : 'text-text-muted'}`}>{mss(job.created_at, now)}</span>
      </Press>
    </li>
  )
}

function PoolCellView({ cell, state, look, now, ...pick }: { cell: PoolCell; state: FlowHost['state']; look: PoolHostResolvedLook | undefined; now: number } & Pick) {
  const t = useI18nStore((s) => s.t)
  const n = cell.rows.length
  const grow = n > 0 ? 1.2 + Math.min(4, n) * 0.25 : 0.7
  const sub = !cell.readable
    ? t('watch.jobs.flow.unreadable', { state: livenessWordOf(t, state.state) })
    : cell.longestS !== null ? t('watch.jobs.flow.waiting', { n, time: mss(now - cell.longestS * 1000, now) }) : n > 0 ? t('watch.jobs.flow.waiting_held', { n }) : t('watch.jobs.flow.nothing_waiting')
  return (
    <div data-testid="flow-pool-cell" data-pool={cell.hostId} style={{ flex: `${grow} 1 0` }} className={`min-w-[13rem] rounded-md border border-border-default bg-surface-secondary p-2 ${pick.lit.pools.has(cell.hostId) ? LIT_RING : ''}`}>
      <div data-testid="flow-pool-head" {...hoverProps(pick.onHover, { kind: 'pool', id: cell.hostId })} className="mb-1 flex items-center gap-1.5 text-xs">
        <Badge look={look} size={14} />
        <span className="font-medium text-text-primary">{cell.alias}</span>
        <span className="min-w-0 flex-1 truncate text-text-muted">{sub}{cell.readable && cell.away > 0 && t('watch.jobs.flow.away', { n: cell.away })}</span>
      </div>
      {cell.readable && (
        <>
          <ul className="flex flex-col">{cell.rows.map((e) => <QueueRow key={jobKey(e.job.origin, e.job.id)} entry={e} now={now} {...pick} />)}</ul>
          {cell.whys.map((w, i) => (
            <p key={i} data-testid="flow-why" className={`mt-1 text-[11px] ${w.bad ? 'text-status-error-text' : 'text-text-muted'}`}>{t('watch.jobs.flow.why_prefix', { n: w.n, why: whyWord(w.why, t, now) })}</p>
          ))}
        </>
      )}
    </div>
  )
}

function WorkerCard({ job, looks, now, ...pick }: { job: PoolJob; looks: Record<string, PoolHostResolvedLook>; now: number } & Pick) {
  const t = useI18nStore((s) => s.t)
  const preparing = job.state === 'claimed'
  const since = elapsedSince(job)
  return (
    <Press
      jobId={jobKey(job.origin, job.id)} hover={{ kind: 'job', key: jobKey(job.origin, job.id) }} {...pick} attrs={{ 'data-j': jobKey(job.origin, job.id), title: jobLine(job) }}
      className={`flex h-7 w-[200px] shrink-0 items-center gap-1.5 rounded-md px-2 text-left text-xs font-mono ${preparing ? 'border border-dashed border-status-success-text' : 'border border-status-success-text bg-status-success-text/12'}`}
    >
      <Badge look={looks[job.origin]} size={13} />
      <span className="min-w-0 flex-1 truncate text-text-primary">{jobLine(job)}</span>
      {preparing
        ? <span className="shrink-0 font-sans text-[10px] text-text-secondary">{t('watch.jobs.preparing')}</span>
        : <span className="shrink-0 tabular-nums text-text-secondary">{mss(since, now)}</span>}
    </Press>
  )
}

function SlotRowView({ row, looks, now, ...pick }: { row: SlotRow; looks: Record<string, PoolHostResolvedLook>; now: number } & Pick) {
  const t = useI18nStore((s) => s.t)
  const free = Math.max(0, (row.total ?? 0) - row.jobs.length)
  const cls = t(`watch.jobs.flow.class.${row.slot}`)
  return (
    <div data-testid={`flow-slot-${row.slot}`} data-busy={row.busy?.kind}>
      <div className="flex flex-wrap items-center gap-1.5 py-0.5">
        <span className="w-4 shrink-0 text-[10px] text-text-muted">{cls}</span>
        {row.jobs.map((j) => <WorkerCard key={jobKey(j.origin, j.id)} job={j} looks={looks} now={now} {...pick} />)}
        {Array.from({ length: free }).map((_, i) => (
          <div key={`free${i}`} data-testid="flow-free" className="grid h-7 w-[200px] shrink-0 place-items-center rounded-md border border-dashed border-border-default text-[10px] text-text-muted">{t('watch.jobs.flow.free')}</div>
        ))}
        {row.busy !== null && <span data-testid="flow-busy" className="shrink-0 text-[10px] text-text-muted">{t('watch.jobs.flow.busy')}</span>}
      </div>
      {row.busy !== null && free > 0 && <p data-testid="flow-busy-line" className="text-[11px] text-status-warning-text">{t('watch.jobs.flow.busy_line', { cls, reason: busyWord(row.busy, t, now) })}</p>}
    </div>
  )
}

function WorkerCellView({ cell, looks, now, ...pick }: { cell: WorkerCell; looks: Record<string, PoolHostResolvedLook>; now: number } & Pick) {
  const t = useI18nStore((s) => s.t)
  const wkId = cell.hostId || cell.alias
  const s = cell.state
  const place = s === null ? null : s.state === 'ok' ? t(`fleet.zone.${s.facts.derived.zone}`) : livenessWordOf(t, s.state)
  const total = (r: SlotRow) => (r.total === null ? '—' : r.total)
  return (
    <div data-testid="flow-worker-cell" data-wk={wkId} className={`min-w-[13rem] flex-1 rounded-md border border-border-default bg-surface-secondary p-2 ${pick.lit.workers.has(wkId) ? LIT_RING : ''}`}>
      <div data-testid="flow-worker-head" {...hoverProps(pick.onHover, { kind: 'worker', id: wkId })} className="mb-1 flex items-center gap-1.5 text-xs">
        <Badge look={looks[cell.alias]} size={14} />
        <span className="font-medium text-text-primary">{cell.alias === '' ? t('watch.jobs.lane.unknown') : cell.alias}</span>
        {place && <span className="text-text-muted">{place}</span>}
        <span className="ml-auto tabular-nums text-text-muted">{t('watch.jobs.flow.slots', { hu: cell.heavy.jobs.length, ht: total(cell.heavy), lu: cell.light.jobs.length, lt: total(cell.light) })}</span>
      </div>
      <SlotRowView row={cell.heavy} looks={looks} now={now} {...pick} />
      <SlotRowView row={cell.light} looks={looks} now={now} {...pick} />
    </div>
  )
}

export function QueueFlow({ hosts, jobs, entries, now, selected, onSelect }: { hosts: readonly FlowMonitorHost[]; jobs: readonly PoolJob[]; entries: readonly QueueEntry[]; now: number; selected: string | null; onSelect: (key: string | null) => void }) {
  const t = useI18nStore((s) => s.t)
  const [hover, setHover] = useState<Hover | null>(null)
  const flowRef = useRef<HTMLDivElement>(null)
  const looks = useMemo(() => Object.fromEntries(hosts.map((h) => [h.alias, h.look])), [hosts])
  const summary = useMemo(() => flowSummaryOf(entries, jobs), [entries, jobs])
  const strip = useMemo(() => queueStripOf(entries, SUMMARY_SEGMENTS, true), [entries])
  const pools = useMemo(() => poolCellsOf(hosts, entries, jobs), [hosts, entries, jobs])
  const workers = useMemo(() => workerCellsOf(hosts, jobs), [hosts, jobs])
  const poolIdOf = useMemo(() => Object.fromEntries(hosts.map((h) => [h.alias, h.hostId])), [hosts])
  // the jobs being prepared or run and where each is drawn: the routes go from its queue pool cell to its card
  const active = useMemo((): ActiveCard[] => workers.cells.flatMap((c) => [...c.heavy.jobs, ...c.light.jobs].flatMap((j) => (poolIdOf[j.origin] !== undefined ? [{ key: jobKey(j.origin, j.id), pool: poolIdOf[j.origin], worker: c.hostId || c.alias }] : []))), [workers, poolIdOf])
  // what the pointer is on lights routes and nothing else
  const { highlighted, candidate, lit } = useMemo(() => {
    const none = new Set<string>()
    const out = { highlighted: none as ReadonlySet<string>, candidate: null as CandidateSpec | null, lit: { pools: none, workers: none } as Lit }
    if (hover === null) return out
    if (hover.kind === 'job') {
      const a = active.find((x) => x.key === hover.key)
      if (a) { out.highlighted = new Set([a.key]); out.lit = { pools: new Set([a.pool]), workers: new Set([a.worker]) } }
    } else if (hover.kind === 'pool') {
      out.highlighted = new Set(active.filter((x) => x.pool === hover.id).map((x) => x.key))
      out.lit = { pools: new Set([hover.id]), workers: none }
    } else if (hover.kind === 'worker') {
      out.highlighted = new Set(active.filter((x) => x.worker === hover.id).map((x) => x.key))
      out.lit = { pools: none, workers: new Set([hover.id]) }
    } else {
      const e = entries.find((x) => jobKey(x.job.origin, x.job.id) === hover.key)
      const pool = e ? poolIdOf[e.job.origin] : undefined
      if (e && pool !== undefined) { out.candidate = { pool, workers: candidateWorkersOf(e.job, hosts) }; out.lit = { pools: new Set([pool]), workers: none } }
    }
    return out
  }, [hover, active, entries, hosts, poolIdOf])
  const pick: Pick = { selected, onSelect, onHover: setHover, lit }
  const lookByPool = useMemo(() => Object.fromEntries(hosts.map((h) => [h.hostId, h.look])), [hosts])
  const colorOf = (pool: string): string | undefined => resolveHostColors(lookByPool[pool], 'console')?.main
  return (
    <div ref={flowRef} data-testid="queue-flow" className="relative flex flex-col gap-3">
      <div data-testid="flow-summary" className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-text-secondary">
        <span className="tabular-nums">{t('watch.jobs.flow.queued', { n: summary.queued })}</span>
        <span className="tabular-nums text-text-muted">{t('watch.jobs.flow.split', { heavy: summary.heavy, light: summary.light })}</span>
        <span className="tabular-nums">{t('watch.jobs.flow.running', { n: summary.running })}</span>
        <span className="tabular-nums">{t('watch.jobs.flow.preparing', { n: summary.preparing })}</span>
        <span data-testid="flow-longest" className={`tabular-nums ${summary.long ? 'text-status-warning-text' : ''}`}>
          {t('watch.jobs.flow.longest', { time: summary.longestS === null ? '—' : mss(now - summary.longestS * 1000, now) })}
        </span>
        <span className="tabular-nums">{t('watch.jobs.flow.held', { n: summary.held })}</span>
        <span className="relative flex h-1.5 w-32 items-stretch gap-px"><QueueSegments strip={strip} nseg={SUMMARY_SEGMENTS} /></span>
      </div>
      <section aria-label={t('watch.jobs.flow.queue_pool')}>
        <h3 className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-text-secondary"><Stack size={14} aria-hidden="true" />{t('watch.jobs.flow.queue_pool')}<span className="font-normal text-text-muted">{t('watch.jobs.flow.queue_pool_hint')}</span></h3>
        <div data-qtop className="flex flex-wrap gap-2">
          {pools.map((c, i) => <PoolCellView key={c.hostId} cell={c} state={hosts[i].state} look={looks[c.alias]} now={now} {...pick} />)}
        </div>
      </section>
      <section aria-label={t('watch.jobs.flow.worker_pool')}>
        <h3 className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-text-secondary"><HardDrives size={14} aria-hidden="true" />{t('watch.jobs.flow.worker_pool')}<span className="font-normal text-text-muted">{t('watch.jobs.flow.worker_pool_hint')}</span></h3>
        <div data-qbot className="flex flex-wrap gap-2">
          {workers.cells.map((c) => <WorkerCellView key={c.hostId || `w:${c.alias}`} cell={c} looks={looks} now={now} {...pick} />)}
        </div>
        {workers.off.length > 0 && <p data-testid="flow-off" className="mt-1.5 text-[11px] text-text-muted">{t('watch.jobs.flow.off_workers', { names: workers.off.join(t('watch.jobs.flow.name_sep')) })}</p>}
      </section>
      <QueueRoutes flowRef={flowRef} active={active} highlighted={highlighted} candidate={candidate} colorOf={colorOf} />
    </div>
  )
}
