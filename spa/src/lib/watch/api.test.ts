import { describe, it, expect, vi, beforeEach } from 'vitest'

const hostFetch = vi.fn()
vi.mock('../host-api', () => ({ hostFetch: (...a: unknown[]) => hostFetch(...a) }))
import { fetchWatchStatus, fetchWatchEvents } from './api'

const body = (o: unknown, status = 200) => new Response(JSON.stringify(o), { status })
const ok = { self: { host_id: 'd1', state: 'awake', session_id: 's1', since: '2026-10-16T01:00:00Z' }, enabled: true, auto_wake: true, mode: 'patrol', targets: ['a', 'b'] }

beforeEach(() => hostFetch.mockReset())

describe('fetchWatchStatus', () => {
  it('reads the status of a host', async () => {
    hostFetch.mockResolvedValue(body(ok))
    const r = await fetchWatchStatus('h1')
    expect(hostFetch).toHaveBeenCalledWith('h1', '/api/watch/status')
    expect(r).toMatchObject({ kind: 'ok', status: { self: { host_id: 'd1', state: 'awake' }, mode: 'patrol', targets: ['a', 'b'] } })
  })
  it('404 is unsupported', async () => {
    hostFetch.mockResolvedValue(body({}, 404))
    expect(await fetchWatchStatus('h1')).toEqual({ kind: 'unsupported' })
  })
  it('a malformed body is an error', async () => {
    hostFetch.mockResolvedValue(body({ nope: 1 }))
    expect(await fetchWatchStatus('h1')).toEqual({ kind: 'error' })
  })
  it('an unknown state is an error', async () => {
    hostFetch.mockResolvedValue(body({ ...ok, self: { ...ok.self, state: 'sleeping' } }))
    expect(await fetchWatchStatus('h1')).toEqual({ kind: 'error' })
  })
  it('a 500 and a network failure are errors', async () => {
    hostFetch.mockResolvedValueOnce(body({}, 500))
    expect(await fetchWatchStatus('h1')).toEqual({ kind: 'error' })
    hostFetch.mockRejectedValueOnce(new Error('down'))
    expect(await fetchWatchStatus('h1')).toEqual({ kind: 'error' })
  })
})

describe('fetchWatchEvents', () => {
  const ev = { id: 'm-1', host: 'm', kind: 'pressure_high', at: '2026-10-17T01:00:00Z', status: 'pending' }
  it('reads the events of a host', async () => {
    hostFetch.mockResolvedValue(body({ events: [ev] }))
    const r = await fetchWatchEvents('h1')
    expect(hostFetch).toHaveBeenCalledWith('h1', '/api/watch/events?limit=500')
    expect(r).toMatchObject({ kind: 'ok', events: [{ id: 'm-1', kind: 'pressure_high' }] })
  })
  it('404 is unsupported; another status, a thrown fetch and an unreadable body are errors', async () => {
    hostFetch.mockResolvedValue(body({}, 404))
    expect(await fetchWatchEvents('h1')).toEqual({ kind: 'unsupported' })
    hostFetch.mockResolvedValue(body({}, 500))
    expect(await fetchWatchEvents('h1')).toEqual({ kind: 'error' })
    hostFetch.mockRejectedValue(new Error('down'))
    expect(await fetchWatchEvents('h1')).toEqual({ kind: 'error' })
    hostFetch.mockResolvedValue(body({ nope: 1 }))
    expect(await fetchWatchEvents('h1')).toEqual({ kind: 'error' })
  })
})
