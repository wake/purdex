import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within, act } from '@testing-library/react'

const readWatchRow = vi.fn()
vi.mock('../../lib/watch/watch-row', async (orig) => ({ ...(await orig<typeof import('../../lib/watch/watch-row')>()), readWatchRow: (...a: unknown[]) => readWatchRow(...a) }))
import { WatchEventsPane } from './WatchEventsPane'
import type { PaneRendererProps } from '../../lib/module-registry'
import { useWatchEventsStore } from '../../lib/watch/events-store'
import { useWatchStatusStore } from '../../lib/watch/status-store'
import type { WatchStatus } from '../../lib/watch/status-types'
import type { WatchEvent } from '../../lib/watch/events-types'
import { clearViewMemory, readEventFilters } from '../../lib/watch/view-memory'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const NOW = new Date(2026, 9, 17, 12, 0).getTime()
const at = (d: number, h: number, m = 0) => new Date(2026, 9, d, h, m).toISOString()
const ev = (id: string, when: string, over: Partial<WatchEvent> = {}): WatchEvent => ({ id, host: 'mlab', kind: 'unmanaged_heavy', at: when, status: 'pending', ...over })
const status = (state: 'awake' | 'resting' | 'none', mode = 'patrol'): WatchStatus => ({ self: { host_id: 'd1', state, since: '2026-10-16T01:00:00Z', session_id: 's' }, enabled: true, auto_wake: true, mode, targets: [] })
/** `unhandled` defaults to what each listed host's pending rows add up to, as a daemon with a short backlog would say. */
const setEvents = (byHost: Record<string, WatchEvent[] | 'error'>, known?: string[], unhandled?: Record<string, number>) => useWatchEventsStore.setState({
  unhandled: unhandled ?? Object.fromEntries(Object.entries(byHost).flatMap(([id, v]) => (v === 'error' ? [] : [[id, v.filter((e) => e.status === 'pending').length]]))),
  byHost: Object.fromEntries(Object.entries(byHost).map(([id, v]) => [id, v === 'error' ? { kind: 'error' as const, at: 1 } : { kind: 'ok' as const, events: v, at: 1 }])),
  known: known ?? Object.keys(byHost),
})
const asGuardian = (state: 'awake' | 'resting' | 'none') => useWatchStatusStore.setState({ byHost: { h1: { kind: 'ok', status: status(state), at: 1 } } })

beforeEach(() => {
  cleanup(); clearViewMemory()
  vi.useFakeTimers({ toFake: ['Date'] }); vi.setSystemTime(NOW)
  readWatchRow.mockReset(); readWatchRow.mockResolvedValue({ revision: 1, row: {}, agentCommand: 'claude --model opus-5', workdir: '/w' })
  useWatchEventsStore.getState().reset(); useWatchStatusStore.getState().reset()
  useHostStore.setState({
    hosts: { h1: { id: 'h1', name: 'mlab', ip: '1', port: 1, token: 't', order: 0 }, h2: { id: 'h2', name: 'air26', ip: '2', port: 1, token: 't', order: 1 } },
    hostOrder: ['h1', 'h2'], activeHostId: 'h1', runtime: { h1: { status: 'connected' }, h2: { status: 'connected' } },
  } as never)
})
afterEach(() => { vi.useRealTimers(); clearViewMemory() })

const noProps = {} as PaneRendererProps
const mount = () => render(<WatchEventsPane {...noProps} />)

describe('WatchEventsPane — count and guardian lines', () => {
  it('says how many wait (handled ones are not counted), in the warning tone', () => {
    asGuardian('awake')
    setEvents({ h1: [ev('1', at(17, 9)), ev('2', at(17, 8), { status: 'handled' })], h2: [ev('3', at(17, 7))] })
    mount()
    const c = screen.getByTestId('events-count')
    expect(c).toHaveTextContent(t('watch.events.count_pending', { n: 2 }))
    expect(c.className).toContain('text-status-warning-text')
  })
  it("says the daemon's number when its list holds only 500 of them", () => {
    asGuardian('awake')
    setEvents({ h1: Array.from({ length: 500 }, (_, i) => ev(String(i), at(17, 9))) }, undefined, { h1: 620 })
    mount()
    expect(screen.getByTestId('events-count')).toHaveTextContent(t('watch.events.count_pending', { n: 620 }))
  })
  it('says nothing waits when nothing does', () => {
    asGuardian('awake')
    setEvents({ h1: [ev('1', at(17, 9), { status: 'handled', note: 'ok' })] })
    mount()
    expect(screen.getByTestId('events-count')).toHaveTextContent(t('watch.events.count_none'))
  })
  it.each([
    ['awake', 'watch.events.guardian_awake'],
    ['resting', 'watch.events.guardian_resting'],
  ] as const)('the guardian line for %s names the host, the state, the mode and the model', async (state, key) => {
    asGuardian(state)
    setEvents({ h1: [] })
    mount()
    await vi.waitFor(() => expect(screen.getByTestId('events-guardian')).toHaveTextContent('opus-5'))
    const line = screen.getByTestId('events-guardian')
    expect(line).toHaveTextContent(t('watch.events.guardian_on', { host: 'mlab' }))
    expect(line).toHaveTextContent(t(key))
    expect(line).toHaveTextContent('patrol')
  })
  it('without a guardian the line says events will not be handled, and pending rows say so too', () => {
    asGuardian('none')
    setEvents({ h1: [ev('1', at(17, 9))] })
    mount()
    expect(screen.getByTestId('events-guardian')).toHaveTextContent(t('watch.events.guardian_none'))
    expect(screen.getByTestId('event-status')).toHaveTextContent(t('watch.events.status.pending_no_guardian'))
  })
})

