// spa/src/components/watch/WatchEventsPane.tsx — the 事件 tab of the watcher area (watcher area spec §5): the count line, the guardian
// line, three rows of filter chips, and the events newest first with day separators. It only reads: no button here changes an
// event (U3, U9) — the count goes down when the watcher marks them handled.
//
// Tab-hosted checklist: the pane unmounts when another tab is shown (keepAliveCount 0), so the chip filters live in
// `lib/watch/view-memory`, not in component state.
import { useEffect, useMemo, useState } from 'react'
import { ShieldCheck } from '@phosphor-icons/react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostLookResolver, hostLabel } from '../../lib/host-look'
import { useWatchStatusStore, deriveGuardian } from '../../lib/watch/status-store'
import { useWatchEventsStore, pendingCount } from '../../lib/watch/events-store'
import { readWatchRow, modelOf } from '../../lib/watch/watch-row'
import {
  applyFilters, clock, groupByDay, mergeRows, shortDuration,
  KIND_FILTERS, KIND_TONE, KNOWN_KINDS, KNOWN_LABELS, LABEL_TONE, STATUS_FILTERS,
  type EventFilters, type EventRow,
} from '../../lib/watch/events-view'
import { readEventFilters, writeEventFilters } from '../../lib/watch/view-memory'

type T = (k: string, p?: Record<string, string | number>) => string

const CHIP = 'px-2.5 py-0.5 text-xs rounded-full border cursor-pointer transition-colors'
const chipClass = (on: boolean): string => `${CHIP} ${on
  ? 'bg-surface-elevated border-border-active text-text-primary'
  : 'bg-transparent border-border-default text-text-muted hover:text-text-primary hover:border-text-muted'}`

function ChipRow<V extends string>({ testId, label, options, value, onPick }: {
  testId: string; label: string; options: { value: V; label: string }[]; value: V; onPick: (v: V) => void
}) {
  return (
    <div data-testid={testId} role="group" aria-label={label} className="flex flex-wrap items-center gap-1.5">
      <span className="w-10 shrink-0 text-xs text-text-muted">{label}</span>
      {options.map((o) => (
        <button key={o.value} type="button" aria-pressed={o.value === value} onClick={() => onPick(o.value)} className={chipClass(o.value === value)}>
          {o.label}
        </button>
      ))}
    </div>
  )
}

/** The kind's tag text, or the wire name of a kind a newer daemon sent. */
const kindText = (t: T, kind: string, short: boolean): string =>
  KNOWN_KINDS.includes(kind) ? t(`watch.events.kind.${kind}${short ? '_short' : ''}`) : kind

function facts(t: T, e: EventRow['event']): string {
  const parts: string[] = []
  if (e.cwd) parts.push(e.cwd)
  if (e.session) parts.push(e.session)
  if (e.peakCores !== undefined) parts.push(t('watch.events.peak_cores', { n: e.peakCores.toFixed(1) }))
  if (e.peakMb !== undefined) parts.push(t('watch.events.peak_mb', { n: Math.round(e.peakMb) }))
  if (e.durationS !== undefined) parts.push(t('watch.events.duration', { t: shortDuration(e.durationS) }))
  if (e.kind !== 'unmanaged_heavy' && e.detail) parts.push(e.detail)
  if (e.slot) parts.push(t('watch.events.slot', { slot: e.slot }))
  return parts.join('・')
}

