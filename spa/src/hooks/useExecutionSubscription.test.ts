// spa/src/hooks/useExecutionSubscription.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useExecutionSubscription, HISTORY_PAGE_LIMIT, SUMMARY_REFETCH_DEBOUNCE_MS } from './useExecutionSubscription'
import { useExecutionStore } from '../stores/useExecutionStore'
import { useHostStore } from '../stores/useHostStore'
import { NexApiError } from '../lib/nex/types'
import * as api from '../lib/nex/nex-api'
import * as sse from '../lib/nex/nex-sse'
import type { NexSseOptions } from '../lib/nex/nex-sse'
import { subscriptionSlots } from '../lib/nex/subscription-slots'

vi.mock('../lib/nex/nex-api', () => ({ getExecution: vi.fn(), attachObserve: vi.fn(), fetchExecutionEvents: vi.fn() }))
vi.mock('../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))

const H = 'host-a', E = 'exc_1', KEY = 'host-a:exc_1'
const summary = (extra = {}) => ({ id: E, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 2, observers: 0, archived: false, ...extra })
const ev = (seq: number, kind = 'assistant') => ({ seq, execution_id: E, kind, payload: { type: kind }, created_at: 0 })

let sseOpts: NexSseOptions | null
let sseClose: ReturnType<typeof vi.fn>

describe('useExecutionSubscription', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    subscriptionSlots.resetForTests()
    useExecutionStore.setState({ executions: {} })
    useHostStore.setState({ hosts: { [H]: { id: H, name: 'A', ip: '1', port: 1 } } as never, hostOrder: [H], activeHostId: H, runtime: {} })
    sseOpts = null
    sseClose = vi.fn()
    vi.mocked(sse.openNexSse).mockReset().mockImplementation((o) => { sseOpts = o; return { close: sseClose } })
    vi.mocked(api.getExecution).mockReset().mockResolvedValue(summary() as never)
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
    const closes: Record<string, ReturnType<typeof vi.fn>> = {}
    vi.mocked(sse.openNexSse).mockImplementation((o) => {
      const id = new URL(o.url, 'http://x').searchParams.get('execution_id')!
      closes[id] = closes[id] ?? vi.fn()
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
})