describe('WatchEventsPane — rows', () => {
  it('an E1 row: time, kind tag, label tag, host, status, command and facts', () => {
    asGuardian('awake')
    setEvents({ h1: [ev('1', at(17, 9, 5), { label: 'gate_gap', command: 'vitest run', cwd: '/w/p', session: 'sess', peakCores: 6.25, peakMb: 900, durationS: 185 })] })
    mount()
    const row = screen.getByTestId('watch-event')
    expect(within(row).getByTestId('event-time')).toHaveTextContent('09:05')
    expect(within(row).getByTestId('event-kind')).toHaveTextContent(t('watch.events.kind.unmanaged_heavy'))
    expect(within(row).getByTestId('event-label')).toHaveTextContent(t('watch.events.label.gate_gap'))
    expect(within(row).getByTestId('event-label').className).toContain('text-status-error-text')
    expect(within(row).getByTestId('event-host')).toHaveTextContent('mlab')
    expect(within(row).getByTestId('event-status')).toHaveTextContent(t('watch.events.status.pending'))
    expect(within(row).getByTestId('event-command')).toHaveTextContent('vitest run')
    expect(within(row).getByTestId('event-command').className).toContain('font-mono')
    expect(within(row).getByTestId('event-facts')).toHaveTextContent(['/w/p', 'sess', t('watch.events.peak_cores', { n: '6.3' }), t('watch.events.peak_mb', { n: 900 }), t('watch.events.duration', { t: '3m05s' })].join('・'))
  })
  it('the host on a row is the host-look name of the App host, whatever the payload\'s host says, and follows a rename', () => {
    asGuardian('awake')
    setEvents({ h1: [ev('1', at(17, 9), { host: 'mlab' }), ev('2', at(17, 8), { host: 'mlab', kind: 'pressure_high' })] })
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, h1: { ...s.hosts.h1, name: '工作站' } } }))
    mount()
    expect(screen.getAllByTestId('event-host').map((n) => n.textContent)).toEqual(['工作站', '工作站'])
    expect(screen.getByTestId('event-command')).toHaveTextContent(t('watch.events.pressure_line', { host: '工作站' }))
    act(() => { useHostStore.setState((s) => ({ hosts: { ...s.hosts, h1: { ...s.hosts.h1, name: '書房' } } })) })
    expect(screen.getAllByTestId('event-host').map((n) => n.textContent)).toEqual(['書房', '書房'])
  })
  it('an E2 row has the machine-wide sentence, with the App host name, instead of a command', () => {
    asGuardian('awake')
    setEvents({ h1: [ev('1', at(17, 9), { kind: 'pressure_high', host: 'a19', pressure: 2, detail: 'swap 3 GB' })] })
    mount()
    expect(screen.getByTestId('event-command')).toHaveTextContent(t('watch.events.pressure_line', { host: 'mlab' }))
    expect(screen.getByTestId('event-facts')).toHaveTextContent('swap 3 GB')
    expect(screen.getByTestId('event-kind').className).toContain('text-status-error-text')
  })
  it('a handled row shows the watcher\'s note under it with a filled shield', () => {
    asGuardian('awake')
    setEvents({ h1: [ev('1', at(17, 9), { status: 'handled', note: 'killed the stray vitest' })] })
    mount()
    expect(screen.getByTestId('event-status')).toHaveTextContent(t('watch.events.status.handled'))
    expect(screen.getByTestId('event-note')).toHaveTextContent('killed the stray vitest')
  })
  it('a pending row shows no note, and nothing in the pane can change an event', () => {
    asGuardian('awake')
    setEvents({ h1: [ev('1', at(17, 9), { note: 'stale' })] })
    mount()
    expect(screen.queryByTestId('event-note')).toBeNull()
    // every button is a filter chip (a toggle); there is no action button, so nothing can mark or skip an event
    const buttons = screen.getAllByRole('button')
    expect(buttons.length).toBeGreaterThan(0)
    for (const b of buttons) expect(b).toHaveAttribute('aria-pressed')
  })
  it('merges the hosts newest first, with day separators', () => {
    asGuardian('awake')
    setEvents({ h1: [ev('a', at(17, 9)), ev('c', at(10, 9))], h2: [ev('b', at(16, 22), { host: 'air26' })] })
    mount()
    expect(screen.getAllByTestId('watch-event').map((n) => n.getAttribute('data-event-id'))).toEqual(['a', 'b', 'c'])
    const days = screen.getAllByTestId('event-day')
    expect(days.map((d) => d.getAttribute('data-day'))).toEqual(['today', 'yesterday', '2026-10-10'])
    expect(days[0]).toHaveTextContent(t('watch.events.today'))
    expect(days[1]).toHaveTextContent(t('watch.events.yesterday'))
  })
  it('an unknown kind from a newer daemon is drawn with its wire name', () => {
    asGuardian('awake')
    setEvents({ h1: [ev('1', at(17, 9), { kind: 'future_kind' })] })
    mount()
    expect(screen.getByTestId('event-kind')).toHaveTextContent('future_kind')
  })
  it('no events at all say so', () => {
    asGuardian('awake')
    setEvents({ h1: [] })
    mount()
    expect(screen.getByTestId('events-empty')).toHaveTextContent(t('watch.events.empty'))
  })
})

