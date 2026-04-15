import { describe, it, expect, vi, afterEach } from 'vitest'
import { checkHealth } from './host-connection'

function healthResponse(mode = 'normal') {
  return new Response(JSON.stringify({ ok: true, mode }), { status: 200 })
}
function ticketResponse(ticket = 'tk_abc') {
  return new Response(JSON.stringify({ ticket }), { status: 200 })
}

describe('checkHealth', () => {
  afterEach(() => { vi.restoreAllMocks() })

  it('Phase 1 only: no token, non-pairing → auth-error', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(healthResponse('normal'))
    const result = await checkHealth('http://localhost:7860')
    expect(result.daemon).toBe('auth-error')
    expect(result.mode).toBe('normal')
    expect(fetch).toHaveBeenCalledTimes(1) // Phase 2 skipped
  })

  it('Phase 1 only: no token, pairing mode → connected', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(healthResponse('pairing'))
    const result = await checkHealth('http://localhost:7860')
    expect(result.daemon).toBe('connected')
    expect(result.mode).toBe('pairing')
  })

  it('Phase 2 success: connected + ticket', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(healthResponse())
      .mockResolvedValueOnce(ticketResponse('tk_123'))
    const result = await checkHealth('http://localhost:7860', () => 'mytoken')
    expect(result.daemon).toBe('connected')
    expect(result.ticket).toBe('tk_123')
    expect(result.mode).toBe('normal')
  })

  it('Phase 2 returns 401 → auth-error', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(healthResponse())
      .mockResolvedValueOnce(new Response('unauthorized', { status: 401 }))
    const result = await checkHealth('http://localhost:7860', () => 'badtoken')
    expect(result.daemon).toBe('auth-error')
  })

  it('Phase 2 returns 503 (PairingGuard) → auth-error', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(healthResponse())
      .mockResolvedValueOnce(new Response('pairing_mode', { status: 503 }))
    const result = await checkHealth('http://localhost:7860', () => 'mytoken')
    expect(result.daemon).toBe('auth-error')
  })

  it('Phase 2 network error → fallback connected', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(healthResponse())
      .mockRejectedValueOnce(new TypeError('Failed to fetch'))
    const result = await checkHealth('http://localhost:7860', () => 'mytoken')
    expect(result.daemon).toBe('connected')
    expect(result.ticket).toBeUndefined()
  })

  it('Phase 1 timeout → unreachable', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(
      Object.assign(new Error('signal is aborted'), { name: 'AbortError' })
    )
    const result = await checkHealth('http://localhost:7860', () => 'mytoken')
    expect(result.daemon).toBe('unreachable')
    expect(result.latency).toBeNull()
  })

  it('Phase 1 network error → refused', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('Failed to fetch'))
    const result = await checkHealth('http://localhost:7860', () => 'mytoken')
    expect(result.daemon).toBe('refused')
  })

  it('mode field parsed from health response', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(healthResponse('pending'))
      .mockResolvedValueOnce(ticketResponse())
    const result = await checkHealth('http://localhost:7860', () => 'tok')
    expect(result.mode).toBe('pending')
  })

  it('Phase 1 JSON parse error (no token) → connected, not refused', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response('not-json', { status: 200 })
    )
    const result = await checkHealth('http://localhost:7860')
    expect(result.daemon).toBe('connected')
    expect(result.latency).toBeGreaterThanOrEqual(0)
    expect(result.mode).toBe('normal')
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('Phase 1 JSON parse error (with token) → connected, Phase 2 skipped', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response('not-json', { status: 200 })
    )
    const result = await checkHealth('http://localhost:7860', () => 'mytoken')
    expect(result.daemon).toBe('connected')
    expect(result.ticket).toBeUndefined()
    expect(result.latency).toBeGreaterThanOrEqual(0)
    expect(fetch).toHaveBeenCalledTimes(1) // Phase 2 not attempted
  })

  it('latency measured from Phase 1', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(healthResponse())
      .mockResolvedValueOnce(ticketResponse())
    const result = await checkHealth('http://localhost:7860', () => 'tok')
    expect(result.latency).toBeGreaterThanOrEqual(0)
  })
})
