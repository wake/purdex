import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import { createTransfer, formatTransferCode, redeemTransfer, TRANSFER_TIMEOUT_MS, type TransferRow } from './host-transfer-api'

function json(body: unknown, status = 200, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json', ...headers } })
}

const ROW: TransferRow = { name: 'air26', ip: '100.64.0.4', port: 7860, token: 'tok-air' }

let relay: string
let fetchSpy: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  useHostStore.getState().reset()
  relay = useHostStore.getState().addHost({ name: 'relay', ip: '100.64.0.2', port: 7860, token: 'relay-tok' })
  fetchSpy = vi.spyOn(globalThis, 'fetch')
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('createTransfer', () => {
  it('posts {hosts} to the relay with the relay token and returns code + expiresAt', async () => {
    fetchSpy.mockResolvedValue(json({ code: 'ABCD2345', expiresAt: 1_700_000_000_000 }))
    const res = await createTransfer(relay, [ROW])
    expect(res).toEqual({ kind: 'ok', code: 'ABCD2345', expiresAt: 1_700_000_000_000 })
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('http://100.64.0.2:7860/api/host-transfer')
    expect(init.method).toBe('POST')
    expect(new Headers(init.headers).get('Authorization')).toBe('Bearer relay-tok')
    expect(JSON.parse(init.body as string)).toEqual({ hosts: [ROW] })
  })

  it.each([
    [400, { reason: 'bad_payload' }, 'bad_payload'],
    [413, { reason: 'too_large' }, 'too_large'],
    [429, { reason: 'capacity' }, 'capacity'],
    [503, { reason: 'unavailable' }, 'unavailable'],
    [401, { reason: 'unauthorized' }, 'unauthorized'],
    [403, { reason: 'no_token' }, 'no_token'],
    [404, 'not json', 'unsupported'],
    [500, 'boom', 'malformed'],
  ])('status %i → %s', async (status, body, reason) => {
    fetchSpy.mockResolvedValue(typeof body === 'string' ? new Response(body, { status }) : json(body, status))
    const res = await createTransfer(relay, [ROW])
    expect(res).toMatchObject({ kind: 'failed', reason, status })
  })

  it('a 200 without a code is malformed', async () => {
    fetchSpy.mockResolvedValue(json({ expiresAt: 1 }))
    expect(await createTransfer(relay, [ROW])).toMatchObject({ kind: 'failed', reason: 'malformed' })
  })

  it('a 200 whose body is not JSON is malformed', async () => {
    fetchSpy.mockResolvedValue(new Response('<html>', { status: 200 }))
    expect(await createTransfer(relay, [ROW])).toMatchObject({ kind: 'failed', reason: 'malformed' })
  })

  it('a rejected fetch is network', async () => {
    fetchSpy.mockRejectedValue(new TypeError('Failed to fetch'))
    expect(await createTransfer(relay, [ROW])).toMatchObject({ kind: 'failed', reason: 'network', status: 0 })
  })

  it('an unknown relay is never sent to', async () => {
    const res = await createTransfer('gone', [ROW])
    expect(res).toMatchObject({ kind: 'failed', reason: 'unknown_host' })
    expect(fetchSpy).not.toHaveBeenCalled()
  })

  it('times out after TRANSFER_TIMEOUT_MS even when the transport ignores its signal', async () => {
    vi.useFakeTimers()
    fetchSpy.mockImplementation(() => new Promise<Response>(() => {}))
    let settled: unknown = null
    void createTransfer(relay, [ROW]).then((r) => (settled = r))
    await vi.advanceTimersByTimeAsync(TRANSFER_TIMEOUT_MS - 1)
    expect(settled).toBeNull()
    await vi.advanceTimersByTimeAsync(1)
    expect(settled).toMatchObject({ kind: 'failed', reason: 'timeout' })
  })
})

describe('redeemTransfer', () => {
  it('posts {code} and returns the raw hosts array', async () => {
    fetchSpy.mockResolvedValue(json({ hosts: [ROW, { junk: 1 }] }))
    const res = await redeemTransfer(relay, 'abcd-2345')
    expect(res).toEqual({ kind: 'ok', hosts: [ROW, { junk: 1 }] })
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('http://100.64.0.2:7860/api/host-transfer/redeem')
    expect(JSON.parse(init.body as string)).toEqual({ code: 'abcd-2345' })
  })

  it('429 rate_limited carries Retry-After in seconds', async () => {
    fetchSpy.mockResolvedValue(json({ reason: 'rate_limited' }, 429, { 'Retry-After': '42' }))
    expect(await redeemTransfer(relay, 'X')).toEqual({ kind: 'failed', reason: 'rate_limited', status: 429, retryAfterS: 42 })
  })

  it.each([
    [400, { reason: 'bad_request' }, 'bad_request'],
    [404, { reason: 'invalid_code' }, 'invalid_code'],
    [404, '404 page not found', 'unsupported'],
    [503, { reason: 'unavailable' }, 'unavailable'],
    [401, { reason: 'unauthorized' }, 'unauthorized'],
    [403, { reason: 'no_token' }, 'no_token'],
    [413, { reason: 'too_large' }, 'too_large'],
  ])('status %i %j → %s', async (status, body, reason) => {
    fetchSpy.mockResolvedValue(typeof body === 'string' ? new Response(body, { status }) : json(body, status))
    expect(await redeemTransfer(relay, 'X')).toMatchObject({ kind: 'failed', reason, status })
  })

  it('a 200 whose hosts is not an array is malformed', async () => {
    fetchSpy.mockResolvedValue(json({ hosts: {} }))
    expect(await redeemTransfer(relay, 'X')).toMatchObject({ kind: 'failed', reason: 'malformed' })
  })

  it('times out with fake timers', async () => {
    vi.useFakeTimers()
    fetchSpy.mockImplementation(() => new Promise<Response>(() => {}))
    const p = redeemTransfer(relay, 'X')
    await vi.advanceTimersByTimeAsync(TRANSFER_TIMEOUT_MS)
    expect(await p).toMatchObject({ kind: 'failed', reason: 'timeout' })
  })
})

describe('formatTransferCode', () => {
  it('splits eight characters as ABCD-2345', () => {
    expect(formatTransferCode('ABCD2345')).toBe('ABCD-2345')
  })
  it('leaves anything else as is', () => {
    expect(formatTransferCode('ABC')).toBe('ABC')
  })
})
