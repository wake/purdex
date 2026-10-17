import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

const fetchWatchEvents = vi.fn()
const fetchWatchUnhandled = vi.fn()
vi.mock('./api', () => ({
  fetchWatchEvents: (...a: unknown[]) => fetchWatchEvents(...a),
  fetchWatchUnhandled: (...a: unknown[]) => fetchWatchUnhandled(...a),
  fetchWatchStatus: vi.fn(),
}))
import { useHostStore } from '../../stores/useHostStore'
import { useWatchStatusStore } from './status-store'
import { startWatchEvents, rereadWatchEvents, applyWatchUnhandled, useWatchEventsStore, pendingCount, anyHostDeclaresWatch } from './events-store'
import type { WatchEvent } from './events-types'

const ev = (id: string, status: 'pending' | 'handled' = 'pending'): WatchEvent => ({ id, host: 'm', kind: 'pressure_high', at: '2026-10-17T01:00:00Z', status })
const okStatus = { kind: 'ok', at: 1, status: { self: { host_id: 'd', state: 'none' }, enabled: true, auto_wake: true, mode: '', targets: [] } } as never

const setHosts = (ids: string[], connected: string[]) => useHostStore.setState({
  hosts: Object.fromEntries(ids.map((id) => [id, { id, name: id, ip: '1', port: 1, token: 't', order: 0 }])),
  hostOrder: ids,
  runtime: Object.fromEntries(connected.map((id) => [id, { status: 'connected' }])),
} as never)

describe('pendingCount / anyHostDeclaresWatch', () => {
  it('adds up the daemons\' own unhandled counts; a host that has not said counts none', () => {
    expect(pendingCount({ a: 3, b: 620 })).toBe(623)
    expect(pendingCount({})).toBe(0)
  })
  it('a watch host is one whose status answered ok', () => {
    expect(anyHostDeclaresWatch({ a: { kind: 'unsupported' }, b: { kind: 'error' } })).toBe(false)
    expect(anyHostDeclaresWatch({ a: { kind: 'unsupported' }, b: { kind: 'ok' } })).toBe(true)
  })
})

