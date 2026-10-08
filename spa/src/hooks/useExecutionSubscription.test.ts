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
import { useNexHostStore } from '../stores/useNexHostStore'
import type { WorkerTask, WorkerTasksSnapshot } from '../lib/nex/types'
import { startWorkerAgentProjection } from './useWorkerAgentProjection'
import { useAgentStore } from '../stores/useAgentStore'
import { useTabStore } from '../stores/useTabStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { emptyListCache } from '../lib/nex/execution-list-effects'
import { compositeKey } from '../lib/composite-key'
import { execAgentCode } from '../lib/nex/worker-agent-status'

vi.mock('../lib/nex/nex-api', () => ({ getExecution: vi.fn(), attachObserve: vi.fn(), fetchExecutionEvents: vi.fn(), fetchExecutionTasks: vi.fn() }))
vi.mock('../lib/nex/nex-sse', () => ({ openNexSse: vi.fn() }))

const H = 'host-a', E = 'exc_1', KEY = 'host-a:exc_1'
const summary = (extra = {}) => ({ id: E, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 2, observers: 0, archived: false, ...extra }) as ExecutionSummary
const ev = (seq: number, kind = 'assistant') => ({ seq, execution_id: E, kind, payload: { type: kind }, created_at: 0 })
// A close mock always has this shape — typed once so every `{ close }`
// literal returned from an openNexSse mock structurally satisfies
// NexSseHandle without each call site needing its own annotation.
type CloseMock = ReturnType<typeof vi.fn<() => void>>
const transient = (event: Record<string, unknown>) => ({ id: null, event: 'stream_event', data: JSON.stringify({ type: 'stream_event', event }) })
const messageStart = (id: string) => transient({ type: 'message_start', message: { id, role: 'assistant', content: [] } })
const textDelta = (index: number, text: string) => transient({ type: 'content_block_delta', index, delta: { type: 'text_delta', text } })

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

  it('applies durable SSE frames and coalesces transient ones without moving lastSeq', async () => {
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => {
      sseOpts!.onStatus('open')
      sseOpts!.onFrame({ id: '3', event: 'assistant', data: '{"type":"assistant"}' })
      sseOpts!.onFrame(messageStart('m1'))
      sseOpts!.onFrame(textDelta(0, 'hi'))
    })
    expect(useExecutionStore.getState().executions[KEY].partial).toBeNull()
    await act(async () => { await vi.advanceTimersByTimeAsync(20) })
    const st = useExecutionStore.getState().executions[KEY]
    expect(st.sse).toBe('open')
    expect(st.partial?.blocks[0].text).toBe('hi')
    expect(st.lastSeq).toBe(3)
    expect(st.messages).toHaveLength(3)
  })

  it('durable frames flush the transient queue first (delta then assistant in the same tick finalizes the block)', async () => {
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => {
      sseOpts!.onStatus('open')
      sseOpts!.onFrame(messageStart('m1'))
      sseOpts!.onFrame(textDelta(0, 'a'))
      sseOpts!.onFrame({ id: '3', event: 'assistant', data: '{"type":"assistant","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"a"}],"stop_reason":null}}' })
    })
    const st = useExecutionStore.getState().executions[KEY]
    expect(st.partial?.blocks[0]).toBeUndefined()
    expect(st.partial?.finalized).toBe(1)
    expect(st.messages).toHaveLength(3)
    expect(st.messages[2].type).toBe('assistant')
  })

  // Spec §6.1 / A4: Nexen's live SSE `data:` is the bare provider payload
  // (no `{seq, kind, payload, created_at}` wrapper), so frameToEvent yields
  // created_at 0 for every live durable frame. The hook stamps client
  // arrival time so the tool timers run on live turns (history keeps the
  // server's created_at).
  it('stamps a bare live durable frame (no wrapper, created_at 0) with Date.now() at arrival', async () => {
    const applyEvents = vi.spyOn(useExecutionStore.getState(), 'applyEvents')
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    vi.setSystemTime(1_700_000_000_000)
    act(() => {
      sseOpts!.onStatus('open')
      sseOpts!.onFrame({ id: '3', event: 'assistant', data: '{"type":"assistant"}' })
    })
    const live = applyEvents.mock.calls.at(-1)![2]
    expect(live).toHaveLength(1)
    expect(live[0]).toMatchObject({ seq: 3, kind: 'assistant', created_at: 1_700_000_000_000 })
    // History (applied before SSE opened) is left exactly as the server sent it.
    expect(applyEvents.mock.calls[0][2].map((e) => e.created_at)).toEqual([0, 0])
  })

  it('keeps the wrapper created_at when a live durable frame carries one', async () => {
    const applyEvents = vi.spyOn(useExecutionStore.getState(), 'applyEvents')
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    vi.setSystemTime(1_700_000_000_000)
    act(() => {
      sseOpts!.onStatus('open')
      sseOpts!.onFrame({ id: '3', event: 'assistant', data: JSON.stringify({ seq: 3, kind: 'assistant', payload: { type: 'assistant' }, created_at: 123 }) })
    })
    const live = applyEvents.mock.calls.at(-1)![2]
    expect(live[0]).toMatchObject({ seq: 3, kind: 'assistant', created_at: 123 })
  })

  it('bare live tool_use then tool_result 5 s later → tools[id] started/ended at the two arrival times, status done', async () => {
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    const toolUse = { type: 'assistant', parent_tool_use_id: null, message: { id: 'm1', role: 'assistant', content: [{ type: 'tool_use', id: 'tu1', name: 'Bash', input: { command: 'sleep 5' } }], stop_reason: null } }
    const toolResult = { type: 'user', parent_tool_use_id: null, message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 'tu1', content: 'ok', is_error: false }], stop_reason: null } }
    vi.setSystemTime(50_000)
    act(() => {
      sseOpts!.onStatus('open')
      sseOpts!.onFrame({ id: '3', event: 'assistant', data: JSON.stringify(toolUse) })
    })
    expect(useExecutionStore.getState().executions[KEY].tools.tu1).toMatchObject({ name: 'Bash', startedAt: 50_000, endedAt: null, status: 'running' })
    vi.setSystemTime(55_000)
    act(() => { sseOpts!.onFrame({ id: '4', event: 'user', data: JSON.stringify(toolResult) }) })
    expect(useExecutionStore.getState().executions[KEY].tools.tu1).toEqual({ name: 'Bash', startedAt: 50_000, endedAt: 55_000, status: 'done' })
  })

  it('reconnecting drops the queued transient frames', async () => {
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => {
      sseOpts!.onStatus('open')
      sseOpts!.onFrame(textDelta(0, 'x'))
      sseOpts!.onStatus('reconnecting')
    })
    await act(async () => { await vi.advanceTimersByTimeAsync(20) })
    expect(useExecutionStore.getState().executions[KEY].partial).toBeNull()
  })

  it('closed drops the queued transient frames', async () => {
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => {
      sseOpts!.onStatus('open')
      sseOpts!.onFrame(textDelta(0, 'x'))
      sseOpts!.onStatus('closed')
    })
    await act(async () => { await vi.advanceTimersByTimeAsync(20) })
    expect(useExecutionStore.getState().executions[KEY].partial).toBeNull()
  })

  it('connecting bumps the generation so an already-scheduled flush writes nothing', async () => {
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => {
      sseOpts!.onStatus('open')
      sseOpts!.onFrame(textDelta(0, 'x'))
      sseOpts!.onStatus('connecting')
    })
    await act(async () => { await vi.advanceTimersByTimeAsync(20) })
    expect(useExecutionStore.getState().executions[KEY].partial).toBeNull()
  })

  it('a stale-generation flush after reconnect cannot land on the new connection', async () => {
    const applyTransient = vi.spyOn(useExecutionStore.getState(), 'applyTransient')
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    const snapshot = { message_id: 'm1', blocks: [{ index: 0, text: 'snap' }] }
    act(() => {
      sseOpts!.onStatus('open')
      sseOpts!.onFrame(textDelta(0, 'old'))
      sseOpts!.onStatus('reconnecting')
      sseOpts!.onStatus('open')
      sseOpts!.onFrame({ id: null, event: 'stream_snapshot', data: JSON.stringify(snapshot) })
    })
    await act(async () => { await vi.advanceTimersByTimeAsync(20) })
    expect(applyTransient).toHaveBeenCalledTimes(1)
    expect(applyTransient).toHaveBeenCalledWith(H, E, [{ kind: 'stream_snapshot', payload: snapshot }])
    const st = useExecutionStore.getState().executions[KEY]
    expect(st.partial?.messageId).toBe('m1')
    expect(st.partial?.blocks[0].text).toBe('snap')
    applyTransient.mockRestore()
  })

  it('unmount drops the queue and never writes after close', async () => {
    const { unmount } = renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => {
      sseOpts!.onStatus('open')
      sseOpts!.onFrame(textDelta(0, 'x'))
    })
    unmount()
    expect(sseClose).toHaveBeenCalled()
    await act(async () => { await vi.advanceTimersByTimeAsync(20) })
    expect(useExecutionStore.getState().executions[KEY].partial).toBeNull()
  })

  it('malformed transient JSON is dropped without a console warning', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => {
      sseOpts!.onStatus('open')
      sseOpts!.onFrame({ id: null, event: 'stream_event', data: '{not json' })
    })
    await act(async () => { await vi.advanceTimersByTimeAsync(20) })
    expect(warn).not.toHaveBeenCalled()
    expect(useExecutionStore.getState().executions[KEY].partial).toBeNull()
    warn.mockRestore()
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

  it('a refetch that started before an exit patch cannot bring the old summary back', async () => {
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    let resolveLate!: (v: ReturnType<typeof summary>) => void
    vi.mocked(api.getExecution).mockReset().mockReturnValueOnce(new Promise((r) => { resolveLate = r }))
    act(() => { sseOpts!.onFrame({ id: '3', event: 'execution.running', data: '{}' }) })
    await act(async () => { await vi.advanceTimersByTimeAsync(SUMMARY_REFETCH_DEBOUNCE_MS + 1) })
    expect(api.getExecution).toHaveBeenCalledTimes(1)
    act(() => { useExecutionStore.getState().applySummaryPatch(H, E, { state: 'terminated', archived: true }) })
    await act(async () => { resolveLate({ ...summary(), state: 'idle', archived: false } as ReturnType<typeof summary>) })
    expect(useExecutionStore.getState().executions[KEY].summary).toMatchObject({ state: 'terminated', archived: true })
  })

  it('an initial summary fetch that started before an exit patch is dropped too', async () => {
    let resolveLate!: (v: ReturnType<typeof summary>) => void
    vi.mocked(api.getExecution).mockReset().mockReturnValueOnce(new Promise((r) => { resolveLate = r }))
    useExecutionStore.getState().setSummary(H, E, summary() as never)
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => { useExecutionStore.getState().applySummaryPatch(H, E, { state: 'terminated', archived: true }) })
    await act(async () => { resolveLate({ ...summary(), state: 'idle', archived: false } as ReturnType<typeof summary>) })
    expect(useExecutionStore.getState().executions[KEY].summary).toMatchObject({ state: 'terminated', archived: true })
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

  // The entry outlives the pane (a tab switch unmounts it); with no stream feeding it, it must not keep saying `open`.
  it.each(['open', 'reconnecting'] as const)('unmount leaves the entry streamless (idle), not %s; a remount dials in again', async (status) => {
    const first = renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => { sseOpts!.onStatus(status) })
    expect(useExecutionStore.getState().executions[KEY].sse).toBe(status)
    first.unmount()
    expect(useExecutionStore.getState().executions[KEY].sse).toBe('idle')
    expect(useExecutionStore.getState().executions[KEY].sseError).toBeNull()

    vi.mocked(api.fetchExecutionEvents).mockResolvedValueOnce({ items: [], next_cursor: 0 })
    renderHook(() => useExecutionSubscription(H, E, true))
    expect(useExecutionStore.getState().executions[KEY].sse).toBe('connecting')
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(sse.openNexSse).toHaveBeenCalledTimes(2)
    act(() => { sseOpts!.onStatus('open') })
    expect(useExecutionStore.getState().executions[KEY].sse).toBe('open')
  })

  it('unmount keeps a terminal problem as it is (closed, with its reason)', async () => {
    vi.mocked(api.getExecution).mockRejectedValueOnce(new NexApiError(404, 'execution_not_found', 'nope'))
    const { unmount } = renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    unmount()
    expect(useExecutionStore.getState().executions[KEY]).toMatchObject({ sse: 'closed', sseError: 'nope' })
  })

  // Pin: alone on its execution, a pane whose stream fails gives the slot back and closes the entry with the reason.
  it.each([
    { name: 'chain fails terminally (not_found)', arrange: () => { vi.mocked(api.getExecution).mockRejectedValueOnce(new NexApiError(404, 'execution_not_found', 'nope')) }, fail: () => {}, err: 'nope' },
    { name: 'chain fails before its stream opens', arrange: () => { vi.mocked(api.getExecution).mockRejectedValueOnce(new TypeError('Failed to fetch')) }, fail: () => {}, err: 'Failed to fetch' },
    { name: 'stream closes terminally', arrange: () => {}, fail: () => { sseOpts!.onStatus('open'); sseOpts!.onStatus('closed', new Error('nex sse: HTTP 401')) }, err: 'nex sse: HTTP 401' },
  ])('a pane alone on its execution whose $name gives the slot back and closes the entry with the reason', async ({ arrange, fail, err }) => {
    arrange()
    renderHook(() => useExecutionSubscription(H, E, true))
    expect(subscriptionSlots.isLive(H, KEY)).toBe(true)
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => { fail() })
    expect(useExecutionStore.getState().executions[KEY]).toMatchObject({ sse: 'closed', sseError: err })
    expect(subscriptionSlots.isLive(H, KEY)).toBe(false)
  })

  // The same execution open in two panes (a split, or two tabs): two instances of this hook share the store entry and
  // the slot key. Each closes its own stream; only the last one to go releases the slot and leaves the entry idle.
  describe('the same execution in two panes', () => {
    interface Stream { opts: NexSseOptions; close: CloseMock }
    let streams: Stream[]
    const AGENT_KEY = compositeKey(H, execAgentCode(E))
    const entry = () => useExecutionStore.getState().executions[KEY]
    const subagentIds = () => useAgentStore.getState().subagents[AGENT_KEY]?.map((s) => s.id)
    const realListSubscribe = useExecutionListStore.getState().subscribe
    const realEnsure = useNexHostStore.getState().ensure
    let stopProjection: (() => void) | null = null

    beforeEach(() => {
      streams = []
      vi.mocked(sse.openNexSse).mockReset().mockImplementation((o) => {
        const s: Stream = { opts: o, close: vi.fn<() => void>() }
        streams.push(s)
        return { close: s.close }
      })
      vi.mocked(api.fetchExecutionEvents).mockReset().mockResolvedValue({ items: [ev(1), ev(2)], next_cursor: 0 })
      // The worker projection reads the entry's decoration (running-subagent refs) while its stream is open.
      useNexHostStore.setState({ ensure: vi.fn<(hostId: string) => Promise<void>>().mockResolvedValue(undefined) })
      useExecutionListStore.setState({
        subscribe: vi.fn(() => () => {}),
        byHost: { [H]: { ...emptyListCache(), phase: 'ready', complete: true, items: [summary({ state: 'running', turn_count: 1 })] } },
      })
      useAgentStore.setState({ statuses: {}, agentTypes: {}, models: {}, subagents: {}, lastEvents: {}, oscTitles: {}, ccStatus: {}, unread: {} })
      useTabStore.setState({
        tabs: { t1: { id: 't1', pinned: false, locked: false, createdAt: 0, layout: { type: 'leaf', pane: { id: 'p1', content: { kind: 'execution', executionId: E, host: H } } } } },
        tabOrder: ['t1'], activeTabId: null,
      })
      stopProjection = startWorkerAgentProjection()
    })
    afterEach(() => {
      stopProjection?.()
      stopProjection = null
      useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
      useExecutionListStore.setState({ byHost: {}, subscribe: realListSubscribe })
      useNexHostStore.setState({ ensure: realEnsure })
    })

    it('one pane closing leaves the other\'s stream feeding the entry: open, slot held, subagent refs kept; the last one closing goes idle and frees the slot', async () => {
      const a = renderHook(() => useExecutionSubscription(H, E, true))
      const b = renderHook(() => useExecutionSubscription(H, E, true))
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      expect(streams).toHaveLength(2)
      act(() => { streams[0].opts.onStatus('open'); streams[1].opts.onStatus('open') })
      act(() => {
        streams[1].opts.onFrame({ id: '3', event: 'task_start', data: JSON.stringify({ task_id: 'sa', turn_id: 't1', kind: 'subagent', subagent_type: 'Explore', started_at: 1000 }) })
      })
      expect(subagentIds()).toEqual(['sa'])

      a.unmount()
      expect(streams[0].close).toHaveBeenCalledTimes(1)
      expect(streams[1].close).not.toHaveBeenCalled()
      expect(entry().sse).toBe('open')
      expect(subscriptionSlots.isLive(H, KEY)).toBe(true)
      expect(subagentIds()).toEqual(['sa'])
      // The remaining pane's stream still feeds the entry.
      act(() => { streams[1].opts.onFrame({ id: '4', event: 'assistant', data: '{"type":"assistant"}' }) })
      expect(entry().lastSeq).toBe(4)
      expect(entry().sse).toBe('open')

      b.unmount()
      expect(streams[1].close).toHaveBeenCalledTimes(1)
      expect(entry().sse).toBe('idle')
      expect(subscriptionSlots.isLive(H, KEY)).toBe(false)
      expect(subagentIds()).toBeUndefined()
    })

    it('a pane switching to another execution releases nothing of the first one while another pane still shows it', async () => {
      const a = renderHook(({ id }) => useExecutionSubscription(H, id, true), { initialProps: { id: E } })
      const b = renderHook(() => useExecutionSubscription(H, E, true))
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      act(() => { streams[0].opts.onStatus('open'); streams[1].opts.onStatus('open') })
      a.rerender({ id: 'exc_2' })
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      expect(streams[0].close).toHaveBeenCalledTimes(1)
      expect(entry().sse).toBe('open')
      expect(subscriptionSlots.isLive(H, KEY)).toBe(true)

      b.unmount()
      expect(entry().sse).toBe('idle')
      expect(subscriptionSlots.isLive(H, KEY)).toBe(false)
      // The switched pane holds its new execution's slot.
      expect(subscriptionSlots.isLive(H, `${H}:exc_2`)).toBe(true)
    })

    // A pane whose own stream fails keeps that to itself while the other pane still streams: the slot and the entry's
    // stream status are the other's. The last stream to end frees the slot and leaves the entry streamless.
    const live = (seq: number) => ({ id: String(seq), event: 'assistant', data: '{"type":"assistant"}' })

    it('a pane whose chain fails terminally (not_found) while the other streams keeps only its problem: the slot and the open entry stay the other\'s, which keeps feeding it', async () => {
      const b = renderHook(() => useExecutionSubscription(H, E, true))
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      act(() => { streams[0].opts.onStatus('open') })
      vi.mocked(api.getExecution).mockRejectedValueOnce(new NexApiError(404, 'execution_not_found', 'nope'))
      const a = renderHook(() => useExecutionSubscription(H, E, true))
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      expect(a.result.current.problem).toBe('not_found')
      expect(streams).toHaveLength(1)
      expect(entry()).toMatchObject({ sse: 'open', sseError: null })
      expect(subscriptionSlots.isLive(H, KEY)).toBe(true)
      act(() => { streams[0].opts.onFrame(live(3)) })
      expect(entry().lastSeq).toBe(3)
      expect(entry().sse).toBe('open')

      b.unmount()
      expect(subscriptionSlots.isLive(H, KEY)).toBe(false)
      expect(entry().sse).toBe('idle')
      a.unmount()
    })

    it('a pane whose own stream closes terminally (401) while the other streams leaves the slot, the open entry and a send in flight to the other', async () => {
      const a = renderHook(() => useExecutionSubscription(H, E, true))
      const b = renderHook(() => useExecutionSubscription(H, E, true))
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      act(() => { streams[0].opts.onStatus('open'); streams[1].opts.onStatus('open') })
      act(() => { useExecutionStore.getState().setPendingSend(H, E, true) })
      act(() => { streams[0].opts.onStatus('closed', new Error('nex sse: HTTP 401')) })
      expect(entry()).toMatchObject({ sse: 'open', sseError: null, pendingSend: true })
      expect(subscriptionSlots.isLive(H, KEY)).toBe(true)
      act(() => { streams[1].opts.onFrame(live(3)) })
      expect(entry().lastSeq).toBe(3)
      expect(entry().sse).toBe('open')

      b.unmount()
      expect(subscriptionSlots.isLive(H, KEY)).toBe(false)
      expect(entry().sse).toBe('idle')
      a.unmount()
    })

    it('a pane whose chain fails before its stream opens while the other streams leaves the slot and the open entry alone, and its own retry still dials in', async () => {
      const b = renderHook(() => useExecutionSubscription(H, E, true))
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      act(() => { streams[0].opts.onStatus('open') })
      vi.mocked(api.getExecution).mockRejectedValueOnce(new TypeError('Failed to fetch'))
      const a = renderHook(() => useExecutionSubscription(H, E, true))
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      expect(a.result.current.problem).toBeNull()
      expect(streams).toHaveLength(1)
      expect(entry()).toMatchObject({ sse: 'open', sseError: null })
      expect(subscriptionSlots.isLive(H, KEY)).toBe(true)
      act(() => { streams[0].opts.onFrame(live(3)) })
      expect(entry().lastSeq).toBe(3)

      // Its own backoff (2 s) runs the chain again and opens a second stream onto the same entry.
      await act(async () => { await vi.advanceTimersByTimeAsync(2000) })
      expect(streams).toHaveLength(2)
      expect(entry().sse).toBe('open')
      act(() => { streams[1].opts.onStatus('open') })
      b.unmount()
      expect(entry().sse).toBe('open')
      expect(subscriptionSlots.isLive(H, KEY)).toBe(true)
      a.unmount()
      expect(entry().sse).toBe('idle')
      expect(subscriptionSlots.isLive(H, KEY)).toBe(false)
    })

    it('a pane dialling in or reconnecting never hides the other\'s delivering stream; once that one ends the entry shows the survivor\'s own status', async () => {
      renderHook(() => useExecutionSubscription(H, E, true))
      const b = renderHook(() => useExecutionSubscription(H, E, true))
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      act(() => { streams[0].opts.onStatus('open') })
      expect(entry()).toMatchObject({ sse: 'open', sseError: null })
      act(() => { streams[1].opts.onStatus('open'); streams[1].opts.onStatus('reconnecting', new Error('nex sse: idle timeout')) })
      expect(entry()).toMatchObject({ sse: 'open', sseError: null })

      act(() => { streams[0].opts.onStatus('closed', new Error('nex sse: HTTP 401')) })
      expect(entry()).toMatchObject({ sse: 'reconnecting', sseError: 'nex sse: idle timeout' })
      expect(subscriptionSlots.isLive(H, KEY)).toBe(true)
      act(() => { streams[1].opts.onStatus('open') })
      expect(entry()).toMatchObject({ sse: 'open', sseError: null })
      b.unmount()
      expect(entry().sse).toBe('idle')
      expect(subscriptionSlots.isLive(H, KEY)).toBe(false)
    })

    it('an eviction of the shared slot pauses both panes and the entry', async () => {
      const a = renderHook(() => useExecutionSubscription(H, E, true))
      const b = renderHook(() => useExecutionSubscription(H, E, true))
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      act(() => { streams[0].opts.onStatus('open'); streams[1].opts.onStatus('open') })
      act(() => { for (const k of ['x1', 'x2', 'x3', 'x4']) subscriptionSlots.touch(H, `${H}:${k}`) })
      expect(streams[0].close).toHaveBeenCalledTimes(1)
      expect(streams[1].close).toHaveBeenCalledTimes(1)
      expect(a.result.current.paused).toBe(true)
      expect(b.result.current.paused).toBe(true)
      expect(entry().sse).toBe('paused')
      expect(subscriptionSlots.isLive(H, KEY)).toBe(false)
    })

    it('an eviction while the other pane is still dialling in leaves the entry to that attempt, which pauses it once no slot is free for it either', async () => {
      const b = renderHook(() => useExecutionSubscription(H, E, true))
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      act(() => { streams[0].opts.onStatus('open') })
      let resolveHistory!: (page: Awaited<ReturnType<typeof api.fetchExecutionEvents>>) => void
      vi.mocked(api.fetchExecutionEvents).mockReturnValueOnce(new Promise((r) => { resolveHistory = r }))
      const a = renderHook(() => useExecutionSubscription(H, E, true))
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      expect(entry().sse).toBe('open')

      act(() => { for (const k of ['x1', 'x2', 'x3', 'x4']) subscriptionSlots.touch(H, `${H}:${k}`) })
      expect(streams[0].close).toHaveBeenCalledTimes(1)
      expect(b.result.current.paused).toBe(true)
      expect(entry().sse).toBe('connecting')

      await act(async () => { resolveHistory({ items: [], next_cursor: 0 }); await vi.advanceTimersByTimeAsync(0) })
      expect(streams).toHaveLength(1)
      expect(a.result.current.paused).toBe(true)
      expect(entry().sse).toBe('paused')
    })
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

  describe('onCapacity (#1866 §4.6)', () => {
    const setup = async () => {
      vi.mocked(sse.openNexSse).mockImplementation(() => ({ close: vi.fn<() => void>() }))
      vi.mocked(api.attachObserve).mockImplementation(async (_h, id) => ({ mode: 'observe', stream_url: `/api/nex/v1/events?execution_id=${id}`, cursor: 0, state: 'idle' }))
      vi.mocked(api.fetchExecutionEvents).mockResolvedValue({ items: [], next_cursor: 0 })
      const hooks = ['exc_a', 'exc_b', 'exc_c', 'exc_d'].map((id) => renderHook(({ active }) => useExecutionSubscription(H, id, active), { initialProps: { active: true } }))
      await act(async () => { await vi.advanceTimersByTimeAsync(0) })
      return hooks
    }
    const opensFor = (id: string) => vi.mocked(sse.openNexSse).mock.calls.filter((c) => c[0].url.includes(id)).length

    it('a pane the lane reservation paused resumes when the lane is given back and it is still active', async () => {
      const hooks = await setup()
      act(() => subscriptionSlots.reserve(H, 'site-wide')) // evicts exc_a
      expect(hooks[0].result.current.paused).toBe(true)
      await act(async () => { subscriptionSlots.unreserve(H, 'site-wide'); await vi.advanceTimersByTimeAsync(0) })
      expect(hooks[0].result.current.paused).toBe(false)
      expect(opensFor('exc_a')).toBe(2)
      hooks.forEach((h) => h.unmount())
    })

    it('an inactive evicted pane ignores the notice and stays paused until activated', async () => {
      const hooks = await setup()
      act(() => subscriptionSlots.reserve(H, 'site-wide'))
      hooks[0].rerender({ active: false })
      await act(async () => { subscriptionSlots.unreserve(H, 'site-wide'); await vi.advanceTimersByTimeAsync(0) })
      expect(hooks[0].result.current.paused).toBe(true)
      expect(opensFor('exc_a')).toBe(1)
      hooks.forEach((h) => h.unmount())
    })

    it('an ordinary LRU eviction between panes never resumes spontaneously', async () => {
      const hooks = await setup()
      const fifth = renderHook(({ active }) => useExecutionSubscription(H, 'exc_e', active), { initialProps: { active: true } })
      await act(async () => { await vi.advanceTimersByTimeAsync(0) }) // evicts exc_a, no reservation
      expect(hooks[0].result.current.paused).toBe(true)
      await act(async () => { subscriptionSlots.reserve(H, 'site-wide'); subscriptionSlots.unreserve(H, 'site-wide'); await vi.advanceTimersByTimeAsync(0) })
      expect(hooks[0].result.current.paused).toBe(true)
      expect(opensFor('exc_a')).toBe(1)
      fifth.unmount(); hooks.forEach((h) => h.unmount())
    })
  })

  it('host removal closes the SSE and reports host_removed (keep-tabs mode, I13)', async () => {
    const { result } = renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => { useHostStore.setState({ hosts: {}, hostOrder: [] }) })
    expect(sseClose).toHaveBeenCalledTimes(1)
    expect(result.current.problem).toBe('host_removed')
    expect(useExecutionStore.getState().executions[KEY]?.sse ?? 'closed').toBe('closed')
  })

  it('re-adding a removed host (keep-tabs delete then undo) restarts the chain from scratch', async () => {
    const hostRow = useHostStore.getState().hosts[H]
    const { result } = renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => { useHostStore.setState({ hosts: {}, hostOrder: [] }) })
    expect(result.current.problem).toBe('host_removed')
    vi.mocked(api.getExecution).mockClear()
    vi.mocked(api.attachObserve).mockClear()
    vi.mocked(sse.openNexSse).mockClear()
    vi.mocked(api.fetchExecutionEvents).mockReset().mockResolvedValueOnce({ items: [], next_cursor: 0 })
    act(() => { useHostStore.setState({ hosts: { [H]: hostRow }, hostOrder: [H] }) })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(result.current.problem).toBeNull()
    expect(api.getExecution).toHaveBeenCalledTimes(1)
    expect(api.attachObserve).toHaveBeenCalledTimes(1)
    expect(sse.openNexSse).toHaveBeenCalledTimes(1)
    expect(subscriptionSlots.isLive(H, KEY)).toBe(true)
  })

  // A run without its host has no cleanup, so it must not count as an instance holding the shared slot and entry.
  it('a pane whose host was missing and came back is the last instance: closing it frees the slot and idles the entry', async () => {
    const hostRow = useHostStore.getState().hosts[H]
    act(() => { useHostStore.setState({ hosts: {}, hostOrder: [] }) })
    const { result, unmount } = renderHook(() => useExecutionSubscription(H, E, true))
    expect(result.current.problem).toBe('host_removed')
    act(() => { useHostStore.setState({ hosts: { [H]: hostRow }, hostOrder: [H] }) })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => { sseOpts!.onStatus('open') })
    expect(subscriptionSlots.isLive(H, KEY)).toBe(true)
    unmount()
    expect(sseClose).toHaveBeenCalledTimes(1)
    expect(subscriptionSlots.isLive(H, KEY)).toBe(false)
    expect(useExecutionStore.getState().executions[KEY].sse).toBe('idle')
  })

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

  it('retries the whole chain with backoff after a non-terminal error, and completes once it succeeds', async () => {
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

  it('a site-wide reservation evicts the pane LRU and a later inactive claim cannot exceed three', async () => {
    const closes: Record<string, CloseMock> = {}
    vi.mocked(sse.openNexSse).mockImplementation((o) => {
      const id = new URL(o.url, 'http://x').searchParams.get('execution_id')!
      closes[id] = closes[id] ?? vi.fn<() => void>()
      return { close: closes[id] }
    })
    vi.mocked(api.attachObserve).mockImplementation(async (_h, id) => ({ mode: 'observe', stream_url: `/api/nex/v1/events?execution_id=${id}`, cursor: 0, state: 'idle' }))
    vi.mocked(api.fetchExecutionEvents).mockResolvedValue({ items: [], next_cursor: 0 })
    const ids = ['exc_a', 'exc_b', 'exc_c', 'exc_d']
    const hooks = ids.map((id) => renderHook(({ active }) => useExecutionSubscription(H, id, active), { initialProps: { active: true } }))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(Object.keys(closes)).toHaveLength(4)

    // the list store takes its lane → exactly one pane (the LRU, exc_a) pauses
    act(() => { subscriptionSlots.reserve(H, 'site-wide') })
    expect(closes['exc_a']).toHaveBeenCalledTimes(1)
    expect(useExecutionStore.getState().executions[`${H}:exc_a`].sse).toBe('paused')
    expect(hooks[0].result.current.paused).toBe(true)
    expect(ids.filter((id) => closes[id].mock.calls.length > 0)).toEqual(['exc_a'])
    expect(ids.filter((id) => useExecutionStore.getState().executions[`${H}:${id}`].sse === 'paused')).toEqual(['exc_a'])

    // a fifth pane mounted inactive cannot claim a fourth pane slot while the lane is held
    const fifth = renderHook(({ active }) => useExecutionSubscription(H, 'exc_e', active), { initialProps: { active: false } })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(fifth.result.current.paused).toBe(true)
    expect(closes['exc_e']).toBeUndefined()
    expect(useExecutionStore.getState().executions[`${H}:exc_e`].sse).toBe('paused')

    // lane released → the next activation goes live without evicting anyone (exc_a is inactive, so it ignores the notice)
    hooks[0].rerender({ active: false })
    act(() => { subscriptionSlots.unreserve(H, 'site-wide') })
    fifth.rerender({ active: true })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(fifth.result.current.paused).toBe(false)
    expect(closes['exc_e']).toBeDefined()
    expect(ids.filter((id) => closes[id].mock.calls.length > 0)).toEqual(['exc_a'])

    fifth.unmount(); hooks.forEach((h) => h.unmount())
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

  it('a terminal SSE close (with error) clears a send stuck in flight so the input is not locked forever', async () => {
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    useExecutionStore.getState().setPendingSend(H, E, true)

    act(() => { sseOpts!.onStatus('closed', new Error('unauthorized')) })

    expect(useExecutionStore.getState().executions[KEY].pendingSend).toBe(false)
  })
})

// T3.1 — nexen #83: after every (re)open of the scoped stream, re-read
// `/tasks?state=running` and merge it as a snapshot. Only on a daemon that
// advertises capabilities.worker_rollup; an older one gets no request.
describe('useExecutionSubscription — /tasks correction (#83)', () => {
  const rollup = { task_kinds: ['shell', 'subagent', 'other'], task_statuses: ['running'], activity_phases: ['model'], subagent_cost: false }
  const setRollup = (on: boolean) =>
    useNexHostStore.setState({ byHost: { [H]: { phase: 'ready', capabilities: on ? { worker_rollup: rollup } : {} } } as never })
  const task = (id: string, extra: Partial<WorkerTask> = {}): WorkerTask => ({
    task_id: id, turn_id: 't1', kind: 'shell', task_type: 'local_bash', tool_use_id: `tu_${id}`, parent_tool_use_id: null,
    description: id, backgrounded: true, status: 'running', provider_status: null, closed_by: null, started_at: 1000, ended_at: null, startSeq: 2, ...extra,
  })
  const snap = (items: WorkerTask[], cursor = 2): WorkerTasksSnapshot => ({ items, cursor })

  beforeEach(() => {
    vi.useFakeTimers()
    subscriptionSlots.resetForTests()
    useExecutionStore.setState({ executions: {} })
    useHostStore.setState({ hosts: { [H]: { id: H, name: 'A', ip: '1', port: 1 } } as never, hostOrder: [H], activeHostId: H, runtime: {} })
    setRollup(true)
    sseOpts = null
    sseClose = vi.fn<() => void>()
    vi.mocked(sse.openNexSse).mockReset().mockImplementation((o) => { sseOpts = o; return { close: sseClose } })
    vi.mocked(api.getExecution).mockReset().mockResolvedValue(summary())
    vi.mocked(api.attachObserve).mockReset().mockResolvedValue({ mode: 'observe', stream_url: '/api/nex/v1/events?execution_id=exc_1', cursor: 2, state: 'idle' })
    vi.mocked(api.fetchExecutionEvents).mockReset().mockResolvedValue({ items: [ev(1), ev(2)], next_cursor: 0 })
    vi.mocked(api.fetchExecutionTasks).mockReset().mockResolvedValue(snap([task('a')]))
  })
  afterEach(() => { vi.useRealTimers(); useNexHostStore.setState({ byHost: {} }) })

  it('fetches once on the first open and merges the snapshot', async () => {
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.fetchExecutionTasks).not.toHaveBeenCalled()
    act(() => { sseOpts!.onStatus('open') })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.fetchExecutionTasks).toHaveBeenCalledTimes(1)
    expect(api.fetchExecutionTasks).toHaveBeenCalledWith(H, E, 'running')
    expect(Object.keys(useExecutionStore.getState().executions[KEY].tasks)).toEqual(['a'])
  })

  it('fetches once on each reconnect', async () => {
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => { sseOpts!.onStatus('open') })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => { sseOpts!.onStatus('reconnecting'); sseOpts!.onStatus('open') })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => { sseOpts!.onStatus('reconnecting'); sseOpts!.onStatus('open') })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.fetchExecutionTasks).toHaveBeenCalledTimes(3)
  })

  it('an older daemon (no worker_rollup) makes no request and tasks stay empty', async () => {
    setRollup(false)
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => { sseOpts!.onStatus('open'); sseOpts!.onStatus('reconnecting'); sseOpts!.onStatus('open') })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.fetchExecutionTasks).not.toHaveBeenCalled()
    expect(useExecutionStore.getState().executions[KEY].tasks).toEqual({})
  })

  it('a snapshot arriving after a live task_end does not reopen the task', async () => {
    let resolve!: (v: WorkerTasksSnapshot) => void
    vi.mocked(api.fetchExecutionTasks).mockReset().mockImplementation(() => new Promise((r) => { resolve = r }))
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => {
      sseOpts!.onStatus('open')
      sseOpts!.onFrame({ id: '3', event: 'task_start', data: JSON.stringify({ task_id: 'a', turn_id: 't1', kind: 'shell', tool_use_id: 'tu_a', started_at: 1000 }) })
      sseOpts!.onFrame({ id: '4', event: 'task_end', data: JSON.stringify({ task_id: 'a', turn_id: 't1', kind: 'shell', tool_use_id: 'tu_a', status: 'completed', ended_at: 2000 }) })
    })
    await act(async () => { resolve(snap([task('a')], 3)); await vi.advanceTimersByTimeAsync(0) })
    expect(useExecutionStore.getState().executions[KEY].tasks.a.status).toBe('completed')
  })

  it('a kicked pane resuming on a new stream fetches again', async () => {
    const { rerender } = renderHook(({ active }) => useExecutionSubscription(H, E, active), { initialProps: { active: true } })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => { sseOpts!.onStatus('open') })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    const first = sseOpts
    // Four busier siblings take every live slot → this pane is evicted (paused).
    act(() => { for (const k of ['x1', 'x2', 'x3', 'x4']) subscriptionSlots.touch(H, `${H}:${k}`) })
    expect(useExecutionStore.getState().executions[KEY].sse).toBe('paused')
    rerender({ active: false })
    rerender({ active: true })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(sseOpts).not.toBe(first)
    act(() => { sseOpts!.onStatus('open') })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(api.fetchExecutionTasks).toHaveBeenCalledTimes(2)
  })

  it('drops a response that belongs to an earlier stream generation', async () => {
    const pending: ((v: WorkerTasksSnapshot) => void)[] = []
    vi.mocked(api.fetchExecutionTasks).mockReset().mockImplementation(() => new Promise((r) => { pending.push(r) }))
    renderHook(() => useExecutionSubscription(H, E, true))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => { sseOpts!.onStatus('open'); sseOpts!.onStatus('reconnecting'); sseOpts!.onStatus('open') })
    // The newer read answers first; the older one lands after it and must be ignored.
    await act(async () => { pending[1](snap([], 5)); await vi.advanceTimersByTimeAsync(0) })
    await act(async () => { pending[0](snap([task('stale')], 2)); await vi.advanceTimersByTimeAsync(0) })
    expect(useExecutionStore.getState().executions[KEY].tasks).toEqual({})
  })

  it('drops a response that lands after the execution changed', async () => {
    let resolve!: (v: WorkerTasksSnapshot) => void
    vi.mocked(api.fetchExecutionTasks).mockReset().mockImplementation(() => new Promise((r) => { resolve = r }))
    const { rerender } = renderHook(({ id }) => useExecutionSubscription(H, id, true), { initialProps: { id: E } })
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    act(() => { sseOpts!.onStatus('open') })
    rerender({ id: 'exc_2' })
    await act(async () => { resolve(snap([task('a')])); await vi.advanceTimersByTimeAsync(0) })
    expect(useExecutionStore.getState().executions[KEY]?.tasks ?? {}).toEqual({})
  })
})
