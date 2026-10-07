// spa/src/hooks/usePermissionAnswer.test.ts — permission channel plan Task 9 (Review Focus #3 / #4).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { usePermissionAnswer } from './usePermissionAnswer'
import { useExecutionLease } from './useExecutionLease'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useHostStore } from '../stores/useHostStore'
import { useExecutionStore } from '../stores/useExecutionStore'
import { NexApiError } from '../lib/nex/types'
import { clearAllPermissionCards, isPermissionCardClosed, permissionCardKey } from '../lib/nex/permission-card-memory'
import * as api from '../lib/nex/nex-api'

vi.mock('../lib/nex/nex-api', () => ({
  answerPermission: vi.fn(), attachControl: vi.fn(), renewLease: vi.fn(), releaseLease: vi.fn(),
}))
vi.mock('../lib/nex/lease-ttl', () => ({ getLeaseTtlSeconds: vi.fn(async () => 30), DEFAULT_LEASE_TTL_S: 120 }))

const H = 'h', E = 'exc_1'
const req = { requestId: 'req_a' }
// The real mlab shape (Task 6 fixture): the answer route already carries /api/nex.
const caps = {
  phase: 'ready', sandbox_profiles: ['handoff_ask'],
  permissions: { profiles: ['handoff_ask'], answer: { method: 'POST', path: '/api/nex/v1/executions/{id}/permissions/{request_id}' }, timeout: { max_s: 86400 } },
}
const err = (status: number, code: string, field?: string) => new NexApiError(status, code, `nexen says ${code}`, undefined, undefined, field)

