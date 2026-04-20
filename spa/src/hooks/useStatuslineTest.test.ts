import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useStatuslineTest } from './useStatuslineTest'
import { hostFetch } from '../lib/host-api'
import { statuslineTestBus } from '../lib/statusline-test-bus'
import { useAgentStore } from '../stores/useAgentStore'

vi.mock('../lib/host-api', () => ({ hostFetch: vi.fn() }))
const mockFetch = hostFetch as unknown as ReturnType<typeof vi.fn>

function sseBodyFrom(events: unknown[]): ReadableStream<Uint8Array> {
  return new ReadableStream<Uint8Array>({
    start(c) {
      const enc = new TextEncoder()
      c.enqueue(enc.encode(events.map((e) => `data: ${JSON.stringify(e)}\n\n`).join('')))
      c.close()
    },
  })
}

beforeEach(() => {
  mockFetch.mockReset()
  statuslineTestBus.reset()
  useAgentStore.setState({ ccStatus: {} })
})

describe('useStatuslineTest', () => {
  it('initial state: all stages untested, not running', () => {
    const { result } = renderHook(() => useStatuslineTest('h1'))
    expect(result.current.state.running).toBe(false)
    expect(result.current.state.stages[1].status).toBe('untested')
    expect(result.current.state.stages[5].status).toBe('untested')
    expect(result.current.state.nonce).toBeNull()
  })

  it('happy path: all 5 stages pass', async () => {
    const nonce = '__pdx_test_aaaa1111'
    const body = sseBodyFrom([
      { type: 'init', nonce },
      { type: 'stage', stage: 1, name: 'Proxy spawned', status: 'passed', elapsed_ms: 12, nonce },
      { type: 'stage', stage: 2, name: 'Proxy → daemon POST received', status: 'passed', elapsed_ms: 8, nonce },
      { type: 'stage', stage: 3, name: 'Daemon → WS broadcast', status: 'passed', elapsed_ms: 3, nonce },
      { type: 'done', nonce },
    ])
    mockFetch.mockResolvedValueOnce({ ok: true, status: 200, body } as unknown as Response)
    mockFetch.mockResolvedValueOnce({ ok: true, status: 200 } as unknown as Response)

    const { result } = renderHook(() => useStatuslineTest('h1'))
    await act(async () => {
      const p = result.current.run()
      // Simulate the WS dispatcher firing while the hook is consuming the SSE body.
      queueMicrotask(() => {
        useAgentStore.getState().setCcStatus('h1', nonce, { model: { id: 'x' } })
        statuslineTestBus.emit({ nonce, hostId: 'h1', raw: { model: { id: 'x' } } })
      })
      await p
    })

    expect(result.current.state.stages[1].status).toBe('passed')
    expect(result.current.state.stages[2].status).toBe('passed')
    expect(result.current.state.stages[3].status).toBe('passed')
    expect(result.current.state.stages[4].status).toBe('passed')
    expect(result.current.state.stages[5].status).toBe('passed')
    expect(result.current.state.lastRunAt).not.toBeNull()
    expect(mockFetch).toHaveBeenNthCalledWith(
      2,
      'h1',
      '/api/agent/cc/statusline/test/ready',
      expect.objectContaining({ method: 'POST' }),
    )
  })

  it('stage 1 failure marks later stages skipped', async () => {
    const body = sseBodyFrom([
      { type: 'init', nonce: '__pdx_test_fail1111' },
      { type: 'stage', stage: 1, status: 'failed', error: 'proxy spawn failed: no such executable', elapsed_ms: 5 },
      { type: 'done' },
    ])
    mockFetch.mockResolvedValueOnce({ ok: true, status: 200, body } as unknown as Response)
    mockFetch.mockResolvedValueOnce({ ok: true, status: 200 } as unknown as Response)

    const { result } = renderHook(() => useStatuslineTest('h1'))
    await act(async () => { await result.current.run() })

    expect(result.current.state.stages[1].status).toBe('failed')
    expect(result.current.state.stages[1].error).toContain('proxy spawn failed')
    expect(result.current.state.stages[2].status).toBe('skipped')
    expect(result.current.state.stages[5].status).toBe('skipped')
  })

  it('falls back to stage-1 nonce when daemon does not send init', async () => {
    const nonce = '__pdx_test_legacy111'
    const body = sseBodyFrom([
      { type: 'stage', stage: 1, name: 'Proxy spawned', status: 'passed', elapsed_ms: 12, nonce },
      { type: 'stage', stage: 2, name: 'Proxy → daemon POST received', status: 'passed', elapsed_ms: 8, nonce },
      { type: 'stage', stage: 3, name: 'Daemon → WS broadcast', status: 'passed', elapsed_ms: 3, nonce },
      { type: 'done', nonce },
    ])
    mockFetch.mockResolvedValueOnce({ ok: true, status: 200, body } as unknown as Response)

    const { result } = renderHook(() => useStatuslineTest('h1'))
    await act(async () => {
      const p = result.current.run()
      queueMicrotask(() => {
        useAgentStore.getState().setCcStatus('h1', nonce, { model: { id: 'x' } })
        statuslineTestBus.emit({ nonce, hostId: 'h1', raw: { model: { id: 'x' } } })
      })
      await p
    })

    expect(result.current.state.stages[4].status).toBe('passed')
    expect(result.current.state.stages[5].status).toBe('passed')
    expect(mockFetch).toHaveBeenCalledTimes(1)
  })

  it('ignores bus events for the same nonce from another host', async () => {
    vi.useFakeTimers()
    const nonce = '__pdx_test_crosshost'
    const body = sseBodyFrom([
      { type: 'init', nonce },
      { type: 'stage', stage: 1, name: 'Proxy spawned', status: 'passed', elapsed_ms: 5, nonce },
      { type: 'stage', stage: 2, name: 'Proxy → daemon POST received', status: 'passed', elapsed_ms: 3, nonce },
      { type: 'stage', stage: 3, name: 'Daemon → WS broadcast', status: 'passed', elapsed_ms: 2, nonce },
      { type: 'done', nonce },
    ])
    mockFetch.mockResolvedValueOnce({ ok: true, status: 200, body } as unknown as Response)
    mockFetch.mockResolvedValueOnce({ ok: true, status: 200 } as unknown as Response)

    const { result } = renderHook(() => useStatuslineTest('h1'))
    let runPromise: Promise<void> = Promise.resolve()
    await act(async () => {
      runPromise = result.current.run()
      queueMicrotask(() => {
        useAgentStore.getState().setCcStatus('h2', nonce, { model: { id: 'other-host' } })
        statuslineTestBus.emit({ nonce, hostId: 'h2', raw: { model: { id: 'other-host' } } })
      })
      await vi.advanceTimersByTimeAsync(2100)
      await runPromise
    })

    expect(result.current.state.stages[4].status).toBe('failed')
    expect(result.current.state.stages[5].status).toBe('skipped')
    vi.useRealTimers()
  })

  it('SSE done without WS event → stage 4 fails after grace period', async () => {
    vi.useFakeTimers()
    const nonce = '__pdx_test_bbbb2222'
    const body = sseBodyFrom([
      { type: 'init', nonce },
      { type: 'stage', stage: 1, name: 'Proxy spawned', status: 'passed', elapsed_ms: 5, nonce },
      { type: 'stage', stage: 2, name: 'Proxy → daemon POST received', status: 'passed', elapsed_ms: 3, nonce },
      { type: 'stage', stage: 3, name: 'Daemon → WS broadcast', status: 'passed', elapsed_ms: 2, nonce },
      { type: 'done', nonce },
    ])
    mockFetch.mockResolvedValueOnce({ ok: true, status: 200, body } as unknown as Response)
    mockFetch.mockResolvedValueOnce({ ok: true, status: 200 } as unknown as Response)

    const { result } = renderHook(() => useStatuslineTest('h1'))
    let runPromise: Promise<void> = Promise.resolve()
    await act(async () => {
      runPromise = result.current.run()
      // Advance past the stage-4 grace period (currently 2000ms). The SSE
      // stream completes synchronously in microtasks; then the grace timer
      // fires because no bus event / ccStatus entry arrived.
      await vi.advanceTimersByTimeAsync(2100)
      await runPromise
    })

    expect(result.current.state.stages[1].status).toBe('passed')
    expect(result.current.state.stages[2].status).toBe('passed')
    expect(result.current.state.stages[3].status).toBe('passed')
    expect(result.current.state.stages[4].status).toBe('failed')
    expect(result.current.state.stages[4].error).toMatch(/WS event not received/i)
    expect(result.current.state.stages[5].status).toBe('skipped')
    vi.useRealTimers()
  })

  it('ready ack failure falls back to the legacy flow', async () => {
    const nonce = '__pdx_test_readyfail'
    const body = sseBodyFrom([
      { type: 'init', nonce },
      { type: 'stage', stage: 1, name: 'Proxy spawned', status: 'passed', elapsed_ms: 5, nonce },
      { type: 'stage', stage: 2, name: 'Proxy → daemon POST received', status: 'passed', elapsed_ms: 3, nonce },
      { type: 'stage', stage: 3, name: 'Daemon → WS broadcast', status: 'passed', elapsed_ms: 2, nonce },
      { type: 'done', nonce },
    ])
    mockFetch.mockResolvedValueOnce({ ok: true, status: 200, body } as unknown as Response)
    mockFetch.mockResolvedValueOnce({ ok: false, status: 500 } as unknown as Response)

    const { result } = renderHook(() => useStatuslineTest('h1'))
    await act(async () => {
      const p = result.current.run()
      queueMicrotask(() => {
        useAgentStore.getState().setCcStatus('h1', nonce, { model: { id: 'x' } })
        statuslineTestBus.emit({ nonce, hostId: 'h1', raw: { model: { id: 'x' } } })
      })
      await p
    })

    expect(result.current.state.stages[1].status).toBe('passed')
    expect(result.current.state.stages[4].status).toBe('passed')
    expect(result.current.state.stages[5].status).toBe('passed')
  })

  it('overall timeout marks incomplete stages failed', async () => {
    vi.useFakeTimers()
    const pendingBody = new ReadableStream<Uint8Array>({ start() { /* never writes */ } })
    mockFetch.mockResolvedValueOnce({ ok: true, status: 200, body: pendingBody } as unknown as Response)

    const { result } = renderHook(() => useStatuslineTest('h1'))
    let runPromise: Promise<void> = Promise.resolve()
    await act(async () => {
      runPromise = result.current.run()
      // Advance past OVERALL_TIMEOUT_MS (currently 10000ms).
      await vi.advanceTimersByTimeAsync(10100)
      await runPromise
    })
    expect(result.current.state.stages[1].status).toBe('failed')
    expect(result.current.state.stages[1].error).toMatch(/timeout/i)
    vi.useRealTimers()
  })
})
