// spa/src/components/watch/WatchEventsPane.tsx — the 事件 tab of the watcher area (monitoring tabs spec M23, M35, M37; plan MON-10c): one list of the pool hosts' alerts (`pool.alerts.v1`) and observations (`pool.observations.v1`), newest first
// with day separators, a line of what is in progress, and three rows of filter chips. It only reads: no button here changes an event (M35); a "more" row is a link to that host's processes tab.
//
// Tab-hosted checklist: the pane unmounts when another tab is shown (keepAliveCount 0), so the chip filters, the scroll and the observations it last read live in `lib/watch/view-memory`, not in component state.
import { useEffect, useMemo, useRef, useState } from 'react'
import { LinkBreak, Power, Question } from '@phosphor-icons/react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { useI18nStore } from '../../stores/useI18nStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { resolveHostColors } from '../../lib/host-color'
import { useAlertsStore } from '../../lib/pool/alerts-store'
import { fetchObservations, type PoolObservation } from '../../lib/pool/observations-api'
import { mergeObservations, alertRowsOf, observationRowsOf, filterEvents, groupByDay, liveCounts, type AlertRow, type EventFilters, type EventRow, type ObservationRow } from '../../lib/pool/events-model'
import { usePoolStore } from '../../lib/pool/store'
import { useMonitorHosts, type MonitorHost } from '../../lib/pool/use-monitor-hosts'
import { errorTextKey } from '../../lib/pool/words'
import { shortDuration } from '../../lib/watch/events-view'
import { openWatchArea } from '../../features/workspace/lib/open-watch-area'
import { HostBadge } from '../HostBadge'
import {
  readEventFilters, writeEventFilters, readEventsScroll, writeEventsScroll, readEventsObservations, writeEventsObservations,
  type EventsObservations,
} from '../../lib/watch/view-memory'

type T = (k: string, p?: Record<string, string | number>) => string

const OBSERVATIONS_CAPABILITY = 'pool.observations.v1'
const OBSERVATIONS_WINDOW_MS = 24 * 60 * 60_000
const OBSERVATIONS_POLL_MS = 60_000
/** A host with more observations than one answer holds is read page by page; this many pages at most, so a flood cannot keep a read busy. */
const OBSERVATIONS_PAGES = 5

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

const pad = (n: number): string => String(n).padStart(2, '0')
const hhmm = (ms: number): string => { const d = new Date(ms); return `${pad(d.getHours())}:${pad(d.getMinutes())}` }

const SILENCE_LOOK = {
  wwd_down: { Icon: LinkBreak, tone: 'text-status-warning-text', bar: 'bg-status-warning-text', key: 'watch.events.silence.wwd_down' },
  host_down: { Icon: Power, tone: 'text-status-error-text', bar: 'bg-status-error-text', key: 'watch.events.silence.host_down' },
  unknown: { Icon: Question, tone: 'text-text-muted', bar: 'bg-text-muted', key: 'watch.events.silence.unknown' },
} as const

const LEVEL_KEYS: Record<string, string> = { push: 'watch.events.level.push', digest: 'watch.events.level.digest', obs: 'watch.events.level.obs', watch: 'watch.events.level.watch' }
/** Kinds the guardian's rules raise that have a name of their own; any other kind stays as the word. */
const ALERT_KINDS = ['pressure_watch', 'pressure_danger', 'swap_growth']

/** What a source daemon's read of one host's observations came to: its list (`truncated`: the pages ran out before the 24 hours did), or the reason there is none. */
type ObsRead = { list: readonly PoolObservation[]; truncated: boolean } | { forbidden: true } | { failed: true }

async function readObservationsOf(source: string, alias: string, since: string): Promise<ObsRead> {
  try {
    const pages: PoolObservation[][] = []
    let until: string | undefined
    let truncated = false
    for (let i = 0; i < OBSERVATIONS_PAGES; i++) {
      const p = await fetchObservations(source, alias, until ? { since, until } : { since })
      pages.push(p.observations)
      truncated = p.known && p.truncated && p.nextUntil !== null
      if (!truncated) break
      until = p.nextUntil ?? undefined
    }
    return { list: mergeObservations(...pages), truncated }
  } catch (e) {
    return (e as { poolError?: { code?: string } })?.poolError?.code === 'forbidden' ? { forbidden: true } : { failed: true }
  }
}

/**
 * What the memory still holds true for `source` (null: nothing is being read) and these hosts: the answers of another source, or of an alias that now names another `host_id`, are dropped, so they are never shown as the current ones.
 * The same memory comes back when nothing differs.
 */
