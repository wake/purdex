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
      { type: 'stage', stage: 1, name: 'Proxy spawned', status: 'passed', elapsed_ms: 12, nonce },
      { type: 'stage', stage: 2, name: 'Proxy → daemon POST received', status: 'passed', elapsed_ms: 8, nonce },
      { type: 'stage', stage: 3, name: 'Daemon → WS broadcast', status: 'passed', elapsed_ms: 3, nonce },
      { type: 'done', nonce },
    ])
    mockFetch.mockResolvedValueOnce({ ok: true, status: 200, body } as unknown as Response)

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
  })

  it('stage 1 failure marks later stages skipped', async () => {
    const body = sseBodyFrom([
      { type: 'stage', stage: 1, status: 'failed', error: 'proxy spawn failed: no such executable', elapsed_ms: 5 },
      { type: 'done' },
    ])
    mockFetch.mockResolvedValueOnce({ ok: true, status: 200, body } as unknown as Response)

    const { result } = renderHook(() => useStatuslineTest('h1'))
    await act(async () => { await result.current.run() })

    expect(result.current.state.stages[1].status).toBe('failed')
    expect(result.current.state.stages[1].error).toContain('proxy spawn failed')
    expect(result.current.state.stages[2].status).toBe('skipped')
    expect(result.current.state.stages[5].status).toBe('skipped')
  })

  it('overall 5s timeout marks incomplete stages failed', async () => {
    vi.useFakeTimers()
    const pendingBody = new ReadableStream<Uint8Array>({ start() { /* never writes */ } })
    mockFetch.mockResolvedValueOnce({ ok: true, status: 200, body: pendingBody } as unknown as Response)

    const { result } = renderHook(() => useStatuslineTest('h1'))
    let runPromise: Promise<void> = Promise.resolve()
    await act(async () => {
      runPromise = result.current.run()
      await vi.advanceTimersByTimeAsync(5100)
      await runPromise
    })
    expect(result.current.state.stages[1].status).toBe('failed')
    expect(result.current.state.stages[1].error).toMatch(/timeout/i)
    vi.useRealTimers()
  })
})
