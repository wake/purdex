import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

const fetchWatchEvents = vi.fn()
vi.mock('./api', () => ({ fetchWatchEvents: (...a: unknown[]) => fetchWatchEvents(...a), fetchWatchStatus: vi.fn() }))
import { useHostStore } from '../../stores/useHostStore'
import { useWatchStatusStore } from './status-store'
import { startWatchEvents, rereadWatchEvents, useWatchEventsStore, pendingCount, anyHostDeclaresWatch, type EventsEntry } from './events-store'
import type { WatchEvent } from './events-types'

const ev = (id: string, status: 'pending' | 'handled' = 'pending'): WatchEvent => ({ id, host: 'm', kind: 'pressure_high', at: '2026-10-17T01:00:00Z', status })
const okStatus = { kind: 'ok', at: 1, status: { self: { host_id: 'd', state: 'none' }, enabled: true, auto_wake: true, mode: '', targets: [] } } as never

const setHosts = (ids: string[], connected: string[]) => useHostStore.setState({
  hosts: Object.fromEntries(ids.map((id) => [id, { id, name: id, ip: '1', port: 1, token: 't', order: 0 }])),
  hostOrder: ids,
  runtime: Object.fromEntries(connected.map((id) => [id, { status: 'connected' }])),
} as never)

describe('pendingCount / anyHostDeclaresWatch', () => {
  it('counts the pending events of the hosts that answered, and none for an error', () => {
    const by: Record<string, EventsEntry> = {
      a: { kind: 'ok', at: 1, events: [ev('1'), ev('2', 'handled'), ev('3')] },
      b: { kind: 'ok', at: 1, events: [ev('4')] },
      c: { kind: 'error', at: 1 },
    }
    expect(pendingCount(by)).toBe(3)
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
    expect(pendingCount(useWatchEventsStore.getState().byHost)).toBe(2)
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
    await flush()
    expect(fetchWatchEvents).toHaveBeenCalledTimes(1)
  })
})
