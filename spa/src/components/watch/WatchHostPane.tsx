// spa/src/components/watch/WatchHostPane.tsx — one host's tab of the watcher area, the host page (monitoring tabs spec M41 header, M22 banner, M43; plan MON-6f-2). A header (badge, alias, region word or state, wwd line), the sub-tab and
// range switches, a banner under the header when the host is not ok, and the sub-tab's body. It reads the pool only (`useMonitorHosts`): no fleet store, no fleet view, no fleet history.
//
// Tab-hosted checklist: the sub-tab, the range and the scroll of each sub-tab live outside the component (`lib/watch/view-memory`, keyed by the pool host id), because the pane unmounts when another tab is shown.
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'
import { resolveHostBadge } from '../../lib/host-color'
import { HostBadge } from '../HostBadge'
import { HostStateIcon } from '../../components/fleet/HostStateIcon'
import { SegmentControl } from '../settings/SegmentControl'
import { RANGE_MS, type HostRange } from '../../lib/pool/host-history'
import { hostExtraOf } from '../../lib/pool/host-extra'
import { useMonitorHosts, type MonitorHost } from '../../lib/pool/use-monitor-hosts'
import { livenessWord, notOkLines, clock } from '../../lib/pool/words'
import { readHostRange, readHostScroll, readHostTab, writeHostRange, writeHostScroll, writeHostTab, type HostSubTab } from '../../lib/watch/view-memory'
import { AvailRow } from './AvailRow'
import { WatchSettings } from './WatchSettings'
import { MonitorTab } from './host/MonitorTab'

const SUBS: readonly HostSubTab[] = ['monitor', 'status', 'procs', 'settings']
const RANGES = Object.keys(RANGE_MS) as HostRange[]
/** The range switch is for the two tabs that draw time (監控, 狀態). */
const RANGE_SUBS: readonly HostSubTab[] = ['monitor', 'status']

const ZONE_CLASS: Record<string, string> = {
  normal: 'text-status-success-text', warning: 'text-status-warning-text', danger: 'text-status-error-text', unknown: 'text-text-muted',
}
const BANNER_CLASS = {
  wwd_down: 'border-status-warning text-status-warning-text',
  host_down: 'border-status-error text-status-error-text',
  unknown: 'border-border-subtle text-text-muted',
} as const

/** The header's second line: the wwd it runs and the time of its last reading; a host that is not ok says the last reading and the last check. */
function MetaLine({ host }: { host: MonitorHost }) {
  const t = useI18nStore((s) => s.t)
  const st = host.state
  if (st.state === 'ok') {
    const p = st.pool
    return <span className="text-xs text-text-muted" data-testid="measure-source">{t('watch.host.meta_ok', { version: p.version, platform: p.platform, arch: p.arch, time: clock(st.facts.at) })}</span>
  }
  const parts = [st.lastFactsAt !== null ? t('watch.host.meta_last_reading', { time: clock(st.lastFactsAt) }) : t('pool.liveness.no_reading')]
  if (st.checkedAt !== null) parts.push(t('pool.liveness.checked', { time: clock(st.checkedAt) }))
  return <span className="text-xs text-text-muted" data-testid="measure-source">{parts.join('・')}</span>
}

/** M22: under the header on every sub-tab, outside the sub-tab's body. Its colour is the state's; the lines are the state's words (`notOkLines`). */
function Banner({ host, now }: { host: MonitorHost; now: number }) {
  const t = useI18nStore((s) => s.t)
  const st = host.state
  if (st.state === 'ok') return null
  return (
    <div data-testid="host-banner" data-state={st.state} role="status" className={`mb-4 rounded-lg border px-4 py-3 text-sm ${BANNER_CLASS[st.state]}`}>
      <div className="flex items-center gap-2">
        <HostStateIcon state={st.state} size={16} />
        <span data-testid="host-banner-title">{t('watch.host.banner.title', { alias: host.alias, state: livenessWord(t, st) })}</span>
      </div>
      {notOkLines(t, st, now).map((line) => <div key={line} className="text-xs mt-1">{line}</div>)}
      <div className="text-xs mt-1">{t('watch.host.banner.chart_stops')}</div>
    </div>
  )
}

