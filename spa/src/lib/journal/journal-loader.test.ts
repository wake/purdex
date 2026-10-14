// spa/src/lib/journal/journal-loader.test.ts — the active tab's unit is loaded once per generation, a remote member follows its
// lead, journal.changed re-reads only the active unit (J-2a T-2a.6 / T-2a.4).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const getJournal = vi.fn()
const getTasks = vi.fn()
const getRoadmap = vi.fn()
vi.mock('./api', () => ({
  getJournal: (...a: unknown[]) => getJournal(...a),
  getTasks: (...a: unknown[]) => getTasks(...a),
  getRoadmap: (...a: unknown[]) => getRoadmap(...a),
}))

import { STALE_DEBOUNCE_MS, startJournalLoader } from './journal-loader'
import { handleJournalEvent } from './journal-ws'
import { parseJournalPage } from './parse'
import { clearViewMemos } from './view-memory'
import { wirePage } from './fixtures'
import { liveSupport, selectUnit, useJournalStore } from '../../stores/useJournalStore'
import { useHostStore } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import type { PaneContent, PaneLayout, Tab } from '../../types/tab'

const cc = (hostId: string, sessionId: string): PaneLayout => ({
  type: 'leaf',
  pane: { id: `p-${sessionId}`, content: { kind: 'tmux-session', hostId, sessionCode: 'abc', mode: 'terminal', cachedName: 'n', tmuxInstance: 'i', rebuild: { sessionName: 'n', tmuxInstance: 'i', agent: { type: 'cc', sessionId, updatedAt: 1 }, capturedAt: 1 } } as PaneContent },
}) as PaneLayout
const tab = (id: string, layout: PaneLayout) => ({ id, layout }) as unknown as Tab
const activate = (id: string) => useTabStore.setState({ activeTabId: id } as never)
const host = (id: string, ip: string, daemonId: string) => ({ id, name: id, ip, port: 7860, order: 0, daemonId })
const verified = (ip: string, daemonId: string) => ({ status: 'connected', daemonIdVerified: { endpoint: `${ip}:7860`, daemonId } })

const okPage = (unitId: string, over: Record<string, unknown> = {}) => ({ kind: 'ok', value: parseJournalPage(wirePage({ unit: { kind: 'team', id: unitId, label: unitId }, ...over })) })
const flush = async () => { await vi.advanceTimersByTimeAsync(0) }
const store = () => useJournalStore.getState()
const changed = (hostId: string, unitId: string, what: string) => handleJournalEvent(hostId, 'journal.changed', { unit: { kind: 'team', id: unitId }, project: 'p', what })

let stop: () => void = () => {}

beforeEach(() => {
  vi.useFakeTimers()
  useJournalStore.getState().reset()
  clearViewMemos()
  getJournal.mockReset(); getTasks.mockReset(); getRoadmap.mockReset()
  useHostStore.setState({
    hosts: { h1: host('h1', '10.0.0.1', 'dm-a'), h2: host('h2', '10.0.0.2', 'dm-b') }, hostOrder: ['h1', 'h2'],
    runtime: { h1: verified('10.0.0.1', 'dm-a'), h2: verified('10.0.0.2', 'dm-b') },
  } as never)
  useTabStore.setState({ tabs: { A: tab('A', cc('h1', 'sA')), B: tab('B', cc('h1', 'sB')), M: tab('M', cc('h1', 'mem')) }, activeTabId: 'A' } as never)
  store().setSupport('h1', true)
  store().setSupport('h2', true)
  getJournal.mockImplementation(async (_h: string, sess: string) => okPage(`unit-${sess}`))
})
afterEach(() => { stop(); vi.useRealTimers() })

