import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useAlertsStore, retainPoolAlerts, alertsEvent, alertsConnectionOpened, alertCountOf, dutyOpenCount, readPoolAlertCount, ALERTS_POLL_MS, type HostAlertsEntry } from './alerts-store'
import { usePoolStore } from './store'
import { useNexHostStore } from '../../stores/useNexHostStore'
import * as api from './alerts-api'
import type { HostAlerts, PoolAlert } from './alerts-api'

vi.mock('./alerts-api', async (orig) => ({ ...(await orig<typeof import('./alerts-api')>()), fetchHostAlerts: vi.fn() }))

const alert = (key: string, over: Partial<PoolAlert> = {}): PoolAlert => ({ key, kind: 'disk_low', level: 'push', since: 1000, last: 2000, clearedAt: null, detail: key, ...over })
const answer = (host: string, alerts: PoolAlert[] = []): HostAlerts => ({ host, known: true, alerts })
const row = (alias: string) => ({ alias, host_id: `id-${alias}`, facts: null, reachable: true }) as never
const setPool = (sourceHostId: string | null, aliases: string[]) => usePoolStore.setState({ hosts: aliases.map(row), sourceHostId } as never)
const setCaps = (id: string, caps: string[]) => useNexHostStore.setState({ byHost: { ...useNexHostStore.getState().byHost, [id]: { daemonCapabilities: caps, fetchedAt: 1 } } } as never)
const fetchMock = () => vi.mocked(api.fetchHostAlerts)
const calls = () => fetchMock().mock.calls.map(([src, alias]) => `${src}:${alias}`)
const flush = async () => { await vi.advanceTimersByTimeAsync(0) }

let release: (() => void) | null = null
beforeEach(() => {
  vi.useFakeTimers()
  fetchMock().mockReset()
  fetchMock().mockImplementation(async (_src, alias) => answer(alias, [alert(`k:${alias}`)]))
  useAlertsStore.getState().reset()
  useNexHostStore.setState({ byHost: {} } as never)
  usePoolStore.setState({ start: vi.fn(), stop: vi.fn(), hosts: [], sourceHostId: null } as never)
})
afterEach(() => { release?.(); release = null; vi.useRealTimers() })

