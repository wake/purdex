import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useExecutionActions } from './useExecutionActions'
import { useExecutionStore } from '../stores/useExecutionStore'
import { NexApiError } from '../lib/nex/types'
import * as api from '../lib/nex/nex-api'

vi.mock('../lib/nex/nex-api', () => ({ sendMessage: vi.fn(), interruptExecution: vi.fn() }))

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
})

describe('useExecutionActions — restoring the draft after a failed send', () => {
  it('a failed typed send still restores it as the draft', async () => {
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(400, 'invalid_text', 'too long'))
    const { result } = renderHook(() => useExecutionActions(H, E, { ensureLease, touch, forget }))
    await act(async () => { await result.current.handleSend('typed') })
    expect(result.current.draft).toBe('typed')
    expect(st().sendError?.code).toBe('invalid_text')
  })

  // A quick reply (R3 T2.1, plan review #1): WorkerInput is keyed on the
  // draft, so any change to it remounts the input and wipes what is typed.
  it('restoreDraft: false only reports the error and never touches the draft', async () => {
    const { result } = renderHook(() => useExecutionActions(H, E, { ensureLease, touch, forget }))
    // A draft already restored by an earlier typed failure …
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(400, 'invalid_text', 'too long'))
    await act(async () => { await result.current.handleSend('typed') })
    expect(result.current.draft).toBe('typed')
    // … is neither replaced nor cleared by a quick reply, failed or not.
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(400, 'invalid_text', 'nope'))
    await act(async () => { await result.current.handleSend('continue', { restoreDraft: false }) })
    expect(result.current.draft).toBe('typed')
    expect(st().sendError?.message).toBe('nope')
    expect(st().pendingSend).toBe(false)
    expect(st().pendingLocal).toBeNull()
    vi.mocked(api.sendMessage).mockResolvedValueOnce({ turn_id: 't2', delivery: 'delivered' })
    await act(async () => { await result.current.handleSend('continue', { restoreDraft: false }) })
    expect(result.current.draft).toBe('typed')
    expect(st().sendError).toBeNull()
  })
})

describe('useExecutionActions — handleSend reports whether the send went through', () => {
  it('resolves true when sendMessage resolves', async () => {
    vi.mocked(api.sendMessage).mockResolvedValueOnce({ turn_id: 't1', delivery: 'delivered' })
    const { result } = renderHook(() => useExecutionActions(H, E, { ensureLease, touch, forget }))
    let ok: boolean | undefined
    await act(async () => { ok = await result.current.handleSend('hi') })
    expect(ok).toBe(true)
  })

  it('resolves false when the send fails', async () => {
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(400, 'invalid_text', 'too long'))
    const { result } = renderHook(() => useExecutionActions(H, E, { ensureLease, touch, forget }))
    let ok: boolean | undefined
    await act(async () => { ok = await result.current.handleSend('hi') })
    expect(ok).toBe(false)
  })

  it('resolves false for the re-entrant no-op while a send is pending', async () => {
    const d = deferredSend()
    const { result } = renderHook(() => useExecutionActions(H, E, { ensureLease, touch, forget }))
    let first: Promise<boolean> | undefined
    await act(async () => { first = result.current.handleSend('A'); await Promise.resolve() })
    let second: boolean | undefined
    await act(async () => { second = await result.current.handleSend('B') })
    expect(second).toBe(false)
    expect(api.sendMessage).toHaveBeenCalledTimes(1)
    d.resolve({ turn_id: 't1', delivery: 'delivered' })
    let firstOk: boolean | undefined
    await act(async () => { firstOk = await first })
    expect(firstOk).toBe(true)
  })

  it('resolves true for an accepted send superseded before it settled — the caller clears its own chips', async () => {
    const { result } = renderHook(() => useExecutionActions(H, E, { ensureLease, touch, forget }))
    const a = deferredSend()
    let pa: Promise<boolean> | undefined
    await act(async () => { pa = result.current.handleSend('A'); await Promise.resolve() })
    await vi.waitFor(() => expect(api.sendMessage).toHaveBeenCalledTimes(1))
    stall()
    deferredSend()
    await act(async () => { void result.current.handleSend('B'); await Promise.resolve() })
    let aOk: boolean | undefined
    await act(async () => { a.resolve({ turn_id: 'tA', delivery: 'queued' }); aOk = await pa })
    expect(aOk).toBe(true)
  })

  it('resolves false for a send superseded before it settled, when it then fails', async () => {
    const { result } = renderHook(() => useExecutionActions(H, E, { ensureLease, touch, forget }))
    const a = deferredSend()
    let pa: Promise<boolean> | undefined
    await act(async () => { pa = result.current.handleSend('A'); await Promise.resolve() })
    await vi.waitFor(() => expect(api.sendMessage).toHaveBeenCalledTimes(1))
    stall()
    deferredSend()
    await act(async () => { void result.current.handleSend('B'); await Promise.resolve() })
    let aOk: boolean | undefined
    await act(async () => { a.reject(new NexApiError(400, 'invalid_text', 'late')); aOk = await pa })
    expect(aOk).toBe(false)
  })

  it('draftText restores only the typed text, not the composed message', async () => {
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(400, 'invalid_text', 'too long'))
    const { result } = renderHook(() => useExecutionActions(H, E, { ensureLease, touch, forget }))
    await act(async () => { await result.current.handleSend('typed\n\n[file: /a/b.txt]', { draftText: 'typed' }) })
    expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', 'typed\n\n[file: /a/b.txt]')
    expect(result.current.draft).toBe('typed')
  })
})

