import { describe, it, expect, vi, afterEach } from 'vitest'
import { checkHealth } from './host-connection'

describe('checkHealth', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('returns connected with latency on HTTP 200', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), { status: 200 })
    )

    const result = await checkHealth('http://localhost:7860')
    expect(result.daemon).toBe('connected')
    // health is pure liveness — tmux status comes via WS or /api/ready
    expect(result.tmux).toBe('unavailable')
    expect(result.latency).toBeGreaterThanOrEqual(0)
  })

  it('returns refused on TypeError (connection refused)', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('Failed to fetch'))

    const result = await checkHealth('http://localhost:7860')
    expect(result.daemon).toBe('refused')
    expect(result.tmux).toBe('unavailable')
    expect(result.latency).toBeNull()
  })

  it('returns unreachable on AbortError (timeout)', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(
      Object.assign(new Error('signal is aborted'), { name: 'AbortError' })
    )

    const result = await checkHealth('http://localhost:7860')
    expect(result.daemon).toBe('unreachable')
    expect(result.tmux).toBe('unavailable')
    expect(result.latency).toBeNull()
  })
})