describe('the alerts store — when it reads', () => {
  it('reads every listed host once through the source daemon', async () => {
    setPool('src', ['mlab', 'air26'])
    release = retainPoolAlerts()
    await flush()
    expect(calls().sort()).toEqual(['src:air26', 'src:mlab'])
    expect(useAlertsStore.getState().byHost.mlab).toMatchObject({ kind: 'ok', known: true })
  })
  it('nothing is read while no source answers', async () => {
    release = retainPoolAlerts()
    await flush()
    expect(calls()).toEqual([])
  })
  it('a source that arrives later is read at once; a host that appears later is read, one that goes is dropped', async () => {
    release = retainPoolAlerts()
    act(() => setPool('src', ['mlab']))
    await flush()
    expect(calls()).toEqual(['src:mlab'])
    act(() => setPool('src', ['mlab', 'air26']))
    await flush()
    expect(calls()).toEqual(['src:mlab', 'src:air26'])
    act(() => setPool('src', ['air26']))
    expect(Object.keys(useAlertsStore.getState().byHost)).toEqual(['air26'])
  })
  it('the source\'s alerts.changed re-reads only that host; another daemon\'s event is ignored', async () => {
    setPool('src', ['mlab', 'air26'])
    release = retainPoolAlerts()
    await flush()
    fetchMock().mockClear()
    alertsEvent('src', 'mlab')
    await flush()
    expect(calls()).toEqual(['src:mlab'])
    fetchMock().mockClear()
    alertsEvent('other', 'air26')
    await flush()
    expect(calls()).toEqual([])
  })
  it('an event for a host the pool does not list is not read', async () => {
    setPool('src', ['mlab'])
    release = retainPoolAlerts()
    await flush()
    fetchMock().mockClear()
    alertsEvent('src', 'ghost')
    await flush()
    expect(calls()).toEqual([])
  })
  it('the source\'s event connection opening reads everything once; another daemon\'s does not', async () => {
    setPool('src', ['mlab', 'air26'])
    release = retainPoolAlerts()
    await flush()
    fetchMock().mockClear()
    alertsConnectionOpened('other')
    await flush()
    expect(calls()).toEqual([])
    alertsConnectionOpened('src')
    await flush()
    expect(calls().sort()).toEqual(['src:air26', 'src:mlab'])
  })
  it('a source without pool.alerts.v1 is polled every 30 s', async () => {
    setPool('src', ['mlab'])
    setCaps('src', ['pool.v1'])
    release = retainPoolAlerts()
    await flush()
    fetchMock().mockClear()
    await vi.advanceTimersByTimeAsync(ALERTS_POLL_MS)
    expect(calls()).toEqual(['src:mlab'])
    await vi.advanceTimersByTimeAsync(ALERTS_POLL_MS)
    expect(calls()).toEqual(['src:mlab', 'src:mlab'])
  })
  it('a source with pool.alerts.v1 has no timer', async () => {
    setPool('src', ['mlab'])
    setCaps('src', ['pool.v1', 'pool.alerts.v1'])
    release = retainPoolAlerts()
    await flush()
    fetchMock().mockClear()
    await vi.advanceTimersByTimeAsync(ALERTS_POLL_MS * 3)
    expect(calls()).toEqual([])
  })
  it('the capability arriving later stops the timer; losing it brings it back', async () => {
    setPool('src', ['mlab'])
    setCaps('src', ['pool.v1'])
    release = retainPoolAlerts()
    await flush()
    act(() => setCaps('src', ['pool.v1', 'pool.alerts.v1']))
    fetchMock().mockClear()
    await vi.advanceTimersByTimeAsync(ALERTS_POLL_MS * 2)
    expect(calls()).toEqual([])
    act(() => setCaps('src', ['pool.v1']))
    await vi.advanceTimersByTimeAsync(ALERTS_POLL_MS)
    expect(calls()).toEqual(['src:mlab'])
  })
  it('the last release clears the timer', async () => {
    setPool('src', ['mlab'])
    release = retainPoolAlerts()
    await flush()
    fetchMock().mockClear()
    release()
    release = null
    await vi.advanceTimersByTimeAsync(ALERTS_POLL_MS * 3)
    expect(calls()).toEqual([])
    expect(vi.getTimerCount()).toBe(0)
  })
  it('two holders: the first release keeps it running, the second stops it', async () => {
    setPool('src', ['mlab'])
    const a = retainPoolAlerts()
    const b = retainPoolAlerts()
    await flush()
    a()
    fetchMock().mockClear()
    alertsEvent('src', 'mlab')
    await flush()
    expect(calls()).toEqual(['src:mlab'])
    b()
    fetchMock().mockClear()
    alertsEvent('src', 'mlab')
    await flush()
    expect(calls()).toEqual([])
  })
})

