import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useExecutionActions } from './useExecutionActions'
import { useExecutionStore } from '../stores/useExecutionStore'
import { NexApiError } from '../lib/nex/types'
import * as api from '../lib/nex/nex-api'

vi.mock('../lib/nex/nex-api', () => ({ sendMessage: vi.fn(), interruptExecution: vi.fn(), terminateExecution: vi.fn() }))

const H = 'h', E = 'exc_1', KEY = 'h:exc_1'
type SendResult = { turn_id: string; delivery: 'delivered' | 'queued' }
const ensureLease = vi.fn(), touch = vi.fn(), forget = vi.fn()
const st = () => useExecutionStore.getState().executions[KEY]

function deferredSend() {
  let resolve!: (v: SendResult) => void
  let reject!: (e: unknown) => void
  vi.mocked(api.sendMessage).mockReturnValueOnce(new Promise<SendResult>((res, rej) => { resolve = res; reject = rej }))
  return { resolve: (v: SendResult) => resolve(v), reject: (e: unknown) => reject(e) }
}

const stall = () => act(() => {
  useExecutionStore.getState().applyEvents(H, E, [
    { seq: 1, execution_id: E, kind: 'execution.turn_stalled', payload: { turn_id: 'tA' }, created_at: 0 },
  ])
})

beforeEach(() => {
  useExecutionStore.setState({ executions: {} })
  ensureLease.mockReset().mockResolvedValue('ls_1'); touch.mockReset(); forget.mockReset()
  vi.mocked(api.sendMessage).mockReset()
})

describe('useExecutionActions — a superseded send never touches the newer one', () => {
  async function sendAThenStallThenB() {
    const { result } = renderHook(() => useExecutionActions(H, E, { ensureLease, touch, forget }))
    const a = deferredSend()
    await act(async () => { void result.current.handleSend('A'); await Promise.resolve() })
    await vi.waitFor(() => expect(api.sendMessage).toHaveBeenCalledTimes(1))
    stall()
    expect(st().pendingSend).toBe(false)
    const b = deferredSend()
    await act(async () => { void result.current.handleSend('B'); await Promise.resolve() })
    await vi.waitFor(() => expect(api.sendMessage).toHaveBeenCalledTimes(2))
    expect(st().pendingLocal).toEqual({ text: 'B', delivery: null })
    return { result, a, b }
  }

  it('A resolving late leaves B\'s bubble, lock and lastTurn alone', async () => {
    const { a } = await sendAThenStallThenB()
    await act(async () => { a.resolve({ turn_id: 'tA', delivery: 'queued' }); await Promise.resolve(); await Promise.resolve() })
    expect(st().pendingLocal).toEqual({ text: 'B', delivery: null })
    expect(st().pendingSend).toBe(true)
    expect(st().lastTurn?.turnId).not.toBe('tA')
  })

  it('A rejecting late does not withdraw B, unlock the input, raise an error or restore A\'s draft', async () => {
    const { result, a } = await sendAThenStallThenB()
    await act(async () => { a.reject(new NexApiError(400, 'invalid_text', 'late')); await Promise.resolve(); await Promise.resolve() })
    expect(st().pendingLocal).toEqual({ text: 'B', delivery: null })
    expect(st().pendingSend).toBe(true)
    expect(st().sendError).toBeNull()
    expect(result.current.draft).toBeNull()
  })

  it('B still completes normally after A was superseded', async () => {
    const { a, b } = await sendAThenStallThenB()
    await act(async () => { a.resolve({ turn_id: 'tA', delivery: 'delivered' }); await Promise.resolve() })
    await act(async () => { b.resolve({ turn_id: 'tB', delivery: 'queued' }); await Promise.resolve(); await Promise.resolve() })
    expect(st().pendingLocal).toEqual({ text: 'B', delivery: 'queued' })
    expect(st().lastTurn).toEqual({ turnId: 'tB', delivery: 'queued' })
  })
})

describe('useExecutionActions — actionPending', () => {
  it('is true while an interrupt request is in flight and false again afterwards', async () => {
    let resolveInterrupt!: (v: { turn_id: string; state: string }) => void
    vi.mocked(api.interruptExecution).mockReset().mockReturnValueOnce(new Promise((res) => { resolveInterrupt = res }))
    const { result } = renderHook(() => useExecutionActions(H, E, { ensureLease, touch, forget }))
    expect(result.current.actionPending).toBe(false)
    await act(async () => { void result.current.handleInterrupt(); await Promise.resolve() })
    await vi.waitFor(() => expect(result.current.actionPending).toBe(true))
    await act(async () => { resolveInterrupt({ turn_id: 't1', state: 'idle' }); await Promise.resolve() })
    await vi.waitFor(() => expect(result.current.actionPending).toBe(false))
  })

  it('clears actionPending when a terminate request rejects', async () => {
    vi.mocked(api.terminateExecution).mockReset().mockRejectedValueOnce(new Error('boom'))
    const { result } = renderHook(() => useExecutionActions(H, E, { ensureLease, touch, forget }))
    await act(async () => { await result.current.handleTerminate() })
    expect(result.current.actionPending).toBe(false)
  })
})
