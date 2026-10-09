import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import * as hostApi from './host-api'
import { useHostStore } from '../stores/useHostStore'
import { DEVICES_TIMEOUT_MS, listDevices, revokePairing } from './devices-api'

const PID = '11111111-2222-4333-8444-555555555555'

const row = {
  id: 'dev1', pairing_id: PID, profile_id: 'p_0123456789ab', label: 'iPhone', created_at: 1, created_by: 'admin',
  use_by: 2, first_used_at: 0, last_used_at: 0, revoked_at: 0,
}

function res(status: number, body?: unknown): Response {
  return new Response(body === undefined ? null : typeof body === 'string' ? body : JSON.stringify(body), { status })
}

beforeEach(() => {
  useHostStore.setState({ hosts: { h: { id: 'h', name: 'h', ip: '1.1.1.1', port: 1, order: 0, token: 't' } }, hostOrder: ['h'] })
})
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('listDevices', () => {
  it('200 returns the rows', async () => {
    const spy = vi.spyOn(hostApi, 'hostFetch').mockResolvedValue(res(200, { devices: [row] }))
    const r = await listDevices('h')
    expect(r).toEqual({ kind: 'ok', rows: [row] })
    expect(spy.mock.calls[0][1]).toBe('/api/devices')
  })

  it('drops rows that are not objects with string ids', async () => {
    vi.spyOn(hostApi, 'hostFetch').mockResolvedValue(res(200, { devices: [row, 5, { id: 1 }] }))
    expect(await listDevices('h')).toEqual({ kind: 'ok', rows: [row] })
  })

  it('404 is unsupported, 401/403 unauthorized, 500 malformed, bad 200 malformed', async () => {
    const spy = vi.spyOn(hostApi, 'hostFetch')
    spy.mockResolvedValueOnce(res(404))
    expect(await listDevices('h')).toMatchObject({ kind: 'failed', reason: 'unsupported', status: 404 })
    spy.mockResolvedValueOnce(res(401))
    expect(await listDevices('h')).toMatchObject({ kind: 'failed', reason: 'unauthorized' })
    spy.mockResolvedValueOnce(res(403))
    expect(await listDevices('h')).toMatchObject({ kind: 'failed', reason: 'unauthorized' })
    spy.mockResolvedValueOnce(res(500))
    expect(await listDevices('h')).toMatchObject({ kind: 'failed', reason: 'malformed', status: 500 })
    spy.mockResolvedValueOnce(res(200, 'not json'))
    expect(await listDevices('h')).toMatchObject({ kind: 'failed', reason: 'malformed' })
  })

  it('a rejected fetch is network; an unknown host is never sent to', async () => {
    const spy = vi.spyOn(hostApi, 'hostFetch').mockRejectedValue(new Error('boom'))
    expect(await listDevices('h')).toMatchObject({ kind: 'failed', reason: 'network' })
    spy.mockClear()
    expect(await listDevices('ghost')).toMatchObject({ kind: 'failed', reason: 'unknown_host' })
    expect(spy).not.toHaveBeenCalled()
  })

  it('times out even when the transport ignores its signal', async () => {
    vi.useFakeTimers()
    vi.spyOn(hostApi, 'hostFetch').mockReturnValue(new Promise(() => {}))
    const p = listDevices('h')
    await vi.advanceTimersByTimeAsync(DEVICES_TIMEOUT_MS)
    expect(await p).toMatchObject({ kind: 'failed', reason: 'timeout' })
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('revokePairing', () => {
  it('204 is ok and the request is DELETE with the pairing id in the query', async () => {
    const spy = vi.spyOn(hostApi, 'hostFetch').mockResolvedValue(res(204))
    expect(await revokePairing('h', PID)).toEqual({ kind: 'ok' })
    expect(spy.mock.calls[0][1]).toBe(`/api/devices?pairing_id=${PID}`)
    expect(spy.mock.calls[0][2]).toMatchObject({ method: 'DELETE' })
  })

  it('404 is unsupported (a daemon without devices.v1), not an error to retry', async () => {
    vi.spyOn(hostApi, 'hostFetch').mockResolvedValue(res(404))
    expect(await revokePairing('h', PID)).toEqual({ kind: 'unsupported' })
  })

  it('other statuses and transport errors are failures', async () => {
    const spy = vi.spyOn(hostApi, 'hostFetch')
    spy.mockResolvedValueOnce(res(503))
    expect(await revokePairing('h', PID)).toMatchObject({ kind: 'failed', reason: 'malformed', status: 503 })
    spy.mockResolvedValueOnce(res(401))
    expect(await revokePairing('h', PID)).toMatchObject({ kind: 'failed', reason: 'unauthorized' })
    spy.mockRejectedValueOnce(new Error('x'))
    expect(await revokePairing('h', PID)).toMatchObject({ kind: 'failed', reason: 'network' })
    expect(await revokePairing('ghost', PID)).toMatchObject({ kind: 'failed', reason: 'unknown_host' })
  })

  it('times out', async () => {
    vi.useFakeTimers()
    vi.spyOn(hostApi, 'hostFetch').mockReturnValue(new Promise(() => {}))
    const p = revokePairing('h', PID)
    await vi.advanceTimersByTimeAsync(DEVICES_TIMEOUT_MS)
    expect(await p).toMatchObject({ kind: 'failed', reason: 'timeout' })
  })
})