describe('startWatchEvents', () => {
  let stop: () => void
  beforeEach(() => {
    fetchWatchEvents.mockReset()
    fetchWatchEvents.mockResolvedValue({ kind: 'ok', events: [ev('1')] })
    fetchWatchUnhandled.mockReset()
    fetchWatchUnhandled.mockResolvedValue({ kind: 'ok', count: 1 })
    useWatchStatusStore.getState().reset()
    useWatchEventsStore.getState().reset()
    setHosts(['h1'], ['h1'])
  })
  afterEach(() => { stop?.() })
  const flush = () => new Promise((r) => setTimeout(r, 0))

  it('reads a host once its status answers ok, and not before', async () => {
    stop = startWatchEvents()
    await flush()
    expect(fetchWatchEvents).not.toHaveBeenCalled()
    useWatchStatusStore.getState().set('h1', okStatus)
    await flush()
    expect(fetchWatchEvents).toHaveBeenCalledTimes(1)
    expect(fetchWatchEvents).toHaveBeenCalledWith('h1')
    expect(useWatchEventsStore.getState().byHost.h1).toMatchObject({ kind: 'ok', events: [{ id: '1' }] })
  })

  it('does not read a host whose status is unsupported', async () => {
    stop = startWatchEvents()
    useWatchStatusStore.getState().set('h1', { kind: 'unsupported' })
    await flush()
    expect(fetchWatchEvents).not.toHaveBeenCalled()
  })

  it('a status read again (watch.changed) does not read the events again', async () => {
    stop = startWatchEvents()
    useWatchStatusStore.getState().set('h1', okStatus)
    await flush()
    useWatchStatusStore.getState().set('h1', okStatus)
    await flush()
    expect(fetchWatchEvents).toHaveBeenCalledTimes(1)
  })

  it('a watch.events push reads that host again, and only one that carries the watch module', async () => {
    stop = startWatchEvents()
    useWatchStatusStore.getState().set('h1', okStatus)
    await flush()
    fetchWatchEvents.mockResolvedValue({ kind: 'ok', events: [ev('1'), ev('2')] })
    rereadWatchEvents('h1')
    await flush()
    expect(fetchWatchEvents).toHaveBeenCalledTimes(2)
    rereadWatchEvents('nobody')
    await flush()
    expect(fetchWatchEvents).toHaveBeenCalledTimes(2)
  })

  it('of two reads in flight the newer wins, whichever answers last', async () => {
    stop = startWatchEvents()
    let first!: (v: unknown) => void
    fetchWatchEvents.mockReturnValueOnce(new Promise((r) => { first = r }))
    useWatchStatusStore.getState().set('h1', okStatus)
    fetchWatchEvents.mockResolvedValueOnce({ kind: 'ok', events: [ev('new')] })
    rereadWatchEvents('h1')
    await flush()
    first({ kind: 'ok', events: [ev('old')] })
    await flush()
    expect(useWatchEventsStore.getState().byHost.h1).toMatchObject({ events: [{ id: 'new' }] })
  })

  it('forgets the events of a host that disconnects, and drops an answer that lands after', async () => {
    stop = startWatchEvents()
    let late!: (v: unknown) => void
    useWatchStatusStore.getState().set('h1', okStatus)
    await flush()
    expect(useWatchEventsStore.getState().byHost.h1).toBeDefined()
    fetchWatchEvents.mockReturnValueOnce(new Promise((r) => { late = r }))
    rereadWatchEvents('h1')
    setHosts(['h1'], [])
    expect(useWatchEventsStore.getState().byHost.h1).toBeUndefined()
    late({ kind: 'ok', events: [ev('ghost')] })
    await flush()
    expect(useWatchEventsStore.getState().byHost.h1).toBeUndefined()
  })

  it('forgets a removed host, and one whose status stops answering ok', async () => {
    stop = startWatchEvents()
    useWatchStatusStore.getState().set('h1', okStatus)
    await flush()
    useWatchStatusStore.getState().forget('h1')
    expect(useWatchEventsStore.getState().byHost.h1).toBeUndefined()
    useWatchStatusStore.getState().set('h1', okStatus)
    await flush()
    expect(useWatchEventsStore.getState().byHost.h1).toBeDefined()
    setHosts([], [])
    expect(useWatchEventsStore.getState().byHost.h1).toBeUndefined()
  })

  it('an unreadable answer is kept as an error entry; a 404 forgets the host', async () => {
    stop = startWatchEvents()
    fetchWatchEvents.mockResolvedValue({ kind: 'error' })
    useWatchStatusStore.getState().set('h1', okStatus)
    await flush()
    expect(useWatchEventsStore.getState().byHost.h1.kind).toBe('error')
    fetchWatchEvents.mockResolvedValue({ kind: 'unsupported' })
    rereadWatchEvents('h1')
    await flush()
    expect(useWatchEventsStore.getState().byHost.h1).toBeUndefined()
  })

  it('a push after stop does nothing', async () => {
    stop = startWatchEvents()
    useWatchStatusStore.getState().set('h1', okStatus)
    await flush()
    stop()
    rereadWatchEvents('h1')
    applyWatchUnhandled('h1', 9)
    await flush()
    expect(fetchWatchEvents).toHaveBeenCalledTimes(1)
    expect(useWatchEventsStore.getState().unhandled.h1).toBe(1)
  })

  describe('the count is the daemon\'s own unhandled number', () => {
    it('reads GET /api/watch/unhandled with the first read: 500 rows listed, 620 unhandled → 620', async () => {
      fetchWatchEvents.mockResolvedValue({ kind: 'ok', events: Array.from({ length: 500 }, (_, i) => ev(String(i))) })
      fetchWatchUnhandled.mockResolvedValue({ kind: 'ok', count: 620 })
      stop = startWatchEvents()
      useWatchStatusStore.getState().set('h1', okStatus)
      await flush()
      expect(fetchWatchUnhandled).toHaveBeenCalledWith('h1')
      expect(pendingCount(useWatchEventsStore.getState().unhandled)).toBe(620)
    })

    it('a watch.events payload sets the host\'s count at once; the list is read again as before', async () => {
      stop = startWatchEvents()
      useWatchStatusStore.getState().set('h1', okStatus)
      await flush()
      fetchWatchEvents.mockResolvedValue({ kind: 'ok', events: [ev('1'), ev('2')] })
      applyWatchUnhandled('h1', 7)
      expect(useWatchEventsStore.getState().unhandled.h1).toBe(7)
      rereadWatchEvents('h1')
      await flush()
      expect(useWatchEventsStore.getState().unhandled.h1).toBe(7) // two pending rows listed, still the daemon's 7
    })

    it('the first count read, answering after a newer payload, does not overwrite it', async () => {
      let late!: (v: unknown) => void
      fetchWatchUnhandled.mockReturnValueOnce(new Promise((r) => { late = r }))
      stop = startWatchEvents()
      useWatchStatusStore.getState().set('h1', okStatus)
      await flush()
      applyWatchUnhandled('h1', 5)
      late({ kind: 'ok', count: 99 })
      await flush()
      expect(useWatchEventsStore.getState().unhandled.h1).toBe(5)
    })

    it('a payload for a host that is not a watch host, or a bad number, changes nothing', async () => {
      stop = startWatchEvents()
      useWatchStatusStore.getState().set('h1', okStatus)
      await flush()
      applyWatchUnhandled('nobody', 4)
      applyWatchUnhandled('h1', -1)
      applyWatchUnhandled('h1', 1.5)
      applyWatchUnhandled('h1', Number.NaN)
      expect(useWatchEventsStore.getState().unhandled).toEqual({ h1: 1 })
    })

    it('an unreadable count leaves the host uncounted; the count goes with the host on disconnect', async () => {
      fetchWatchUnhandled.mockResolvedValue({ kind: 'error' })
      stop = startWatchEvents()
      useWatchStatusStore.getState().set('h1', okStatus)
      await flush()
      expect(useWatchEventsStore.getState().unhandled).toEqual({})
      applyWatchUnhandled('h1', 3)
      setHosts(['h1'], [])
      expect(useWatchEventsStore.getState().unhandled).toEqual({})
    })
  })
})