function reconcileObservations(mem: EventsObservations, source: string | null, hosts: readonly { hostId: string; alias: string }[]): EventsObservations {
  const owners = Object.fromEntries(hosts.map((h) => [h.alias, h.hostId]))
  const sameSource = mem.source === source
  const keep = (alias: string): boolean => sameSource && mem.owners[alias] === owners[alias]
  const only = (list: readonly string[]): string[] => list.filter(keep)
  const byHost = Object.fromEntries(Object.entries(mem.byHost).filter(([a]) => keep(a)))
  const same = sameSource && Object.keys(byHost).length === Object.keys(mem.byHost).length && only(mem.failed).length === mem.failed.length
    && only(mem.truncated).length === mem.truncated.length && only(mem.forbidden).length === mem.forbidden.length && Object.keys(owners).every((a) => mem.owners[a] === owners[a])
  if (same) return mem
  return { source, owners, byHost, failed: only(mem.failed), truncated: only(mem.truncated), forbidden: only(mem.forbidden) }
}

/**
 * The observations of every pool host over the last 24 hours, while the tab is shown: on mount and every 60 s, through the pool's source daemon, only when it announces `pool.observations.v1`. The last answers are kept in the view memory,
 * tied to the source and to each alias's `host_id` they were read for. Each host is read and committed on its own, so one that hangs holds back only itself; a host whose read failed keeps its last list but is named as unreadable (`failed`),
 * a 403 is per host (`forbidden`), and one whose pages ran out is marked `truncated`.
 */
function useObservations(hosts: readonly { hostId: string; alias: string }[]): EventsObservations {
  const source = usePoolStore((s) => s.sourceHostId)
  const announces = useNexHostStore((s) => source !== null && s.byHost[source]?.daemonCapabilities?.includes(OBSERVATIONS_CAPABILITY) === true)
  const want = announces ? source : null
  const [state, setState] = useState<EventsObservations>(readEventsObservations)
  const key = hosts.map((h) => `${h.alias}\0${h.hostId}`).join('\u0001')
  const current = useMemo(() => (key === '' ? [] : key.split('\u0001').map((p) => { const [alias, hostId] = p.split('\0'); return { alias, hostId } })), [key])
  // What is shown is the memory as it holds for this source and these hosts, worked out here; the effect below brings the stored memory to the same.
  const shown = useMemo(() => reconcileObservations(state, want, current), [state, want, current])
  useEffect(() => {
    writeEventsObservations(reconcileObservations(readEventsObservations(), want, current))
    if (want === null) return
    let live = true
    // A read that takes longer than the beat must not land over a newer one of the same host: only the newest started read of a host writes.
    const seq = new Map<string, number>()
    const readHost = async (alias: string): Promise<void> => {
      const mine = (seq.get(alias) ?? 0) + 1
      seq.set(alias, mine)
      const r = await readObservationsOf(want, alias, new Date(Date.now() - OBSERVATIONS_WINDOW_MS).toISOString())
      if (!live || seq.get(alias) !== mine) return
      const prev = reconcileObservations(readEventsObservations(), want, current)
      const without = (list: readonly string[]): string[] => list.filter((a) => a !== alias)
      const next: EventsObservations = 'list' in r
        ? { ...prev, forbidden: without(prev.forbidden), byHost: { ...prev.byHost, [alias]: r.list }, failed: without(prev.failed), truncated: r.truncated ? [...without(prev.truncated), alias] : without(prev.truncated) }
        : 'forbidden' in r
          ? { ...prev, forbidden: [...without(prev.forbidden), alias], failed: without(prev.failed) }
          : { ...prev, forbidden: without(prev.forbidden), failed: [...without(prev.failed), alias] }
      writeEventsObservations(next)
      setState(next)
    }
    const readAll = (): void => { for (const h of current) void readHost(h.alias) }
    readAll()
    const id = setInterval(readAll, OBSERVATIONS_POLL_MS)
    return () => { live = false; clearInterval(id) }
  }, [want, current])
  return shown
}

/** The wall clock, moved on every minute: the day separators and the "in progress for" times follow it without any event arriving. */
function useMinuteClock(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 60_000)
    return () => clearInterval(id)
  }, [])
  return now
}

function obsDetail(t: T, o: ObservationRow['obs']): string {
  return [
    `${o.session}（${o.via}）`,
    t('watch.events.obs.cpu_seconds', { n: Math.round(o.cpuSeconds) }),
    t('watch.events.obs.cores', { n: o.cpuCores.toFixed(1) }),
    t('watch.events.obs.mem', { n: Math.round(o.memPeakMb) }),
    t('watch.events.obs.procs', { n: o.procsPeak }),
  ].join('・')
}