describe('loading the active tab\'s unit', () => {
  it('loads the active tab\'s session once; switching away and back asks nothing again', async () => {
    stop = startJournalLoader()
    await flush()
    expect(getJournal).toHaveBeenCalledTimes(1)
    expect(getJournal).toHaveBeenLastCalledWith('h1', 'sA', undefined)
    activate('B'); await flush()
    expect(getJournal).toHaveBeenCalledTimes(2)
    activate('A'); await flush()
    activate('B'); await flush()
    expect(getJournal).toHaveBeenCalledTimes(2)
    expect(selectUnit(store(), 'h1', 'sA')?.unit.id).toBe('unit-sA')
    expect(selectUnit(store(), 'h1', 'sB')?.unit.id).toBe('unit-sB')
  })

  it('loads whether or not any card is open (nothing in the view state is consulted) and ignores tabs with no cc session', async () => {
    useTabStore.setState({ tabs: { A: tab('A', { type: 'leaf', pane: { id: 'x', content: { kind: 'dashboard' } } } as PaneLayout) }, activeTabId: 'A' } as never)
    stop = startJournalLoader()
    await flush()
    expect(getJournal).not.toHaveBeenCalled()
  })

  it('a member\'s tab and its lead\'s tab ask with their own sessions and share the team unit the daemon names', async () => {
    getJournal.mockImplementation(async () => okPage('team-1'))
    stop = startJournalLoader()
    await flush()
    activate('M'); await flush()
    expect(getJournal.mock.calls.map((c) => c[1])).toEqual(['sA', 'mem'])
    expect(selectUnit(store(), 'h1', 'sA')).toBe(selectUnit(store(), 'h1', 'mem'))
  })

  it('asks nothing while support is unknown; a disconnect keeps the held unit; the reconnect generation loads once', async () => {
    store().fence('h1')
    stop = startJournalLoader()
    await flush()
    expect(getJournal).not.toHaveBeenCalled()
    expect(liveSupport('h1')).toBeUndefined()
    store().setSupport('h1', true); await flush()
    expect(getJournal).toHaveBeenCalledTimes(1)
    store().fence('h1') // disconnect
    await flush()
    expect(selectUnit(store(), 'h1', 'sA')).toBeDefined()
    expect(getJournal).toHaveBeenCalledTimes(1)
    store().fence('h1'); store().setSupport('h1', true); await flush() // reconnect + probe answer
    expect(getJournal).toHaveBeenCalledTimes(2)
    useHostStore.setState({ runtime: { ...useHostStore.getState().runtime } }); await flush() // unrelated churn
    expect(getJournal).toHaveBeenCalledTimes(2)
  })

  it('a failed or empty answer is not a request loop: host churn does not retry, a tab switch does', async () => {
    getJournal.mockRejectedValue(new Error('500'))
    stop = startJournalLoader()
    await flush()
    for (let i = 0; i < 3; i++) { useHostStore.setState({ runtime: { ...useHostStore.getState().runtime } }); await flush() }
    expect(getJournal).toHaveBeenCalledTimes(1)
    getJournal.mockImplementation(async () => ({ kind: 'none' }))
    activate('B'); await flush()
    activate('A'); await flush()
    expect(getJournal.mock.calls.map((c) => c[1])).toEqual(['sA', 'sB', 'sA'])
    expect(store().routes[`h1\u0000sA`]).toEqual({ kind: 'none' })
  })
})

describe('a remote member follows its lead', () => {
  const remote = { kind: 'remote', leadHostId: 'dm-b', leadSessionId: 'lead-s' }

  it('asks the lead\'s host with the lead\'s session, and the unit lives under that host', async () => {
    getJournal.mockImplementation(async (h: string) => (h === 'h1' ? remote : okPage('team-1')))
    stop = startJournalLoader()
    await flush()
    expect(getJournal.mock.calls.map((c) => [c[0], c[1]])).toEqual([['h1', 'sA'], ['h2', 'lead-s']])
    expect(store().routes[`h1\u0000sA`]).toMatchObject({ kind: 'follow', hostId: 'h2', sessionId: 'lead-s' })
    expect(selectUnit(store(), 'h2', 'lead-s')?.unit.id).toBe('team-1')
    expect(selectUnit(store(), 'h1', 'sA')).toBeUndefined()
  })

  it('a lead host this App does not have is unreachable; once it is configured and verified the loader follows it', async () => {
    useHostStore.setState({ hosts: { h1: host('h1', '10.0.0.1', 'dm-a') }, runtime: { h1: verified('10.0.0.1', 'dm-a') } } as never)
    getJournal.mockImplementation(async (h: string) => (h === 'h1' ? remote : okPage('team-1')))
    stop = startJournalLoader()
    await flush()
    expect(store().routes[`h1\u0000sA`]).toEqual({ kind: 'unreachable', leadHostId: 'dm-b', leadSessionId: 'lead-s' })
    expect(getJournal).toHaveBeenCalledTimes(1)
    useHostStore.setState({ hosts: { h1: host('h1', '10.0.0.1', 'dm-a'), h2: host('h2', '10.0.0.2', 'dm-b') }, runtime: { h1: verified('10.0.0.1', 'dm-a'), h2: verified('10.0.0.2', 'dm-b') } } as never)
    await flush()
    expect(getJournal.mock.calls.map((c) => [c[0], c[1]])).toEqual([['h1', 'sA'], ['h2', 'lead-s']])
    expect(store().routes[`h1\u0000sA`]).toMatchObject({ kind: 'follow', hostId: 'h2' })
  })

  it('a lead host that answers 409 itself does not loop', async () => {
    getJournal.mockResolvedValue(remote)
    stop = startJournalLoader()
    await flush()
    await flush()
    expect(getJournal).toHaveBeenCalledTimes(2)
    expect(store().routes[`h1\u0000sA`]?.kind).toBe('unreachable')
  })

  it('journal.changed re-reads the followed unit on the lead host', async () => {
    getJournal.mockImplementation(async (h: string) => (h === 'h1' ? remote : okPage('team-1')))
    stop = startJournalLoader()
    await flush()
    getJournal.mockClear()
    changed('h2', 'team-1', 'entries')
    await vi.advanceTimersByTimeAsync(STALE_DEBOUNCE_MS)
    expect(getJournal.mock.calls.map((c) => [c[0], c[1]])).toEqual([['h2', 'lead-s']])
  })
})