describe('useExecutionActions — native image attachments (phase E)', () => {
  const attachments = [{ type: 'image' as const, media_type: 'image/png', data: 'AAAA' }]
  const previews = [{ previewUrl: 'blob:p1', media_type: 'image/png' }]

  it('forwards attachments and keeps the same previews array on the optimistic line after the POST', async () => {
    const d = deferredSend()
    const { result } = renderHook(() => useExecutionActions(H, E, { ensureLease, touch, forget }))
    let done!: Promise<boolean>
    await act(async () => { done = result.current.handleSend('', { attachments, previews }); await Promise.resolve() })
    await vi.waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', '', attachments))
    expect(st().pendingLocal?.attachments).toBe(previews)
    await act(async () => { d.resolve({ turn_id: 't1', delivery: 'queued' }); await done })
    expect(st().pendingLocal).toEqual({ text: '', delivery: 'queued', attachments: previews })
    expect(st().pendingLocal?.attachments).toBe(previews)
  })

  it('a re-entrant no-op send revokes the previews it was handed (the store never saw them)', async () => {
    const revoke = vi.fn()
    const orig = URL.revokeObjectURL
    Object.assign(URL, { revokeObjectURL: revoke })
    try {
      useExecutionStore.getState().setPendingSend(H, E, true)
      useExecutionStore.getState().setSendLocked(H, E, true)
      const { result } = renderHook(() => useExecutionActions(H, E, { ensureLease, touch, forget }))
      let ok = true
      await act(async () => { ok = await result.current.handleSend('t', { attachments, previews: [{ previewUrl: 'blob:x', media_type: 'image/png' }] }) })
      expect(ok).toBe(false)
      expect(revoke.mock.calls).toEqual([['blob:x']])
      expect(api.sendMessage).not.toHaveBeenCalled()
    } finally {
      Object.assign(URL, { revokeObjectURL: orig })
    }
  })

  it('records the per-image attachment_index on the send error', async () => {
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(400, 'attachment_too_large', 'big', undefined, 1))
    const { result } = renderHook(() => useExecutionActions(H, E, { ensureLease, touch, forget }))
    let ok = true
    await act(async () => { ok = await result.current.handleSend('t', { attachments, previews }) })
    expect(ok).toBe(false)
    expect(st().sendError).toMatchObject({ code: 'attachment_too_large', attachmentIndex: 1 })
    expect(st().pendingLocal).toBeNull()
  })
})
