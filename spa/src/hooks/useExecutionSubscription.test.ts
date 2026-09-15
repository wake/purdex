// spa/src/hooks/useExecutionSubscription.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useExecutionSubscription, HISTORY_PAGE_LIMIT, SUMMARY_REFETCH_DEBOUNCE_MS } from './useExecutionSubscription'
import { useExecutionStore } from '../stores/useExecutionStore'
import { useHostStore } from '../stores/useHostStore'
import { NexApiError, type ExecutionSummary } from '../lib/nex/types'
import * as api from '../lib/nex/nex-api'
import * as sse from '../lib/nex/nex-sse'
import type { NexSseOptions } from '../lib/nex/nex-sse'
import { subscriptionSlots } from '../lib/nex/subscription-slots'

vi.mock('../lib/nex/nex-api', () => ({ getExecution: vi.fn(), attachObserve: vi.fn(), fetchExecutionEvents: vi.fn() }))
vi.mock('../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))

const H = 'host-a', E = 'exc_1', KEY = 'host-a:exc_1'
const summary = (extra = {}) => ({ id: E, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 2, observers: 0, archived: false, ...extra }) as ExecutionSummary
const ev = (seq: number, kind = 'assistant') => ({ seq, execution_id: E, kind, payload: { type: kind }, created_at: 0 })
// A close mock always has this shape — typed once so every `{ close }`
// literal returned from an openNexSse mock structurally satisfies
// NexSseHandle without each call site needing its own annotation.
type CloseMock = ReturnType<typeof vi.fn<() => void>>

let sseOpts: NexSseOptions | null
let sseClose: CloseMock

describe('useExecutionSubscription', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    subscriptionSlots.resetForTests()
    useExecutionStore.setState({ executions: {} })
    useHostStore.setState({ hosts: { [H]: { id: H, name: 'A', ip: '1', port: 1 } } as never, hostOrder: [H], activeHostId: H, runtime: {} })
    sseOpts = null
    sseClose = vi.fn<() => void>()
    vi.mocked(sse.openNexSse).mockReset().mockImplementation((o) => { sseOpts = o; return { close: sseClose } })
    vi.mocked(api.getExecution).mockReset().mockResolvedValue(summary())
    vi.mocked(api.attachObserve).mockReset().mockResolvedValue({ mode: 'observe', stream_url: '/api/nex/v1/events?execution_id=exc_1', cursor: 2, state: 'idle' })
    vi.mocked(api.fetchExecutionEvents).mockReset()
      .mockResolvedValueOnce({ items: [ev(1), ev(2)], next_cursor: 0 })
  })
  afterEach(() => vi.useRealTimers())

  it('loads summary, pages history ascending, then opens SSE at lastSeq (order contract)', async () => {
    const { result } = renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.getExecution).toHaveBeenCalledWith(H, E)
    expect(api.attachObserve).toHaveBeenCalledWith(H, E)
    expect(api.fetchExecutionEvents).toHaveBeenCalledWith(H, E, { after: 0, limit: HISTORY_PAGE_LIMIT })
    const st = useExecutionStore.getState().executions[KEY]
    expect(st.messages).toHaveLength(2)
    expect(st.historyLoaded).toBe(true)
    expect(sse.openNexSse).toHaveBeenCalledTimes(1)
    expect(sseOpts!.url).toBe('/api/nex/v1/events?execution_id=exc_1')
    expect(sseOpts!.getLastEventId()).toBe(2)
    expect(result.current.problem).toBeNull()
    // history was applied before SSE opened
    const order = [api.fetchExecutionEvents, sse.openNexSse].map((f) => vi.mocked(f).mock.invocationCallOrder[0])
    expect(order[0]).toBeLessThan(order[1])
  })

  it('follows next_cursor across pages and stops at 0', async () => {
    vi.mocked(api.fetchExecutionEvents).mockReset()
      .mockResolvedValueOnce({ items: [ev(1)], next_cursor: 1 })
      .mockResolvedValueOnce({ items: [ev(2)], next_cursor: 0 })
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.fetchExecutionEvents).toHaveBeenNthCalledWith(2, H, E, { after: 1, limit: HISTORY_PAGE_LIMIT })
    expect(useExecutionStore.getState().executions[KEY].lastSeq).toBe(2)
  })

  it('applies durable SSE frames, drops transient ones, mirrors status', async () => {
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => {
      sseOpts!.onStatus('open')
      sseOpts!.onFrame({ id: '3', event: 'assistant', data: '{"type":"assistant"}' })
      sseOpts!.onFrame({ id: null, event: 'stream_event', data: '{}' })
      sseOpts!.onFrame({ id: '4', event: 'assistant', data: '{not json' })
    })
    const st = useExecutionStore.getState().executions[KEY]
    expect(st.sse).toBe('open')
    expect(st.messages).toHaveLength(3)
    expect(st.lastSeq).toBe(3)
  })

  it('refetches the summary when a lifecycle event marks it stale (debounced) and after reconnect', async () => {
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    vi.mocked(api.getExecution).mockClear()
    act(() => {
      sseOpts!.onFrame({ id: '3', event: 'execution.running', data: '{}' })
      sseOpts!.onFrame({ id: '4', event: 'execution.terminal', data: '{"reason":"completed","state":"idle","turn_id":"t"}' })
    })
    await act(async () => { await vi.advanceTimersByTimeAsync(SUMMARY_REFETCH_DEBOUNCE_MS + 1) })
    expect(api.getExecution).toHaveBeenCalledTimes(1)
    expect(useExecutionStore.getState().executions[KEY].summaryStale).toBe(false)
    act(() => { sseOpts!.onStatus('reconnecting'); sseOpts!.onStatus('open') })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.getExecution).toHaveBeenCalledTimes(2)
  })

  it('reports not_found and opens nothing when the summary 404s', async () => {
    vi.mocked(api.getExecution).mockRejectedValueOnce(new NexApiError(404, 'execution_not_found', 'nope'))
    const { result } = renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(result.current.problem).toBe('not_found')
    expect(sse.openNexSse).not.toHaveBeenCalled()
  })

  it('reports nex_unavailable on 503 nex_unavailable and nex_disabled on a bare 404', async () => {
    vi.mocked(api.getExecution).mockRejectedValueOnce(new NexApiError(503, 'nex_unavailable', 'init failed'))
    const a = renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(a.result.current.problem).toBe('nex_unavailable')
    a.unmount()
    vi.mocked(api.getExecution).mockRejectedValueOnce(new NexApiError(404, 'http_404', 'nex: HTTP 404'))
    const b = renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(b.result.current.problem).toBe('nex_disabled')
  })

  it('keeps the server message on the store as sseError instead of the bare problem code (spec §4.5)', async () => {
    vi.mocked(api.getExecution).mockRejectedValueOnce(new NexApiError(503, 'nex_unavailable', 'nex: init: assembling engine: boom'))
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(useExecutionStore.getState().executions[KEY].sseError).toContain('boom')
  })

  it('warns once per connection on a malformed durable frame and does not advance the cursor', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => {
      sseOpts!.onFrame({ id: '9', event: 'assistant', data: '{oops' })
      sseOpts!.onFrame({ id: '10', event: 'assistant', data: '{oops again' })
    })
    expect(warn).toHaveBeenCalledTimes(1)
    expect(useExecutionStore.getState().executions[KEY].lastSeq).toBe(2)
    warn.mockRestore()
  })

  it('closes the SSE on unmount and when the executionId changes', async () => {
    const { rerender, unmount } = renderHook(({ id }) => useExecutionSubscription(H, id, true), { initialProps: { id: E } })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    vi.mocked(api.fetchExecutionEvents).mockResolvedValueOnce({ items: [], next_cursor: 0 })
    rerender({ id: 'exc_2' })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(sseClose).toHaveBeenCalledTimes(1)
    unmount()
    expect(sseClose).toHaveBeenCalledTimes(2)
  })

  it('reports host_removed immediately when the stored host does not exist — never falls back to another host', async () => {
    useHostStore.setState({ hosts: { other: { id: 'other', name: 'O', ip: '9', port: 1 } } as never, hostOrder: ['other'], activeHostId: 'other', runtime: {} })
    const { result } = renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(result.current.problem).toBe('host_removed')
    expect(api.getExecution).not.toHaveBeenCalled()
    expect(sse.openNexSse).not.toHaveBeenCalled()
  })

  it('pauses the least-recently-active subscription beyond MAX_LIVE_SUBSCRIPTIONS_PER_HOST and resumes it on activation', async () => {
    const closes: Record<string, CloseMock> = {}
    vi.mocked(sse.openNexSse).mockImplementation((o) => {
      const id = new URL(o.url, 'http://x').searchParams.get('execution_id')!
      closes[id] = closes[id] ?? vi.fn<() => void>()
      return { close: closes[id] }
    })
    vi.mocked(api.attachObserve).mockImplementation(async (_h, id) => ({ mode: 'observe', stream_url: `/api/nex/v1/events?execution_id=${id}`, cursor: 0, state: 'idle' }))
    vi.mocked(api.fetchExecutionEvents).mockResolvedValue({ items: [], next_cursor: 0 })
    const hooks = ['exc_a', 'exc_b', 'exc_c', 'exc_d'].map((id) => renderHook(({ active }) => useExecutionSubscription(H, id, active), { initialProps: { active: true } }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(Object.keys(closes)).toHaveLength(4)
    // fifth pane activates → exc_a (least recently active) pauses
    const fifth = renderHook(({ active }) => useExecutionSubscription(H, 'exc_e', active), { initialProps: { active: true } })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(closes['exc_a']).toHaveBeenCalledTimes(1)
    expect(useExecutionStore.getState().executions['host-a:exc_a'].sse).toBe('paused')
    expect(hooks[0].result.current.paused).toBe(true)
    // re-activating exc_a evicts the now least-recent (exc_b) and reopens exc_a with Last-Event-ID.
    // Adapted from the brief (which reran with the *same* active:true value): a slot loss from the
    // LRU cap is not itself an `active` transition, so nothing in the hook's reactive inputs changes
    // on a same-value rerender — an effect gated on unchanged deps correctly does not refire (and an
    // effect that fires on every render to work around that self-heals the pause the instant this
    // component re-renders from setPaused(true), which is an infinite loop, empirically confirmed by
    // an OOM crash). A real pane's `active` genuinely flips false→true when it regains focus after
    // losing its slot to a busier sibling, so the test drives that same transition explicitly.
    hooks[0].rerender({ active: false })
    hooks[0].rerender({ active: true })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(closes['exc_b']).toHaveBeenCalledTimes(1)
    expect(vi.mocked(sse.openNexSse).mock.calls.filter((c) => c[0].url.includes('exc_a'))).toHaveLength(2)
    expect(hooks[0].result.current.paused).toBe(false)
    fifth.unmount(); hooks.forEach((h) => h.unmount())
  })

  it('host removal closes the SSE and reports host_removed (keep-tabs mode, I13)', async () => {
    const { result } = renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => { useHostStore.setState({ hosts: {}, hostOrder: [] }) })
    expect(sseClose).toHaveBeenCalledTimes(1)
    expect(result.current.problem).toBe('host_removed')
    expect(useExecutionStore.getState().executions[KEY]?.sse ?? 'closed').toBe('closed')
  })

  // --- fix round 1 -----------------------------------------------------

  it('an inactive-at-mount pane claims a free slot and goes live (spec §4.3.2 step 4); once slots are full it stays paused until activation evicts the LRU', async () => {
    const closes: Record<string, CloseMock> = {}
    vi.mocked(sse.openNexSse).mockImplementation((o) => {
      const id = new URL(o.url, 'http://x').searchParams.get('execution_id')!
      closes[id] = closes[id] ?? vi.fn<() => void>()
      return { close: closes[id] }
    })
    vi.mocked(api.attachObserve).mockImplementation(async (_h, id) => ({ mode: 'observe', stream_url: `/api/nex/v1/events?execution_id=${id}`, cursor: 0, state: 'idle' }))
    vi.mocked(api.fetchExecutionEvents).mockResolvedValue({ items: [], next_cursor: 0 })

    // A) inactive at mount, a slot is free → goes live anyway.
    const first = renderHook(({ active }) => useExecutionSubscription(H, 'exc_a', active), { initialProps: { active: false } })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(closes['exc_a']).toBeDefined()
    expect(first.result.current.paused).toBe(false)
    expect(useExecutionStore.getState().executions[`${H}:exc_a`].sse).not.toBe('paused')

    // B) fill the remaining 3 slots (exc_a + these three = 4, at cap).
    const filler = ['exc_b', 'exc_c', 'exc_d'].map((id) => renderHook(() => useExecutionSubscription(H, id, true)))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(Object.keys(closes)).toHaveLength(4)

    // C) a 6th pane mounts inactive with no free slot → stays paused, no SSE opened.
    const sixth = renderHook(({ active }) => useExecutionSubscription(H, 'exc_f', active), { initialProps: { active: false } })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(sixth.result.current.paused).toBe(true)
    expect(closes['exc_f']).toBeUndefined()
    expect(useExecutionStore.getState().executions[`${H}:exc_f`].sse).toBe('paused')

    // D) activating it evicts the LRU and goes live.
    sixth.rerender({ active: true })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(sixth.result.current.paused).toBe(false)
    expect(closes['exc_f']).toBeDefined()
    expect(closes['exc_f']).toHaveBeenCalledTimes(0) // its own stream never closed
    const evictedCount = ['exc_a', 'exc_b', 'exc_c', 'exc_d'].filter((id) => closes[id].mock.calls.length > 0).length
    expect(evictedCount).toBe(1)

    first.unmount(); filler.forEach((h) => h.unmount()); sixth.unmount()
  })

  it('re-triggers a stale-summary refetch when the store is still stale after the fetch resolves (subscribe only fires on a boolean transition)', async () => {
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    vi.mocked(api.getExecution).mockClear()

    let resolveFirst!: (v: ExecutionSummary) => void
    vi.mocked(api.getExecution).mockImplementationOnce(() => new Promise<ExecutionSummary>((resolve) => { resolveFirst = resolve }))

    act(() => { sseOpts!.onFrame({ id: '10', event: 'execution.running', data: '{}' }) })
    await act(async () => { await vi.advanceTimersByTimeAsync(SUMMARY_REFETCH_DEBOUNCE_MS + 1) })
    expect(api.getExecution).toHaveBeenCalledTimes(1)

    // A second lifecycle event lands while the first refetch is still in flight.
    act(() => { sseOpts!.onFrame({ id: '11', event: 'execution.terminal', data: '{"reason":"completed","state":"idle","turn_id":"t"}' }) })
    expect(useExecutionStore.getState().executions[KEY].lastSeq).toBe(11)

    // Primed before the first resolves, for the retry this fix must trigger.
    vi.mocked(api.getExecution).mockResolvedValueOnce(summary({ state: 'running', event_count: 99 }))

    await act(async () => { resolveFirst(summary({ state: 'running' })); await vi.advanceTimersByTimeAsync(0) })
    // The stale first response (fetched as-of seq 10) must not clobber the local 'idle' patch from seq 11.
    expect(useExecutionStore.getState().executions[KEY].summary?.state).toBe('idle')
    expect(useExecutionStore.getState().executions[KEY].summaryStale).toBe(true)

    await act(async () => { await vi.advanceTimersByTimeAsync(SUMMARY_REFETCH_DEBOUNCE_MS + 1) })
    expect(api.getExecution).toHaveBeenCalledTimes(2)
    expect(useExecutionStore.getState().executions[KEY].summaryStale).toBe(false)
    expect(useExecutionStore.getState().executions[KEY].summary?.event_count).toBe(99)
  })

  it('caps consecutive stale/failed refetches so a persistent problem does not loop forever every debounce', async () => {
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    vi.mocked(api.getExecution).mockClear()
    // Every refetch fails — the "failed" half of the fix's retry cap.
    vi.mocked(api.getExecution).mockRejectedValue(new Error('network down'))

    act(() => { sseOpts!.onFrame({ id: '20', event: 'execution.running', data: '{}' }) })
    for (let i = 0; i < 8; i++) {
      await act(async () => { await vi.advanceTimersByTimeAsync(SUMMARY_REFETCH_DEBOUNCE_MS + 1) })
    }
    // 8 debounce cycles elapsed, but each failure reschedules the next
    // attempt itself (there is no fresh stale->true transition to drive
    // it) — the cap must have stopped that chain well short of 8.
    expect(vi.mocked(api.getExecution).mock.calls.length).toBeLessThanOrEqual(5)
    expect(vi.mocked(api.getExecution).mock.calls.length).toBeGreaterThanOrEqual(1)
  })

  it('a debounced refetch whose getExecution rejects after unmount does not re-arm the retry timer', async () => {
    const { unmount } = renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    vi.mocked(api.getExecution).mockClear()

    let rejectDeferred!: (e: unknown) => void
    vi.mocked(api.getExecution).mockImplementationOnce(() => new Promise<ExecutionSummary>((_resolve, reject) => { rejectDeferred = reject }))

    act(() => { sseOpts!.onFrame({ id: '30', event: 'execution.running', data: '{}' }) })
    await act(async () => { await vi.advanceTimersByTimeAsync(SUMMARY_REFETCH_DEBOUNCE_MS + 1) })
    expect(api.getExecution).toHaveBeenCalledTimes(1) // the debounced refetch, now in flight

    unmount()
    await act(async () => { rejectDeferred(new Error('network down after unmount')); await vi.advanceTimersByTimeAsync(0) })

    // The rejection landed after cleanup — no stray retry timer should have
    // been armed by it (up to 4 stray post-unmount calls were possible
    // before this fix, one per uncapped consecutive-failure retry).
    await act(async () => { await vi.advanceTimersByTimeAsync(SUMMARY_REFETCH_DEBOUNCE_MS + 1) })
    expect(api.getExecution).toHaveBeenCalledTimes(1)
  })

  it('resets paused to false when executionId changes, so a fresh key with a free slot goes live rather than staying stuck paused', async () => {
    vi.mocked(api.attachObserve).mockImplementation(async (_h, id) => ({ mode: 'observe', stream_url: `/api/nex/v1/events?execution_id=${id}`, cursor: 0, state: 'idle' }))
    vi.mocked(api.fetchExecutionEvents).mockResolvedValue({ items: [], next_cursor: 0 })
    const closes: Record<string, CloseMock> = {}
    vi.mocked(sse.openNexSse).mockImplementation((o) => {
      const id = new URL(o.url, 'http://x').searchParams.get('execution_id')!
      closes[id] = closes[id] ?? vi.fn<() => void>()
      return { close: closes[id] }
    })

    const { result, rerender } = renderHook(({ id, active }) => useExecutionSubscription(H, id, active), { initialProps: { id: E, active: true } })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(closes[E]).toBeDefined()

    // Evict this pane by filling the cap with four other keys, then free one back up.
    act(() => {
      subscriptionSlots.touch(H, 'other-1')
      subscriptionSlots.touch(H, 'other-2')
      subscriptionSlots.touch(H, 'other-3')
      subscriptionSlots.touch(H, 'other-4')
    })
    expect(result.current.paused).toBe(true)
    act(() => { subscriptionSlots.release(H, 'other-1') }) // free a slot

    rerender({ id: 'exc_new', active: false })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(result.current.paused).toBe(false)
    expect(closes['exc_new']).toBeDefined()
  })

  it('retries the whole chain with backoff after a non-terminal error, and completes once it succeeds (I1)', async () => {
    vi.mocked(api.getExecution).mockReset().mockRejectedValueOnce(new TypeError('Failed to fetch')).mockResolvedValueOnce(summary())
    const { result } = renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.getExecution).toHaveBeenCalledTimes(1)
    expect(result.current.problem).toBeNull()
    expect(useExecutionStore.getState().executions[KEY].sse).toBe('closed')
    expect(useExecutionStore.getState().executions[KEY].sseError).toMatch(/Failed to fetch/)

    // First backoff delay (2s) elapses — the chain retries from the top.
    await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
    expect(api.getExecution).toHaveBeenCalledTimes(2)
    expect(useExecutionStore.getState().executions[KEY].historyLoaded).toBe(true)
    expect(result.current.problem).toBeNull()
  })

  it('unmount cancels a pending retry timer so getExecution is not called again', async () => {
    vi.mocked(api.getExecution).mockReset().mockRejectedValue(new TypeError('Failed to fetch'))
    const { unmount } = renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.getExecution).toHaveBeenCalledTimes(1)
    unmount()
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
    expect(api.getExecution).toHaveBeenCalledTimes(1)
  })

  it('a terminal SSE close (with error) releases the slot so a later activation can reopen', async () => {
    const { rerender } = renderHook(({ active }) => useExecutionSubscription(H, E, active), { initialProps: { active: true } })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(sse.openNexSse).toHaveBeenCalledTimes(1)

    act(() => { sseOpts!.onStatus('closed', new Error('unauthorized')) })
    expect(subscriptionSlots.isLive(H, KEY)).toBe(false)
    expect(useExecutionStore.getState().executions[KEY].sse).toBe('closed')

    rerender({ active: false })
    rerender({ active: true })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(sse.openNexSse).toHaveBeenCalledTimes(2)
  })
})