function EventItem({ row, hostName, noGuardian }: { row: EventRow; hostName: string; noGuardian: boolean }) {
  const t = useI18nStore((s) => s.t)
  const e = row.event
  const pending = e.status === 'pending'
  const label = e.label
  const line = e.kind === 'pressure_high' ? t('watch.events.pressure_line', { host: hostName }) : e.command
  const factText = facts(t, e)
  return (
    <li data-testid="watch-event" data-event-id={e.id} data-status={e.status} className="border-b border-border-subtle/50 py-2.5">
      <div className="flex items-center gap-2 text-xs">
        <span data-testid="event-time" className="tabular-nums text-text-muted">{clock(e.at)}</span>
        <span data-testid="event-kind" className={`rounded border border-current px-1.5 text-[10px] leading-4 ${KIND_TONE[e.kind] ?? 'text-text-secondary'}`}>
          {kindText(t, e.kind, false)}
        </span>
        {label && (
          <span data-testid="event-label" className={`rounded border border-current px-1.5 text-[10px] leading-4 ${LABEL_TONE[label] ?? 'text-text-secondary'}`}>
            {KNOWN_LABELS.includes(label) ? t(`watch.events.label.${label}`) : label}
          </span>
        )}
        <span data-testid="event-host" className="text-text-secondary">{hostName}</span>
        <span data-testid="event-status" className={`ml-auto ${pending ? 'text-status-warning-text' : 'text-text-muted'}`}>
          {pending ? t(noGuardian ? 'watch.events.status.pending_no_guardian' : 'watch.events.status.pending') : t('watch.events.status.handled')}
        </span>
      </div>
      {line && <div data-testid="event-command" className={`mt-1 text-sm text-text-primary ${e.kind === 'pressure_high' ? '' : 'font-mono'} break-all`}>{line}</div>}
      {factText && <div data-testid="event-facts" className="mt-0.5 text-xs text-text-muted break-all">{factText}</div>}
      {!pending && e.note && (
        <div data-testid="event-note" className="mt-1 flex items-start gap-1.5 text-xs text-text-secondary">
          <ShieldCheck size={13} weight="fill" className="mt-px shrink-0 text-status-success-text" />
          <span className="min-w-0 break-words">{e.note}</span>
        </div>
      )}
    </li>
  )
}

/** The wall clock, moved on every minute: the day separators (今天／昨天) follow midnight without any event arriving. */
function useMinuteClock(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 60_000)
    return () => clearInterval(id)
  }, [])
  return now
}