describe('journal.changed', () => {
  it('re-reads the active unit once for a burst, after the debounce', async () => {
    stop = startJournalLoader()
    await flush()
    getJournal.mockClear()
    for (let i = 0; i < 20; i++) changed('h1', 'unit-sA', 'entries')
    await vi.advanceTimersByTimeAsync(STALE_DEBOUNCE_MS - 1)
    expect(getJournal).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(getJournal).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(2000)
    expect(getJournal).toHaveBeenCalledTimes(1)
  })

  it('what: tasks re-reads the tasks only, and only once the tasks were loaded', async () => {
    getTasks.mockResolvedValue({ kind: 'ok', value: [] })
    stop = startJournalLoader()
    await flush()
    getJournal.mockClear()
    changed('h1', 'unit-sA', 'tasks') // tasks never opened: the flag waits, nothing is read
    await vi.advanceTimersByTimeAsync(STALE_DEBOUNCE_MS * 2)
    expect(getTasks).not.toHaveBeenCalled()
    await store().loadTasks('h1', 'sA')
    getTasks.mockClear()
    changed('h1', 'unit-sA', 'tasks')
    await vi.advanceTimersByTimeAsync(STALE_DEBOUNCE_MS)
    expect(getTasks).toHaveBeenCalledTimes(1)
    expect(getJournal).not.toHaveBeenCalled()
  })

  it('a hidden unit\'s event fetches nothing; when its tab becomes active it is read once', async () => {
    stop = startJournalLoader()
    await flush()
    activate('B'); await flush()
    getJournal.mockClear()
    for (let i = 0; i < 5; i++) changed('h1', 'unit-sA', 'entries') // A is hidden
    await vi.advanceTimersByTimeAsync(STALE_DEBOUNCE_MS * 3)
    expect(getJournal).not.toHaveBeenCalled()
    activate('A')
    await vi.advanceTimersByTimeAsync(STALE_DEBOUNCE_MS)
    expect(getJournal).toHaveBeenCalledTimes(1)
    expect(getJournal.mock.calls[0][1]).toBe('sA')
    await vi.advanceTimersByTimeAsync(2000)
    expect(getJournal).toHaveBeenCalledTimes(1)
  })

  it('an event during the re-read keeps the unit stale and is read again', async () => {
    stop = startJournalLoader()
    await flush()
    let release!: (v: unknown) => void
    getJournal.mockClear()
    getJournal.mockImplementationOnce(() => new Promise((r) => { release = r }))
    changed('h1', 'unit-sA', 'entries')
    await vi.advanceTimersByTimeAsync(STALE_DEBOUNCE_MS)
    changed('h1', 'unit-sA', 'entries') // lands while the read is out
    release(okPage('unit-sA'))
    await flush()
    await vi.advanceTimersByTimeAsync(STALE_DEBOUNCE_MS)
    expect(getJournal).toHaveBeenCalledTimes(2)
  })
})