describe('usePermissionAnswer', () => {
  const ensureLease = vi.fn(), forget = vi.fn(), touch = vi.fn()
  const lease = { ensureLease, forget, touch }
  const render = () => renderHook(() => usePermissionAnswer(H, E, lease))

  beforeEach(() => {
    ensureLease.mockReset().mockResolvedValue('ls_1')
    forget.mockReset()
    touch.mockReset()
    vi.mocked(api.answerPermission).mockReset().mockResolvedValue({ request_id: 'req_a', outcome: 'allowed' })
    useNexHostStore.setState({ byHost: { [H]: { phase: 'ready', capabilities: caps } } as never })
    // "Closed by this pane" lives in module memory now (A2): one test's close must not seed the next.
    clearAllPermissionCards()
  })

  it('allow: ensureLease, then exactly one answer with the pane lease and the host capabilities; the request closes', async () => {
    const { result } = render()
    await act(async () => { await result.current.answer(req, 'allow') })
    expect(ensureLease).toHaveBeenCalledTimes(1)
    expect(touch).toHaveBeenCalled()
    expect(api.answerPermission).toHaveBeenCalledTimes(1)
    const [h, e, id, body, c] = vi.mocked(api.answerPermission).mock.calls[0]
    expect([h, e, id]).toEqual([H, E, 'req_a'])
    expect(body).toEqual({ decision: 'allow', leaseId: 'ls_1' })
    expect(c).toBe(useNexHostStore.getState().byHost[H]!.capabilities)
    expect(result.current.closed.has('req_a')).toBe(true)
    expect(result.current.error).toBeUndefined()
    expect(result.current.busy).toBe(false)
  })

  it('allow never carries a message', async () => {
    const { result } = render()
    await act(async () => { await result.current.answer(req, 'allow', 'ignored') })
    expect(vi.mocked(api.answerPermission).mock.calls[0][3]).toEqual({ decision: 'allow', leaseId: 'ls_1' })
  })

  it('deny carries the note as message; an empty or blank note sends none (Nexen\'s default applies)', async () => {
    const { result } = render()
    await act(async () => { await result.current.answer(req, 'deny', '  不要動 prod ') })
    expect(vi.mocked(api.answerPermission).mock.calls[0][3]).toEqual({ decision: 'deny', message: '不要動 prod', leaseId: 'ls_1' })
    // One request per note: a request this pane closed is never answered again, not even by a fresh hook (A2).
    for (const [i, note] of [undefined, '', '   '].entries()) {
      vi.mocked(api.answerPermission).mockClear()
      const other = render()
      await act(async () => { await other.result.current.answer({ requestId: `req_b${i}` }, 'deny', note) })
      expect(vi.mocked(api.answerPermission).mock.calls[0][3]).toEqual({ decision: 'deny', leaseId: 'ls_1' })
    }
  })

  it('409 permission_not_pending closes quietly: no error, no retry', async () => {
    vi.mocked(api.answerPermission).mockRejectedValueOnce(err(409, 'permission_not_pending'))
    const { result } = render()
    await act(async () => { await result.current.answer(req, 'allow') })
    expect(api.answerPermission).toHaveBeenCalledTimes(1)
    expect(result.current.closed.has('req_a')).toBe(true)
    expect(result.current.error).toBeUndefined()
    expect(forget).not.toHaveBeenCalled()
  })

  it.each(['lease_expired', 'lease_mismatch', 'lease_required'])('%s → forget, one re-attach, the SAME request answered once more', async (code) => {
    vi.mocked(api.answerPermission).mockRejectedValueOnce(err(409, code))
    ensureLease.mockResolvedValueOnce('ls_1').mockResolvedValueOnce('ls_2')
    const { result } = render()
    await act(async () => { await result.current.answer(req, 'deny', 'no') })
    expect(forget).toHaveBeenCalledTimes(1)
    expect(ensureLease).toHaveBeenCalledTimes(2)
    expect(api.answerPermission).toHaveBeenCalledTimes(2)
    const calls = vi.mocked(api.answerPermission).mock.calls
    expect(calls.map((c) => c[2])).toEqual(['req_a', 'req_a'])
    expect(calls[1][3]).toEqual({ decision: 'deny', message: 'no', leaseId: 'ls_2' })
    expect(result.current.closed.has('req_a')).toBe(true)
    expect(result.current.error).toBeUndefined()
  })

  it('two lease failures → the error line (no third attempt), and the dead lease is dropped again', async () => {
    vi.mocked(api.answerPermission).mockRejectedValueOnce(err(409, 'lease_expired')).mockRejectedValueOnce(err(409, 'lease_mismatch'))
    const { result } = render()
    await act(async () => { await result.current.answer(req, 'allow') })
    expect(api.answerPermission).toHaveBeenCalledTimes(2)
    expect(forget).toHaveBeenCalledTimes(2)
    expect(result.current.error).toMatchObject({ requestId: 'req_a', code: 'lease_mismatch' })
    expect(result.current.closed.has('req_a')).toBe(false)
  })

  it('lease_expired, then permission_not_pending on the retry → quiet close, no error', async () => {
    vi.mocked(api.answerPermission).mockRejectedValueOnce(err(409, 'lease_expired')).mockRejectedValueOnce(err(409, 'permission_not_pending'))
    const { result } = render()
    await act(async () => { await result.current.answer(req, 'allow') })
    expect(api.answerPermission).toHaveBeenCalledTimes(2)
    expect(result.current.closed.has('req_a')).toBe(true)
    expect(result.current.error).toBeUndefined()
  })

  it('permission_not_found is NOT quiet: an error line and a console warning; the card stays', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.mocked(api.answerPermission).mockRejectedValueOnce(err(404, 'permission_not_found'))
    const { result } = render()
    await act(async () => { await result.current.answer(req, 'allow') })
    expect(result.current.error).toMatchObject({ requestId: 'req_a', code: 'permission_not_found' })
    expect(result.current.closed.has('req_a')).toBe(false)
    expect(warn).toHaveBeenCalledTimes(1)
    expect(String(warn.mock.calls[0][0])).toContain('req_a')
    warn.mockRestore()
  })

  it('invalid_permission_answer keeps the request open and reports the field', async () => {
    vi.mocked(api.answerPermission).mockRejectedValueOnce(err(400, 'invalid_permission_answer', 'message'))
    const { result } = render()
    await act(async () => { await result.current.answer(req, 'deny', 'x') })
    expect(result.current.error).toEqual({ requestId: 'req_a', code: 'invalid_permission_answer', message: 'nexen says invalid_permission_answer', field: 'message' })
    expect(result.current.closed.has('req_a')).toBe(false)
  })

  it('lease_held from the attach is silent here (the pane\'s lease notice says it); nothing is sent', async () => {
    ensureLease.mockRejectedValueOnce(err(409, 'lease_held'))
    const { result } = render()
    await act(async () => { await result.current.answer(req, 'allow') })
    expect(api.answerPermission).not.toHaveBeenCalled()
    expect(result.current.error).toBeUndefined()
    expect(result.current.closed.has('req_a')).toBe(false)
  })

  it('any other failure (network) shows the error line and leaves the card to retry', async () => {
    vi.mocked(api.answerPermission).mockRejectedValueOnce(err(0, 'network'))
    const { result } = render()
    await act(async () => { await result.current.answer(req, 'allow') })
    expect(result.current.error).toMatchObject({ code: 'network' })
    vi.mocked(api.answerPermission).mockResolvedValueOnce({ request_id: 'req_a', outcome: 'allowed' })
    await act(async () => { await result.current.answer(req, 'allow') })
    expect(result.current.error).toBeUndefined()
    expect(result.current.closed.has('req_a')).toBe(true)
  })

  it('a double click sends one answer: busy while in flight, the second call is a no-op', async () => {
    let resolveAnswer!: (v: { request_id: string; outcome: 'allowed' }) => void
    vi.mocked(api.answerPermission).mockImplementationOnce(() => new Promise((r) => { resolveAnswer = r }))
    const { result } = render()
    let first!: Promise<void>
    let second!: Promise<void>
    act(() => {
      first = result.current.answer(req, 'allow')
      second = result.current.answer(req, 'allow')
    })
    expect(result.current.busy).toBe(true)
    await act(async () => { await Promise.resolve() })
    await act(async () => { resolveAnswer({ request_id: 'req_a', outcome: 'allowed' }); await first; await second })
    expect(api.answerPermission).toHaveBeenCalledTimes(1)
    expect(result.current.busy).toBe(false)
  })

  it('a request already closed by this pane is never answered again', async () => {
    const { result } = render()
    await act(async () => { await result.current.answer(req, 'allow') })
    await act(async () => { await result.current.answer(req, 'deny') })
    expect(api.answerPermission).toHaveBeenCalledTimes(1)
  })

  // A2: the pane unmounts on a tab switch; the hook that comes back must still know what it closed.
  it.each([
    ['answered', () => vi.mocked(api.answerPermission).mockResolvedValueOnce({ request_id: 'req_a', outcome: 'allowed' })],
    ['found already ended (409)', () => vi.mocked(api.answerPermission).mockRejectedValueOnce(err(409, 'permission_not_pending'))],
  ])('a request %s before a remount stays closed in the remounted hook and is never answered again', async (_label, arrange) => {
    arrange()
    const first = render()
    await act(async () => { await first.result.current.answer(req, 'allow') })
    first.unmount()
    const second = render()
    expect(second.result.current.closed.has('req_a')).toBe(true)
    await act(async () => { await second.result.current.answer(req, 'deny') })
    expect(api.answerPermission).toHaveBeenCalledTimes(1)
    expect(isPermissionCardClosed(permissionCardKey(H, E, 'req_a'))).toBe(true)
  })

  it('a close is this execution\'s and this host\'s only: the same request id elsewhere is still answered', async () => {
    const first = render()
    await act(async () => { await first.result.current.answer(req, 'allow') })
    useNexHostStore.setState({ byHost: { [H]: { phase: 'ready', capabilities: caps }, h2: { phase: 'ready', capabilities: caps } } as never })
    for (const [h, e] of [[H, 'exc_2'], ['h2', E]] as const) {
      const other = renderHook(() => usePermissionAnswer(h, e, lease))
      expect(other.result.current.closed.has('req_a')).toBe(false)
      await act(async () => { await other.result.current.answer(req, 'allow') })
    }
    expect(vi.mocked(api.answerPermission).mock.calls.map((c) => [c[0], c[1], c[2]])).toEqual([[H, E, 'req_a'], [H, 'exc_2', 'req_a'], ['h2', E, 'req_a']])
  })
})