export function WatchEventsPane(_props: PaneRendererProps) {
  const t = useI18nStore((s) => s.t)
  const byHost = useWatchEventsStore((s) => s.byHost)
  const known = useWatchEventsStore((s) => s.known)
  const unhandled = useWatchEventsStore((s) => s.unhandled)
  const statusByHost = useWatchStatusStore((s) => s.byHost)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const lookOf = useHostLookResolver()
  const nameOf = (id: string): string => hostLabel(id, lookOf(id))

  // Chip choices outlive the pane (view memory); the state only makes the pane redraw.
  const [filters, setFilters] = useState<EventFilters>(readEventFilters)
  const pick = (patch: Partial<EventFilters>): void => {
    const next = { ...filters, ...patch }
    writeEventFilters(next)
    setFilters(next)
  }

  const guardian = useMemo(() => deriveGuardian(statusByHost), [statusByHost])
  const holder = guardian.appHostId
  const [read, setRead] = useState<{ host: string; model?: string } | null>(null)
  useEffect(() => {
    if (!holder) return
    let live = true
    void readWatchRow(holder).then((row) => { if (live) setRead({ host: holder, model: row ? modelOf(row.agentCommand) : undefined }) })
    return () => { live = false }
  }, [holder])
  const model = holder && read?.host === holder ? read.model : undefined

  const pending = pendingCount(unhandled) // the daemons' own counts: a list holds at most 500 rows
  const hasGuardian = guardian.state === 'awake' || guardian.state === 'resting'
  const rows = useMemo(() => mergeRows(byHost, hostOrder), [byHost, hostOrder])

  // A remembered host that is gone from the App means no filter on hosts.
  const sources = useMemo(() => hostOrder.filter((id) => byHost[id]?.kind === 'ok'), [hostOrder, byHost])
  const effective: EventFilters = useMemo(
    () => (filters.host !== 'all' && !sources.includes(filters.host) ? { ...filters, host: 'all' } : filters),
    [filters, sources],
  )
  const shown = useMemo(() => applyFilters(rows, effective), [rows, effective])
  const now = useMinuteClock()
  const groups = useMemo(() => groupByDay(shown, now), [shown, now])

  // A host that carried the watch module and cannot be read now: offline, or it answered with an error.
  const unreadable = known.filter((id) => hosts[id] && !(byHost[id]?.kind === 'ok'))

  const guardianText = hasGuardian
    ? [
        t('watch.events.guardian_on', { host: guardian.reachable && guardian.appHostId ? nameOf(guardian.appHostId) : (guardian.alias ?? guardian.hostId ?? '') }),
        guardian.state === 'awake' ? t('watch.events.guardian_awake') : t('watch.events.guardian_resting'),
        guardian.mode,
        model,
      ].filter(Boolean).join('・')
    : t('watch.events.guardian_none')

  return (
    <div data-testid="watch-events-pane" className="h-full overflow-y-auto p-6">
      <h2 className="text-lg font-semibold text-text-primary">{t('watch.pane.events')}</h2>
      <p data-testid="events-count" className={`mt-1 text-sm ${pending > 0 ? 'text-status-warning-text' : 'text-text-secondary'}`}>
        {pending > 0 ? t('watch.events.count_pending', { n: pending }) : t('watch.events.count_none')}
      </p>
      <p data-testid="events-guardian" className="mt-0.5 flex items-center gap-1.5 text-xs text-text-muted">
        <ShieldCheck size={13} weight={hasGuardian ? 'fill' : 'regular'} className={hasGuardian ? 'text-status-success-text' : ''} />
        <span>{guardianText}</span>
      </p>

      {unreadable.map((id) => (
        <p key={id} data-testid={`events-offline-${id}`} className="mt-3 rounded border border-border-subtle px-3 py-1.5 text-xs text-status-warning-text">
          {t('watch.events.offline', { host: nameOf(id) })}
        </p>
      ))}

      <div className="mt-4 flex flex-col gap-2">
        <ChipRow
          testId="filter-host" label={t('watch.events.filter.host')} value={effective.host}
          options={[{ value: 'all', label: t('watch.events.filter.all') }, ...sources.map((id) => ({ value: id, label: nameOf(id) }))]}
          onPick={(v) => pick({ host: v })}
        />
        <ChipRow
          testId="filter-kind" label={t('watch.events.filter.kind')} value={effective.kind}
          options={KIND_FILTERS.map((k) => ({ value: k, label: k === 'all' ? t('watch.events.filter.all') : kindText(t, k, true) }))}
          onPick={(v) => pick({ kind: v })}
        />
        <ChipRow
          testId="filter-status" label={t('watch.events.filter.status')} value={effective.status}
          options={STATUS_FILTERS.map((s) => ({ value: s, label: t(`watch.events.filter.status_${s}`) }))}
          onPick={(v) => pick({ status: v })}
        />
      </div>

      {groups.length === 0 ? (
        <p data-testid="events-empty" className="mt-8 text-sm text-text-muted">{rows.length === 0 ? t('watch.events.empty') : t('watch.events.empty_filtered')}</p>
      ) : (
        <div className="mt-4">
          {groups.map((g) => (
            <section key={g.day} data-testid="event-day" data-day={g.day}>
              <h3 className="mt-3 mb-1 text-xs font-medium text-text-muted">
                {g.day === 'today' ? t('watch.events.today') : g.day === 'yesterday' ? t('watch.events.yesterday') : g.day}
              </h3>
              <ul>
                {g.rows.map((r) => (
                  <EventItem key={`${r.hostId}\0${r.event.id}`} row={r} hostName={nameOf(r.hostId)} noGuardian={!hasGuardian} />
                ))}
              </ul>
            </section>
          ))}
        </div>
      )}
    </div>
  )
}