function EventItem({ row, host, now }: { row: AlertRow | ObservationRow; host: MonitorHost | undefined; now: number }) {
  const t = useI18nStore((s) => s.t)
  const silence = row.source === 'alert' && row.silence !== undefined ? SILENCE_LOOK[row.silence] : null
  const Icon = silence?.Icon
  const kindWord = row.source === 'obs' ? t('watch.events.obs.kind')
    : silence ? t(silence.key)
    : ALERT_KINDS.includes(row.kind) ? t(`watch.events.alert_kind.${row.kind}`)
    : row.kind
  const kindTone = silence?.tone ?? 'text-text-secondary'
  const levelKey = LEVEL_KEYS[row.level]
  const colors = host ? resolveHostColors({ colors: host.look.colors, color: host.look.color }, 'console') : null
  const endTone = row.live ? (row.level === 'push' ? 'text-status-error-text' : 'text-status-warning-text') : 'text-text-muted'
  const endText = row.live
    ? t('watch.events.live_for', { t: shortDuration((now - row.at) / 1000) })
    : t(row.source === 'alert' && row.silence !== undefined ? 'watch.events.recovered' : 'watch.events.ended', { time: hhmm(row.endAt ?? row.at) })
  return (
    <li
      data-testid="watch-event" data-event-id={row.id} data-source={row.source} data-live={row.live ? 'true' : 'false'} data-silence={row.source === 'alert' ? row.silence : undefined}
      className="relative border-b border-border-subtle/50 py-2.5 pl-3"
    >
      {silence && <span data-testid="event-bar" className={`absolute inset-y-1 left-0 w-0.5 rounded ${silence.bar}`} />}
      <div className="flex items-center gap-2 text-xs">
        <span data-testid="event-time" className="tabular-nums text-text-muted">{hhmm(row.at)}</span>
        {levelKey && <span data-testid="event-level" className="rounded border border-current px-1.5 text-[10px] leading-4 text-text-secondary">{t(levelKey)}</span>}
        <span data-testid="event-kind" className={`flex items-center gap-1 rounded border border-current px-1.5 text-[10px] leading-4 ${kindTone}`}>
          {Icon && <Icon size={11} weight="bold" />}
          {kindWord}
        </span>
        <span data-testid="event-host" className="flex items-center gap-1.5 text-text-secondary">
          <HostBadge colors={colors} icon={host?.look.icon} iconWeight={host?.look.iconWeight} box={16} inset={2} radius={4} lineColor="host" />
          {row.alias}
        </span>
        <span data-testid="event-end" className={`ml-auto ${endTone}`}>{endText}</span>
      </div>
      <div data-testid="event-title" className="mt-1 text-sm text-text-primary break-all">
        {row.source === 'obs' ? t('watch.events.obs.title', { sig: row.title }) : row.title}
      </div>
      {row.source === 'obs' && <div data-testid="event-detail" className="mt-0.5 text-xs text-text-muted break-all">{obsDetail(t, row.obs)}</div>}
    </li>
  )
}

