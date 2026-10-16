import { describe, it, expect, vi, beforeEach } from 'vitest'

const hostFetch = vi.fn()
vi.mock('../host-api', () => ({ hostFetch: (...a: unknown[]) => hostFetch(...a) }))
import { fetchWatchStatus } from './api'

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
