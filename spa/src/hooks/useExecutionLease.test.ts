import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useExecutionLease, LEASE_IDLE_MULTIPLIER } from './useExecutionLease'
import { useExecutionStore } from '../stores/useExecutionStore'
import { useHostStore } from '../stores/useHostStore'
import { NexApiError } from '../lib/nex/types'
import * as api from '../lib/nex/nex-api'

vi.mock('../lib/nex/nex-api', () => ({
  attachControl: vi.fn(), renewLease: vi.fn(), releaseLease: vi.fn(),
}))
vi.mock('../lib/nex/lease-ttl', () => ({ getLeaseTtlSeconds: vi.fn(async () => 30), DEFAULT_LEASE_TTL_S: 120 }))

const H = 'h', E = 'exc_1', KEY = 'h:exc_1'
const lease = () => useExecutionStore.getState().executions[KEY]?.lease ?? null

describe('useExecutionLease', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-15T00:00:00Z'))
    useExecutionStore.setState({ executions: {} })
    useHostStore.setState({ hosts: { [H]: { id: H, name: 'H', ip: '1', port: 1 } } as never, hostOrder: [H], activeHostId: H, runtime: {} })
    vi.mocked(api.attachControl).mockReset().mockResolvedValue({ mode: 'control', lease_id: 'ls_1', expires_at: Date.now() + 30_000 })
    vi.mocked(api.renewLease).mockReset().mockImplementation(async () => ({ mode: 'control', lease_id: 'ls_1', expires_at: Date.now() + 30_000 }))
    vi.mocked(api.releaseLease).mockReset().mockResolvedValue(undefined)
  })
  afterEach(() => { vi.useRealTimers() })

  it('ensureLease attaches lazily once and shares the in-flight promise', async () => {
    const { result } = renderHook(() => useExecutionLease(H, E))
    let ids: string[] = []
    await act(async () => { ids = await Promise.all([result.current.ensureLease(), result.current.ensureLease()]) })
    expect(ids).toEqual(['ls_1', 'ls_1'])
    expect(api.attachControl).toHaveBeenCalledTimes(1)
    expect(lease()).toEqual({ leaseId: 'ls_1', expiresAt: expect.any(Number) })
  })

  it('renews at ttl/3 while active and keeps the lease across three cycles (I3)', async () => {
    const { result } = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await result.current.ensureLease() })
    for (let i = 0; i < 3; i++) {
      act(() => { result.current.touch() })
      await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    }
    expect(api.renewLease).toHaveBeenCalledTimes(3)
    expect(lease()?.leaseId).toBe('ls_1')
  })

  it('stops renewing after 2×ttl without activity and re-acquires on the next ensureLease (I3)', async () => {
    const { result } = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await result.current.ensureLease() })
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000 * LEASE_IDLE_MULTIPLIER + 10_000) })
    const renewsWhileIdle = vi.mocked(api.renewLease).mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
    expect(vi.mocked(api.renewLease).mock.calls.length).toBe(renewsWhileIdle) // no more renews once idle
    vi.mocked(api.attachControl).mockResolvedValueOnce({ mode: 'control', lease_id: 'ls_2', expires_at: Date.now() + 30_000 })
    let id = ''
    await act(async () => { id = await result.current.ensureLease() })
    expect(id).toBe('ls_2')
  })

  it('surfaces lease_held with the holder from the summary and rethrows', async () => {
    useExecutionStore.getState().setSummary(H, E, { id: E, lease: { principal_id: 'pdx:mlab/t-other', expires_at: 1 } } as never)
    vi.mocked(api.attachControl).mockRejectedValueOnce(new NexApiError(409, 'lease_held', 'held'))
    const { result } = renderHook(() => useExecutionLease(H, E))
    await expect(act(async () => { await result.current.ensureLease() })).rejects.toMatchObject({ code: 'lease_held' })
    expect(useExecutionStore.getState().executions[KEY].leaseError).toEqual({ code: 'lease_held', heldBy: 'pdx:mlab/t-other' })
  })

  it('drops the local lease silently when renew says lease_expired', async () => {
    const { result } = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await result.current.ensureLease() })
    vi.mocked(api.renewLease).mockRejectedValueOnce(new NexApiError(409, 'lease_expired', 'gone'))
    act(() => { result.current.touch() })
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(lease()).toBeNull()
  })

  it('forget() clears the lease without a network call and the next ensureLease attaches anew', async () => {
    const { result } = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await result.current.ensureLease() })
    expect(lease()).not.toBeNull()

    act(() => { result.current.forget() })
    expect(lease()).toBeNull()
    expect(api.releaseLease).not.toHaveBeenCalled()

    // No more renew ticks after forget (the timer stopped).
    act(() => { result.current.touch() })
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(api.renewLease).not.toHaveBeenCalled()

    vi.mocked(api.attachControl).mockResolvedValueOnce({ mode: 'control', lease_id: 'ls_2', expires_at: Date.now() + 30_000 })
    let id = ''
    await act(async () => { id = await result.current.ensureLease() })
    expect(id).toBe('ls_2')
  })

  it('a renew that resolves after release() cannot write the lease back', async () => {
    const { result } = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await result.current.ensureLease() })
    let resolveRenew!: (v: { mode: 'control'; lease_id: string; expires_at: number }) => void
    vi.mocked(api.renewLease).mockImplementationOnce(() => new Promise((r) => { resolveRenew = r }))
    act(() => { result.current.touch() })
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) }) // renew in flight
    await act(async () => { await result.current.release() })
    expect(lease()).toBeNull()
    await act(async () => { resolveRenew({ mode: 'control', lease_id: 'ls_1', expires_at: Date.now() + 30_000 }) })
    expect(lease()).toBeNull()
  })

  it('host removal stops the renew timer and drops the local lease without a release call (I13)', async () => {
    useHostStore.setState({ hosts: { [H]: { id: H, name: 'H', ip: '1', port: 1 } } as never, hostOrder: [H], activeHostId: H, runtime: {} })
    const { result } = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await result.current.ensureLease() })
    act(() => { useHostStore.setState({ hosts: {}, hostOrder: [] }) })
    expect(lease()).toBeNull()
    act(() => { result.current.touch() })
    await act(async () => { await vi.advanceTimersByTimeAsync(60_000) })
    expect(api.renewLease).not.toHaveBeenCalled()
    expect(api.releaseLease).not.toHaveBeenCalled()
  })

  it('ignores a stale renew success for an id a newer attach already replaced (identity guard)', async () => {
    const { result } = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await result.current.ensureLease() }) // attaches ls_1
    let resolveRenew!: (v: { mode: 'control'; lease_id: string; expires_at: number }) => void
    vi.mocked(api.renewLease).mockImplementationOnce(() => new Promise((r) => { resolveRenew = r }))
    act(() => { result.current.touch() })
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) }) // renew(ls_1) now in flight, hung

    // Force a fresh attach without going through release(): make the current
    // lease look like it's about to expire.
    act(() => { useExecutionStore.getState().setLease(H, E, { leaseId: 'ls_1', expiresAt: Date.now() + 1000 }) })
    vi.mocked(api.attachControl).mockResolvedValueOnce({ mode: 'control', lease_id: 'ls_2', expires_at: Date.now() + 30_000 })
    let id2 = ''
    await act(async () => { id2 = await result.current.ensureLease() })
    expect(id2).toBe('ls_2')
    expect(lease()?.leaseId).toBe('ls_2')

    // The stale renew(ls_1) resolves late — must not overwrite ls_2.
    await act(async () => { resolveRenew({ mode: 'control', lease_id: 'ls_1', expires_at: Date.now() + 30_000 }) })
    expect(lease()?.leaseId).toBe('ls_2')
  })

  it('ignores a stale lease_mismatch/lease_expired for an id a newer attach already replaced, and keeps renewing the current lease (identity guard)', async () => {
    const { result } = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await result.current.ensureLease() }) // attaches ls_1
    let rejectRenew!: (e: unknown) => void
    vi.mocked(api.renewLease).mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectRenew = reject }))
    act(() => { result.current.touch() })
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) }) // renew(ls_1) now in flight, hung

    act(() => { useExecutionStore.getState().setLease(H, E, { leaseId: 'ls_1', expiresAt: Date.now() + 1000 }) })
    vi.mocked(api.attachControl).mockResolvedValueOnce({ mode: 'control', lease_id: 'ls_2', expires_at: Date.now() + 30_000 })
    await act(async () => { await result.current.ensureLease() })
    expect(lease()?.leaseId).toBe('ls_2')

    // The stale renew(ls_1) rejects with lease_mismatch late — must not wipe
    // ls_2 or stop its timer.
    await act(async () => { rejectRenew(new NexApiError(409, 'lease_mismatch', 'stale')) })
    expect(lease()?.leaseId).toBe('ls_2')

    // The default renewLease mock always echoes 'ls_1' regardless of the id
    // it was called with (fine for the ls_1-only tests above); make this
    // one honor the id it's given so we can tell a real ls_2 renewal apart
    // from an accidental overwrite.
    vi.mocked(api.renewLease).mockImplementation(async (_h, _e, leaseId) => ({ mode: 'control', lease_id: leaseId, expires_at: Date.now() + 30_000 }))
    const callsBefore = vi.mocked(api.renewLease).mock.calls.length
    act(() => { result.current.touch() })
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(vi.mocked(api.renewLease).mock.calls.length).toBeGreaterThan(callsBefore)
    expect(vi.mocked(api.renewLease)).toHaveBeenCalledWith(H, E, 'ls_2')
    expect(lease()?.leaseId).toBe('ls_2')
  })

  it('restarts the heartbeat when the fast path reuses a lease after the idle timer stopped (I3 re-arm)', async () => {
    const { result } = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await result.current.ensureLease() })
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000 * LEASE_IDLE_MULTIPLIER + 10_000) }) // idle timeout stops the timer
    const attachCallsBefore = vi.mocked(api.attachControl).mock.calls.length
    act(() => { result.current.touch() })
    let id = ''
    await act(async () => { id = await result.current.ensureLease() })
    expect(id).toBe('ls_1')
    expect(vi.mocked(api.attachControl).mock.calls.length).toBe(attachCallsBefore) // fast path: no new attach

    const renewCallsBefore = vi.mocked(api.renewLease).mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000) })
    expect(vi.mocked(api.renewLease).mock.calls.length).toBeGreaterThan(renewCallsBefore)
  })

  it('fires a keepalive release on beforeunload without waiting for unmount', async () => {
    const { result } = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await result.current.ensureLease() })
    act(() => { window.dispatchEvent(new Event('beforeunload')) })
    expect(api.releaseLease).toHaveBeenCalledWith(H, E, 'ls_1', { keepalive: true })
  })

  it('ensureLease works again after a removed host is re-added (keep-tabs delete then undo)', async () => {
    const hostRow = useHostStore.getState().hosts[H]
    const { result } = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await result.current.ensureLease() })
    act(() => { useHostStore.setState({ hosts: {}, hostOrder: [] }) })
    expect(lease()).toBeNull()
    act(() => { useHostStore.setState({ hosts: { [H]: hostRow }, hostOrder: [H] }) })
    vi.mocked(api.attachControl).mockResolvedValue({ mode: 'control', lease_id: 'ls_2', expires_at: Date.now() + 30_000 })
    let id = ''
    await act(async () => { id = await result.current.ensureLease() })
    expect(id).toBe('ls_2')
    expect(lease()).toEqual({ leaseId: 'ls_2', expiresAt: expect.any(Number) })
    expect(api.releaseLease).not.toHaveBeenCalled()
  })

  it('rejects ensureLease immediately when the host has been removed, without attaching (host_removed)', async () => {
    const { result } = renderHook(() => useExecutionLease(H, E))
    act(() => { useHostStore.setState({ hosts: {}, hostOrder: [] }) })
    await expect(result.current.ensureLease()).rejects.toMatchObject({ code: 'host_removed' })
    expect(api.attachControl).not.toHaveBeenCalled()
  })

  it('releases exactly once on unmount when held, never when not (I6)', async () => {
    const a = renderHook(() => useExecutionLease(H, E))
    a.unmount()
    expect(api.releaseLease).not.toHaveBeenCalled()
    const b = renderHook(() => useExecutionLease(H, E))
    await act(async () => { await b.result.current.ensureLease() })
    b.unmount()
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.releaseLease).toHaveBeenCalledTimes(1)
    expect(api.releaseLease).toHaveBeenCalledWith(H, E, 'ls_1')
    expect(lease()).toBeNull()
  })
})