export function WatchHostPane({ pane }: PaneRendererProps) {
  const t = useI18nStore((s) => s.t)
  const poolHostId = pane.content.kind === 'watch-host' ? pane.content.poolHostId : undefined
  const hosts = useMonitorHosts()
  const host = useMemo(() => (poolHostId ? hosts.find((h) => h.hostId === poolHostId) : undefined), [hosts, poolHostId])
  const memoryKey = poolHostId ?? ''
  // The guardian settings are read from the App host that fronts the pool host, whether or not the pool reads the host through another daemon: a disconnected owner is offline for them.
  const ownerId = host?.daemonHostId ?? null
  const ownerDown = useHostStore((s) => ownerId !== null && s.runtime[ownerId]?.status !== 'connected')

  // The choices outlive this component: the memory is the source, the state only re-renders.
  const [sub, setSubState] = useState<HostSubTab>(() => readHostTab(memoryKey))
  const setSub = (s: HostSubTab) => { writeHostTab(memoryKey, s); setSubState(s) }
  const [range, setRangeState] = useState<HostRange>(() => readHostRange(memoryKey))
  const setRange = (r: HostRange) => { writeHostRange(memoryKey, r); setRangeState(r) }

  // The charts end at "now": a 5 s tick (one interval per pane, cleared on unmount) keeps the axis moving between answers.
  const [tick, setTick] = useState(0)
  useEffect(() => {
    const id = setInterval(() => setTick((n) => n + 1), 5000)
    return () => clearInterval(id)
  }, [])
  // eslint-disable-next-line react-hooks/purity, react-hooks/exhaustive-deps
  const now = useMemo(() => Date.now(), [hosts, tick])

  // Scroll: each sub-tab keeps its own place. Restored when the sub-tab (or the host) shows, written as the reader scrolls.
  const scroller = useRef<HTMLDivElement>(null)
  const present = host !== undefined
  useLayoutEffect(() => {
    if (scroller.current) scroller.current.scrollTop = readHostScroll(memoryKey, sub)
  }, [memoryKey, sub, present])

  if (!poolHostId) return <p className="p-6 text-sm text-text-muted">{t('watch.host.no_host')}</p>
  if (!host) return <p className="p-6 text-sm text-text-muted" data-testid="host-not-listed">{t('watch.host.pool_missing')}</p>

  const st = host.state
  const badge = resolveHostBadge(host.look, 'console')
  const opacity = st.state === 'host_down' ? 0.4 : st.state === 'unknown' ? 0.6 : 1
  const zone = st.state === 'ok' ? st.facts.derived.zone : null
  const offline = st.state !== 'ok'
  const peerIds = hosts.filter((h) => h.hostId !== host.hostId).flatMap((h) => (h.daemonHostId ? [h.daemonHostId] : []))

  return (
    <div ref={scroller} className="flex-1 min-h-0 h-full overflow-y-auto" data-testid={`watch-host-${host.hostId}`}
      onScroll={(e) => writeHostScroll(memoryKey, sub, e.currentTarget.scrollTop)}>
      <div className="p-6 max-w-6xl">
        <div className="flex items-center gap-3 mb-4">
          <span style={{ opacity }} className="flex"><HostBadge colors={badge.colors} icon={badge.icon} iconWeight={badge.iconWeight} box={20} inset={3} radius={5} lineColor="host" /></span>
          <h2 className="text-lg font-semibold text-text-primary">{host.alias}</h2>
          {st.state === 'ok'
            ? <span className={`text-sm ${zone ? ZONE_CLASS[zone] : 'text-text-muted'}`} data-testid="zone-text">{zone ? t(`fleet.zone.${zone}`) : t('fleet.no_data')}</span>
            : <span className="flex items-center gap-1 text-sm text-text-muted" data-testid="zone-text"><HostStateIcon state={st.state} size={14} />{livenessWord(t, st)}</span>}
          <MetaLine host={host} />
          <span className="flex-1" />
          {RANGE_SUBS.includes(sub) && (
            <span data-testid="range-switch" role="group" aria-label={t('watch.host.range_label')}>
              <SegmentControl options={RANGES.map((r) => ({ value: r, label: t(`watch.host.range.${r}`) }))} value={range} onChange={setRange} />
            </span>
          )}
          <span data-testid="page-switch" role="group" aria-label={t('watch.host.page_label')}>
            <SegmentControl options={SUBS.map((s) => ({ value: s, label: t(`watch.host.page.${s}`) }))} value={sub} onChange={setSub} />
          </span>
        </div>
        <Banner host={host} now={now} />
        {sub === 'monitor' && <MonitorTab host={host} range={range} now={now} />}
        {sub === 'status' && (<>
          {st.state === 'ok' && <AvailRow pool={hostExtraOf(st, st.pool)} exceptions={st.facts.exceptions} at={st.facts.at} now={now} />}
          <p className="text-xs text-text-muted" data-testid="status-later">{t('watch.host.status_later')}</p>
        </>)}
        {sub === 'procs' && <p className="text-xs text-text-muted" data-testid="procs-later">{t('watch.host.procs_later')}</p>}
        {sub === 'settings' && <WatchSettings hostId={memoryKey} hostConfigId={host.daemonHostId ?? undefined} poolAlias={host.alias} offline={offline || ownerDown} peerIds={peerIds} />}
      </div>
    </div>
  )
}
