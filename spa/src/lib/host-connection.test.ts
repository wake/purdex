import { describe, it, expect, vi, afterEach } from 'vitest'
import { checkHealth } from './host-connection'

describe('checkHealth', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('returns connected with latency on HTTP 200', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ok: true, tmux: true }), { status: 200 })
    )

    const result = await checkHealth('http://localhost:7860')
    expect(result.daemon).toBe('connected')
    expect(result.tmux).toBe('ok')
    expect(result.latency).toBeGreaterThanOrEqual(0)
  })

  it('returns connected with tmux unavailable', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ok: true, tmux: false }), { status: 200 })
    )

    const result = await checkHealth('http://localhost:7860')
    expect(result.daemon).toBe('connected')
    expect(result.tmux).toBe('unavailable')
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
