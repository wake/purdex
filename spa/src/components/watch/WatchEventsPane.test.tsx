// The 事件 tab (monitoring tabs spec M23, M35, M37; plan MON-10c): the pool hosts' alerts and observations as one list, three chip filters, no button that changes an event.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup, within, act } from '@testing-library/react'
import { WatchEventsPane } from './WatchEventsPane'
import { useAlertsStore, alertCountOf, type HostAlertsEntry } from '../../lib/pool/alerts-store'
import type { PoolAlert } from '../../lib/pool/alerts-api'
import type { PoolObservation } from '../../lib/pool/observations-api'
import { usePoolStore } from '../../lib/pool/store'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { clearViewMemory, readEventFilters, writeEventFilters } from '../../lib/watch/view-memory'
import { PoolReadError } from '../../lib/pool/api'
import { classify } from '../../lib/pool/errors'
import type { MonitorHost } from '../../lib/pool/use-monitor-hosts'

const fetchObservations = vi.hoisted(() => vi.fn())
const openWatchArea = vi.hoisted(() => vi.fn())
const monitorHosts = vi.hoisted(() => ({ list: [] as unknown[] }))
vi.mock('../../lib/pool/observations-api', async (orig) => ({ ...((await orig()) as Record<string, unknown>), fetchObservations }))
vi.mock('../../features/workspace/lib/open-watch-area', () => ({ openWatchArea }))
vi.mock('../../lib/pool/use-monitor-hosts', () => ({ useMonitorHosts: () => monitorHosts.list }))

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const NOW = new Date(2026, 9, 21, 12, 0, 0).getTime()
const MIN = 60_000
const hhmm = (ms: number) => { const d = new Date(ms); return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}` }

const host = (hostId: string, alias: string): MonitorHost => ({ hostId, alias, daemonHostId: null, look: { indicator: 'ring' } as never, state: {} as never, poolFailure: null, count: null, local: false })
const alert = (p: Partial<PoolAlert> & { key: string }): PoolAlert => ({ kind: 'orphans', level: 'digest', since: NOW - 60 * MIN, last: NOW - MIN, clearedAt: null, detail: '', ...p })
const ok = (alerts: PoolAlert[]): HostAlertsEntry => ({ kind: 'ok', known: true, alerts, at: NOW })
const obs = (id: string, over: Partial<PoolObservation> = {}): PoolObservation => ({
  id, host: 'mserv', session: 'purdex-1f', via: 'session', command: 'cc1plus', signature: `sig-${id}`, startedAt: NOW - 30 * MIN, endedAt: NOW - 20 * MIN, cpuSeconds: 120, cpuPeak: 450, memPeakMb: 2048, procsPeak: 7, ...over,
})
const page = (observations: PoolObservation[]) => ({ host: 'mserv', known: true, observations, truncated: false, nextUntil: null })
const rows = () => screen.queryAllByTestId('watch-event')
const flush = async () => { await act(async () => { await vi.advanceTimersByTimeAsync(0) }) }
const pane = () => render(<WatchEventsPane {...({} as never)} />)

beforeEach(() => {
  cleanup(); clearViewMemory(); fetchObservations.mockReset(); openWatchArea.mockReset()
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'setTimeout', 'clearTimeout', 'Date'] })
  vi.setSystemTime(NOW)
  monitorHosts.list = [host('h-mlab', 'mlab'), host('h-mserv', 'mserv')]
  usePoolStore.setState({ sourceHostId: 'src', hosts: [] } as never)
  useNexHostStore.setState({ byHost: { src: { phase: 'ready', daemonCapabilities: ['pool.alerts.v1', 'pool.observations.v1'] } } } as never)
  useAlertsStore.setState({ byHost: {}, source: null })
  fetchObservations.mockResolvedValue(page([]))
})
afterEach(() => { vi.useRealTimers(); clearViewMemory() })

describe('the events tab (MON-10c)', () => {
  it('draws the alerts and the observations as rows, newest first, with the page title and no guardian line', async () => {
    useAlertsStore.setState({ byHost: { mlab: ok([alert({ key: 'orphans:mlab', detail: '3 個孤兒行程', since: NOW - 90 * MIN })]) } })
    fetchObservations.mockImplementation(async (_s: string, alias: string) => page(alias === 'mserv' ? [obs('o1')] : []))
    pane()
    await flush()
    expect(screen.getByRole('heading', { name: t('watch.pane.events') })).toBeInTheDocument()
    expect(rows().map((r) => r.getAttribute('data-source'))).toEqual(['obs', 'alert']) // the observation started 30 min ago, the alert 90
    expect(screen.queryByTestId('events-guardian')).toBeNull()
    expect(within(rows()[1]).getByTestId('event-title')).toHaveTextContent('3 個孤兒行程')
    expect(within(rows()[0]).getByTestId('event-title')).toHaveTextContent('sig-o1')
  })

  it('counts what is in progress in the line at the top, push level in red', async () => {
    useAlertsStore.setState({ byHost: { mlab: ok([alert({ key: 'a', level: 'push' }), alert({ key: 'b' }), alert({ key: 'c', clearedAt: NOW - 5 * MIN })]) } })
    pane()
    await flush()
    expect(screen.getByTestId('events-live-line')).toHaveTextContent(`${t('watch.events.live_push', { n: 1 })}・${t('watch.events.live_other', { n: 1 })}`)
    expect(screen.getByTestId('events-live-push')).toHaveClass('text-status-error-text')
  })

  it('draws a silence with its icon, colour, the 2 px bar and the recovery time', async () => {
    const cleared = NOW - 10 * MIN
    useAlertsStore.setState({ byHost: { mlab: ok([
      alert({ key: 'silence:mserv', kind: 'silence', silence: 'host_down', level: 'push', since: NOW - 60 * MIN, clearedAt: cleared, detail: 'mserv 停了' }),
      alert({ key: 'silence:mlab', kind: 'silence', silence: 'wwd_down', level: 'push', since: NOW - 30 * MIN, detail: 'wwd 停了' }),
    ]) } })
    pane()
    await flush()
    const done = rows().find((r) => r.getAttribute('data-silence') === 'host_down')!
    expect(within(done).getByTestId('event-bar')).toHaveClass('w-0.5')
    expect(within(done).getByTestId('event-kind')).toHaveClass('text-status-error-text')
    expect(within(done).getByTestId('event-end')).toHaveTextContent(t('watch.events.recovered', { time: hhmm(cleared) }))
    const live = rows().find((r) => r.getAttribute('data-silence') === 'wwd_down')!
    expect(within(live).getByTestId('event-kind')).toHaveClass('text-status-warning-text')
    expect(within(live).getByTestId('event-end')).toHaveTextContent(t('watch.events.live_for', { t: '30m00s' }))
    expect(within(live).getByTestId('event-end')).toHaveClass('text-status-error-text') // push level in progress
  })

  it('an ordinary alert has no bar and says "ended" when it is over', async () => {
    const cleared = NOW - 10 * MIN
    useAlertsStore.setState({ byHost: { mlab: ok([alert({ key: 'orphans:mlab', clearedAt: cleared })]) } })
    pane()
    await flush()
    expect(screen.queryByTestId('event-bar')).toBeNull()
    expect(screen.getByTestId('event-end')).toHaveTextContent(t('watch.events.ended', { time: hhmm(cleared) }))
  })

  it('a watch-level alert says 守門員, counts as "other" in the top line and has the kind\'s name (88 10-21)', async () => {
    useAlertsStore.setState({ byHost: { mlab: ok([
      alert({ key: 'p1', kind: 'pressure_watch', level: 'watch' }),
      alert({ key: 'p2', kind: 'swap_growth', level: 'watch' }),
      alert({ key: 'p3', kind: 'pressure_danger', level: 'push' }),
      alert({ key: 'p4', kind: 'brand_new_kind', level: 'digest' }),
    ]) } })
    pane()
    await flush()
    const byKey = (k: string) => rows().find((r) => r.getAttribute('data-event-id')!.endsWith(`|${k}|${NOW - 60 * MIN}`))!
    expect(within(byKey('p1')).getByTestId('event-level')).toHaveTextContent(t('watch.events.level.watch'))
    expect(within(byKey('p1')).getByTestId('event-kind')).toHaveTextContent(t('watch.events.alert_kind.pressure_watch'))
    expect(within(byKey('p2')).getByTestId('event-kind')).toHaveTextContent(t('watch.events.alert_kind.swap_growth'))
    expect(within(byKey('p3')).getByTestId('event-kind')).toHaveTextContent(t('watch.events.alert_kind.pressure_danger'))
    expect(within(byKey('p4')).getByTestId('event-kind')).toHaveTextContent('brand_new_kind') // a kind this App does not know stays as the word
    expect(screen.getByTestId('events-live-line')).toHaveTextContent(`${t('watch.events.live_push', { n: 1 })}・${t('watch.events.live_other', { n: 3 })}`)
    expect(alertCountOf(useAlertsStore.getState().byHost)).toBe(1) // the shield counts push level only
  })

  it('has no button in a row: nothing here changes an event (M35)', async () => {
    useAlertsStore.setState({ byHost: { mlab: ok([alert({ key: 'a', level: 'push' })]) } })
    fetchObservations.mockImplementation(async (_s: string, alias: string) => page(alias === 'mserv' ? [obs('o1', { endedAt: null })] : []))
    pane()
    await flush()
    expect(rows()).toHaveLength(2)
    for (const r of rows()) expect(within(r).queryAllByRole('button')).toHaveLength(0)
  })

  it('filters by host, by source and by state', async () => {
    useAlertsStore.setState({ byHost: { mlab: ok([alert({ key: 'a' }), alert({ key: 'b', clearedAt: NOW - MIN })]) } })
    fetchObservations.mockImplementation(async (_s: string, alias: string) => page(alias === 'mserv' ? [obs('o1', { endedAt: null })] : []))
    pane()
    await flush()
    expect(rows()).toHaveLength(3)
    fireEvent.click(within(screen.getByTestId('filter-host')).getByRole('button', { name: 'mserv' }))
    expect(rows().map((r) => r.getAttribute('data-source'))).toEqual(['obs'])
    fireEvent.click(within(screen.getByTestId('filter-host')).getByRole('button', { name: t('watch.events.filter.all') }))
    fireEvent.click(within(screen.getByTestId('filter-source')).getByRole('button', { name: t('watch.events.filter.source_alert') }))
    expect(rows().map((r) => r.getAttribute('data-source'))).toEqual(['alert', 'alert'])
    fireEvent.click(within(screen.getByTestId('filter-status')).getByRole('button', { name: t('watch.events.filter.status_live') }))
    expect(rows()).toHaveLength(1)
    expect(rows()[0]).toHaveAttribute('data-live', 'true')
  })

  it('a remembered host that left the pool means no filter on hosts', async () => {
    writeEventFilters({ host: 'h-gone', source: 'all', status: 'all' })
    useAlertsStore.setState({ byHost: { mlab: ok([alert({ key: 'a' })]) } })
    pane()
    await flush()
    expect(rows()).toHaveLength(1)
    expect(within(screen.getByTestId('filter-host')).getByRole('button', { name: t('watch.events.filter.all') })).toHaveAttribute('aria-pressed', 'true')
  })

  it('writes a picked filter into the view memory', async () => {
    pane()
    await flush()
    fireEvent.click(within(screen.getByTestId('filter-source')).getByRole('button', { name: t('watch.events.filter.source_obs') }))
    expect(readEventFilters().source).toBe('obs')
  })

  it('says it cannot read a host\'s alerts, with the reason', async () => {
    useAlertsStore.setState({ byHost: { mserv: { kind: 'error', code: 'no_answer', at: NOW } } })
    pane()
    await flush()
    expect(screen.getByTestId('events-alerts-unreadable-mserv')).toHaveTextContent('mserv')
  })

  it('a 403 on the observations is one line, once, however many hosts answer it', async () => {
    fetchObservations.mockRejectedValue(new PoolReadError('/x', 403, classify(403, null)))
    pane()
    await flush()
    expect(screen.getAllByTestId('events-obs-forbidden')).toHaveLength(1)
    expect(screen.getByTestId('events-obs-forbidden')).toHaveTextContent(t('watch.events.obs_forbidden'))
  })

  it('reads the observations of every host since 24 hours ago, again every 60 s, and stops when the tab is gone', async () => {
    const { unmount } = pane()
    await flush()
    expect(fetchObservations).toHaveBeenCalledTimes(2)
    expect(fetchObservations).toHaveBeenCalledWith('src', 'mlab', { since: new Date(NOW - 24 * 60 * MIN).toISOString() })
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(fetchObservations).toHaveBeenCalledTimes(4)
    unmount()
    await act(async () => { await vi.advanceTimersByTimeAsync(180_000) })
    expect(fetchObservations).toHaveBeenCalledTimes(4)
  })

  it('an older read that finishes after a newer one does not put its answer over it', async () => {
    let releaseOld: (v: unknown) => void = () => {}
    fetchObservations.mockImplementationOnce(() => new Promise((r) => { releaseOld = r })) // mlab, first beat: slow
    fetchObservations.mockImplementationOnce(async () => page([])) // mserv, first beat
    pane()
    await flush()
    fetchObservations.mockImplementation(async (_s: string, alias: string) => page(alias === 'mserv' ? [obs('new')] : []))
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(rows().map((r) => r.getAttribute('data-event-id'))).toEqual(['h-mserv|new'])
    await act(async () => { releaseOld(page([obs('stale')])); await vi.advanceTimersByTimeAsync(0) })
    expect(rows().map((r) => r.getAttribute('data-event-id'))).toEqual(['h-mserv|new'])
  })

  it('a host whose read never answers does not hold back the others', async () => {
    fetchObservations.mockImplementation((_s: string, alias: string) => (alias === 'mlab' ? new Promise(() => {}) : Promise.resolve(page([obs('fine')]))))
    pane()
    await flush()
    expect(rows().map((r) => r.getAttribute('data-event-id'))).toEqual(['h-mserv|fine'])
  })

  it('names a host whose observations could not be read, keeping the list it had', async () => {
    fetchObservations.mockImplementation(async (_s: string, alias: string) => page(alias === 'mserv' ? [obs('o1')] : []))
    pane()
    await flush()
    expect(screen.queryByTestId('events-obs-unreadable-mserv')).toBeNull()
    fetchObservations.mockRejectedValue(new Error('boom'))
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(screen.getByTestId('events-obs-unreadable-mserv')).toHaveTextContent(t('watch.events.obs_unreadable', { alias: 'mserv' }))
    expect(rows()).toHaveLength(1)
  })

  it('says "at least" when the pages ran out before the 24 hours did', async () => {
    fetchObservations.mockImplementation(async (_s: string, alias: string, q: { until?: string }) => ({
      host: alias, known: true, truncated: true, nextUntil: `u${(q.until ?? 'u0').slice(1) + '1'}`,
      observations: alias === 'mserv' ? [1, 2, 3, 4].map((n) => obs(`${q.until ?? 'u0'}-${n}`, { startedAt: NOW - n * MIN })) : [],
    }))
    pane()
    await flush()
    expect(screen.getByTestId('event-more-h-mserv')).toHaveTextContent(t('watch.events.more_at_least', { alias: 'mserv', n: 17 }))
  })

  it('does not read the observations from a source that does not announce pool.observations.v1', async () => {
    useNexHostStore.setState({ byHost: { src: { phase: 'ready', daemonCapabilities: ['pool.alerts.v1'] } } } as never)
    pane()
    await flush()
    await act(async () => { await vi.advanceTimersByTimeAsync(120_000) })
    expect(fetchObservations).not.toHaveBeenCalled()
  })

  it('keeps only the newest three observations of a host and links the rest to its 行程 tab', async () => {
    fetchObservations.mockImplementation(async (_s: string, alias: string) => page(alias === 'mserv' ? [1, 2, 3, 4, 5].map((n) => obs(`o${n}`, { startedAt: NOW - n * MIN, endedAt: NOW - n * MIN + 1000 })) : []))
    pane()
    await flush()
    expect(rows()).toHaveLength(3)
    const more = screen.getByTestId('event-more-h-mserv')
    expect(more).toHaveTextContent(t('watch.events.more', { alias: 'mserv', n: 2 }))
    fireEvent.click(within(more).getByRole('button'))
    expect(openWatchArea).toHaveBeenCalledWith({ poolHost: { hostId: 'h-mserv' }, sub: 'procs' })
  })

  it('the "more" row goes away under the wwd-alerts filter', async () => {
    fetchObservations.mockImplementation(async (_s: string, alias: string) => page(alias === 'mserv' ? [1, 2, 3, 4].map((n) => obs(`o${n}`, { startedAt: NOW - n * MIN })) : []))
    pane()
    await flush()
    expect(screen.getByTestId('event-more-h-mserv')).toBeInTheDocument()
    fireEvent.click(within(screen.getByTestId('filter-source')).getByRole('button', { name: t('watch.events.filter.source_alert') }))
    expect(screen.queryByTestId('event-more-h-mserv')).toBeNull()
  })

  it('separates days: today first, an older day by its date', async () => {
    useAlertsStore.setState({ byHost: { mlab: ok([alert({ key: 'a' }), alert({ key: 'b', since: NOW - 3 * 24 * 60 * MIN, clearedAt: NOW - 3 * 24 * 60 * MIN + MIN })]) } })
    pane()
    await flush()
    const days = screen.getAllByTestId('event-day')
    expect(days).toHaveLength(2)
    expect(days[0]).toHaveTextContent(t('watch.events.today'))
  })
})