export function WatchEventsPane(_props: PaneRendererProps) {
  const t = useI18nStore((s) => s.t)
  const hosts = useMonitorHosts()
  const alerts = useAlertsStore((s) => s.byHost)
  const observations = useObservations(hosts)
  const now = useMinuteClock()

  // Chip choices outlive the pane (view memory); the state only makes the pane redraw.
  const [filters, setFilters] = useState<EventFilters>(readEventFilters)
  const pick = (patch: Partial<EventFilters>): void => {
    const next = { ...filters, ...patch }
    writeEventFilters(next)
    setFilters(next)
  }
  // A remembered host that is gone from the pool means no filter on hosts.
  const effective: EventFilters = useMemo(
    () => (filters.host !== 'all' && !hosts.some((h) => h.hostId === filters.host) ? { ...filters, host: 'all' } : filters),
    [filters, hosts],
  )

  const eventHosts = useMemo(() => hosts.map((h) => ({ hostId: h.hostId, alias: h.alias })), [hosts])
  const all = useMemo<EventRow[]>(
    () => [...alertRowsOf(alerts, eventHosts), ...observationRowsOf(observations.byHost, eventHosts)],
    [alerts, observations.byHost, eventHosts],
  )
  const counts = useMemo(() => liveCounts(all), [all])
  const shown = useMemo(() => filterEvents(all, effective), [all, effective])
  const groups = useMemo(() => groupByDay(shown, now), [shown, now])
  const hostById = useMemo(() => new Map(hosts.map((h) => [h.hostId, h])), [hosts])

  // The scroll is kept as it moves and put back when the pane comes up again.
  const scroller = useRef<HTMLDivElement>(null)
  useEffect(() => { if (scroller.current) scroller.current.scrollTop = readEventsScroll() }, [])

  const unreadable = hosts.filter((h) => alerts[h.alias]?.kind === 'error')
  const reasonOf = (alias: string): string => {
    const e = alerts[alias]
    const key = e?.kind === 'error' ? errorTextKey(e.code) : null
    return key ? t(key) : e?.kind === 'error' ? e.code : ''
  }

  return (
    <div ref={scroller} onScroll={(e) => writeEventsScroll(e.currentTarget.scrollTop)} data-testid="watch-events-pane" className="h-full overflow-y-auto p-6">
      <h2 className="text-lg font-semibold text-text-primary">{t('watch.pane.events')}</h2>
      <p data-testid="events-live-line" className="mt-1 text-sm text-text-secondary">
        <span data-testid="events-live-push" className={counts.push > 0 ? 'text-status-error-text' : ''}>{t('watch.events.live_push', { n: counts.push })}</span>
        {'・'}
        <span>{t('watch.events.live_other', { n: counts.other })}</span>
      </p>

      {unreadable.map((h) => (
        <p key={h.hostId} data-testid={`events-alerts-unreadable-${h.alias}`} className="mt-3 rounded border border-border-subtle px-3 py-1.5 text-xs text-status-warning-text">
          {t('watch.events.alerts_unreadable', { alias: h.alias, reason: reasonOf(h.alias) })}
        </p>
      ))}
      {observations.failed.filter((a) => hosts.some((h) => h.alias === a)).map((a) => (
        <p key={a} data-testid={`events-obs-unreadable-${a}`} className="mt-3 rounded border border-border-subtle px-3 py-1.5 text-xs text-status-warning-text">
          {t('watch.events.obs_unreadable', { alias: a })}
        </p>
      ))}
      {observations.forbidden.length > 0 && (
        <p data-testid="events-obs-forbidden" className="mt-3 rounded border border-border-subtle px-3 py-1.5 text-xs text-text-muted">{t('watch.events.obs_forbidden')}</p>
      )}

      <div className="mt-4 flex flex-col gap-2">
        <ChipRow
          testId="filter-host" label={t('watch.events.filter.host')} value={effective.host}
          options={[{ value: 'all', label: t('watch.events.filter.all') }, ...hosts.map((h) => ({ value: h.hostId, label: h.alias }))]}
          onPick={(v) => pick({ host: v })}
        />
        <ChipRow
          testId="filter-source" label={t('watch.events.filter.source')} value={effective.source}
          options={[
            { value: 'all', label: t('watch.events.filter.all') },
            { value: 'alert', label: t('watch.events.filter.source_alert') },
            { value: 'obs', label: t('watch.events.filter.source_obs') },
          ]}
          onPick={(v) => pick({ source: v })}
        />
        <ChipRow
          testId="filter-status" label={t('watch.events.filter.status')} value={effective.status}
          options={[
            { value: 'all', label: t('watch.events.filter.status_all') },
            { value: 'live', label: t('watch.events.filter.status_live') },
            { value: 'done', label: t('watch.events.filter.status_done') },
          ]}
          onPick={(v) => pick({ status: v })}
        />
      </div>

      {groups.length === 0 ? (
        <p data-testid="events-empty" className="mt-8 text-sm text-text-muted">{all.length === 0 ? t('watch.events.empty') : t('watch.events.empty_filtered')}</p>
      ) : (
        <div className="mt-4">
          {groups.map((g) => (
            <section key={g.date} data-testid="event-day" data-day={g.label}>
              <h3 className="mt-3 mb-1 text-xs font-medium text-text-muted">
                {g.label === 'today' ? t('watch.events.today_date', { date: g.date }) : g.label === 'yesterday' ? t('watch.events.yesterday_date', { date: g.date }) : g.date}
              </h3>
              <ul>
                {g.rows.map((r) => (r.more === true
                  ? (
                    <li key={r.id} data-testid={`event-more-${r.hostId}`} className="py-2 text-xs text-text-muted">
                      <button type="button" onClick={() => openWatchArea({ poolHost: { hostId: r.hostId }, sub: 'procs' })} className="cursor-pointer text-left text-accent hover:underline">
                        {t(observations.truncated.includes(r.alias) ? 'watch.events.more_at_least' : 'watch.events.more', { alias: r.alias, n: r.n })}
                      </button>
                    </li>
                  )
                  : <EventItem key={r.id} row={r} host={hostById.get(r.hostId)} now={now} />))}
              </ul>
            </section>
          ))}
        </div>
      )}
    </div>
  )
}