describe('usePermissionAnswer with the real lease (hold)', () => {
  const KEY = 'h:exc_1'
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-07T00:00:00Z'))
    useExecutionStore.setState({ executions: {} })
    useHostStore.setState({ hosts: { [H]: { id: H, name: 'H', ip: '1', port: 1 } } as never, hostOrder: [H], activeHostId: H, runtime: {} })
    useNexHostStore.setState({ byHost: { [H]: { phase: 'ready', capabilities: caps } } as never })
    vi.mocked(api.attachControl).mockReset().mockResolvedValueOnce({ mode: 'control', lease_id: 'ls_1', expires_at: Date.now() + 30_000 })
    vi.mocked(api.renewLease).mockReset().mockImplementation(async () => ({ mode: 'control', lease_id: 'ls_1', expires_at: Date.now() + 30_000 }))
    vi.mocked(api.releaseLease).mockReset().mockResolvedValue(undefined)
    vi.mocked(api.answerPermission).mockReset().mockResolvedValue({ request_id: 'req_a', outcome: 'allowed' })
    clearAllPermissionCards()
  })
  afterEach(() => { vi.useRealTimers() })

  it('a renew failing during the hold drops the lease; the next answer re-attaches transparently and sends exactly one answer', async () => {
    const { result } = renderHook(() => {
      const lease = useExecutionLease(H, E, { hold: true })
      return { lease, perm: usePermissionAnswer(H, E, lease) }
    })
    await act(async () => { await result.current.lease.ensureLease() })
    vi.mocked(api.renewLease).mockRejectedValueOnce(new NexApiError(409, 'lease_mismatch', 'taken'))
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(useExecutionStore.getState().executions[KEY]?.lease ?? null).toBeNull()
    vi.mocked(api.attachControl).mockResolvedValueOnce({ mode: 'control', lease_id: 'ls_2', expires_at: Date.now() + 30_000 })
    await act(async () => { await result.current.perm.answer(req, 'allow') })
    expect(api.attachControl).toHaveBeenCalledTimes(2)
    expect(api.answerPermission).toHaveBeenCalledTimes(1)
    expect(vi.mocked(api.answerPermission).mock.calls[0][3]).toEqual({ decision: 'allow', leaseId: 'ls_2' })
    expect(result.current.perm.closed.has('req_a')).toBe(true)
  })
})