describe('the alerts store — what it keeps', () => {
  it('a new source: the old source\'s late answer is not written', async () => {
    let late!: (v: HostAlerts) => void
    fetchMock().mockImplementationOnce(() => new Promise<HostAlerts>((r) => { late = r }))
    setPool('old', ['mlab'])
    release = retainPoolAlerts()
    await flush()
    act(() => setPool('new', ['mlab']))
    await flush()
    expect(useAlertsStore.getState().byHost.mlab).toMatchObject({ kind: 'ok' })
    late(answer('mlab', [alert('stale')]))
    await flush()
    const e = useAlertsStore.getState().byHost.mlab
    expect(e.kind === 'ok' && e.alerts.map((a) => a.key)).toEqual(['k:mlab'])
  })
  it('two reads of one host overlap: the older one finishing last does not put its list over the newer one', async () => {
    const waiting: Array<(v: HostAlerts) => void> = []
    fetchMock().mockImplementation(() => new Promise<HostAlerts>((r) => { waiting.push(r) }))
    setPool('src', ['mlab'])
    release = retainPoolAlerts()
    await flush()
    alertsEvent('src', 'mlab') // a second read of the same host while the first is out
    await flush()
    expect(waiting).toHaveLength(2)
    waiting[1](answer('mlab', [alert('new')])) // the newer one answers first
    await flush()
    waiting[0](answer('mlab', [alert('old')])) // the older one comes in late
    await flush()
    const e = useAlertsStore.getState().byHost.mlab
    expect(e.kind === 'ok' && e.alerts.map((a) => a.key)).toEqual(['new'])
  })
  it('a read that fails is an error entry with its code', async () => {
    fetchMock().mockRejectedValueOnce(Object.assign(new Error('x'), { poolError: { code: 'wwd_unavailable', action: 'backoff' } }))
    setPool('src', ['mlab'])
    release = retainPoolAlerts()
    await flush()
    expect(useAlertsStore.getState().byHost.mlab).toMatchObject({ kind: 'error', code: 'wwd_unavailable' })
  })
  it('the whole pool unreadable (source null): the last answers stay and nothing is read', async () => {
    setPool('src', ['mlab'])
    setCaps('src', ['pool.v1'])
    release = retainPoolAlerts()
    await flush()
    act(() => setPool(null, []))
    fetchMock().mockClear()
    await vi.advanceTimersByTimeAsync(ALERTS_POLL_MS * 2)
    expect(calls()).toEqual([])
    expect(Object.keys(useAlertsStore.getState().byHost)).toEqual(['mlab'])
  })
})

describe('alertCountOf (M36)', () => {
  const ok = (alerts: PoolAlert[]): HostAlertsEntry => ({ kind: 'ok', known: true, alerts, at: 1 })
  const silence = (alias: string, over: Partial<PoolAlert> = {}) => alert(`silence:${alias}`, { kind: 'silence', silence: 'host_down', ...over })
  it('counts the active push alerts only', () => {
    expect(alertCountOf({ mlab: ok([alert('a'), alert('b', { clearedAt: 5 }), alert('c', { level: 'digest' })]) })).toBe(1)
  })
  it('a level the App does not know is not a push', () => {
    expect(alertCountOf({ mlab: ok([alert('a', { level: 'urgent' })]) })).toBe(0)
  })
  it('the same silence key in several hosts\' lists is one; a cleared one is none', () => {
    expect(alertCountOf({ mlab: ok([silence('mserv')]), air26: ok([silence('mserv')]), air19: ok([silence('mserv', { clearedAt: 9 })]) })).toBe(1)
    expect(alertCountOf({ mlab: ok([silence('mserv')]), air26: ok([silence('air19')]) })).toBe(2)
  })
  it('the same non-silence key on two hosts is two (each belongs to its own host)', () => {
    expect(alertCountOf({ mlab: ok([alert('disk_low')]), air26: ok([alert('disk_low')]) })).toBe(2)
  })
  it('the spec example: both lists have silence:mserv, mlab has a disk_low push and a digest: 2', () => {
    expect(alertCountOf({ mlab: ok([silence('mserv'), alert('disk_low:mlab'), alert('orphans:mlab', { kind: 'orphans', level: 'digest' })]), air26: ok([silence('mserv')]) })).toBe(2)
  })
  it('a host whose read failed counts none; no hosts is 0', () => {
    expect(alertCountOf({ mlab: { kind: 'error', code: 'wwd_unavailable', at: 1 } })).toBe(0)
    expect(alertCountOf({})).toBe(0)
  })
  it('the guardian\'s half is 0 until MON-11 and readPoolAlertCount reads the store', () => {
    expect(dutyOpenCount()).toBe(0)
    useAlertsStore.setState({ byHost: { mlab: ok([alert('a')]) } })
    expect(readPoolAlertCount()).toBe(1)
  })
})

function act(fn: () => void) { fn() }