describe('WatchEventsPane — filters', () => {
  const setup = () => {
    asGuardian('awake')
    setEvents({
      h1: [ev('a', at(17, 9), { kind: 'pressure_high' }), ev('b', at(17, 8), { status: 'handled', note: 'n' })],
      h2: [ev('c', at(17, 7), { kind: 'slot_long' })],
    })
    mount()
  }
  const ids = () => screen.queryAllByTestId('watch-event').map((n) => n.getAttribute('data-event-id'))
  const chip = (row: string, name: string) => within(screen.getByTestId(row)).getByRole('button', { name })

  it('the host chips are the hosts that answered, and narrow the list', () => {
    setup()
    fireEvent.click(chip('filter-host', 'air26'))
    expect(ids()).toEqual(['c'])
    expect(chip('filter-host', 'air26')).toHaveAttribute('aria-pressed', 'true')
  })
  it('the kind chips use the short names', () => {
    setup()
    fireEvent.click(chip('filter-kind', t('watch.events.kind.pressure_high_short')))
    expect(ids()).toEqual(['a'])
  })
  it('the status chips split pending from handled; empty results say it is the filter', () => {
    setup()
    fireEvent.click(chip('filter-status', t('watch.events.filter.status_handled')))
    expect(ids()).toEqual(['b'])
    fireEvent.click(chip('filter-kind', t('watch.events.kind.slot_long_short')))
    expect(ids()).toEqual([])
    expect(screen.getByTestId('events-empty')).toHaveTextContent(t('watch.events.empty_filtered'))
  })
  it('keeps the choices in view memory', () => {
    setup()
    fireEvent.click(chip('filter-host', 'mlab'))
    fireEvent.click(chip('filter-status', t('watch.events.filter.status_pending')))
    expect(readEventFilters()).toEqual({ host: 'h1', kind: 'all', status: 'pending' })
  })
  it('a remembered host that no longer answers means no host filter', () => {
    setup()
    fireEvent.click(chip('filter-host', 'air26'))
    cleanup()
    setEvents({ h1: [ev('a', at(17, 9))] }, ['h1', 'h2'])
    mount()
    expect(ids()).toEqual(['a'])
  })
})

describe('WatchEventsPane — hosts that cannot be read', () => {
  it('says so per host above the list: offline, or an unreadable answer; events of the others stay', () => {
    asGuardian('awake')
    setEvents({ h1: [ev('1', at(17, 9))], h2: 'error' }, ['h1', 'h2'])
    mount()
    expect(screen.getByTestId('events-offline-h2')).toHaveTextContent(t('watch.events.offline', { host: 'air26' }))
    expect(screen.queryByTestId('events-offline-h1')).toBeNull()
    expect(screen.getAllByTestId('watch-event')).toHaveLength(1)
  })
  it('a host that went offline (forgotten) but was known gets the note', () => {
    asGuardian('awake')
    setEvents({ h1: [ev('1', at(17, 9))] }, ['h1', 'h2'])
    mount()
    expect(screen.getByTestId('events-offline-h2')).toBeInTheDocument()
  })
  it('a host that never carried the watch module gets none', () => {
    asGuardian('awake')
    setEvents({ h1: [ev('1', at(17, 9))] }, ['h1'])
    mount()
    expect(screen.queryByTestId('events-offline-h2')).toBeNull()
  })
})
