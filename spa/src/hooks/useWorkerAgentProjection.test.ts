// spa/src/hooks/useWorkerAgentProjection.test.ts
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useWorkerAgentProjection, startWorkerAgentProjection } from './useWorkerAgentProjection'
import { useAgentStore } from '../stores/useAgentStore'
import { useTabStore } from '../stores/useTabStore'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useHostStore } from '../stores/useHostStore'
import { compositeKey } from '../lib/composite-key'
import { shouldDispatch } from './useNotificationDispatcher'
import { STORAGE_KEYS } from '../lib/storage'
import { defaultExecutionState, type ExecutionState } from '../lib/nex/event-reducer'
import { emptyListCache } from '../lib/nex/execution-list-effects'
import type { ExecutionSummary, NexEvent, WorkerTask } from '../lib/nex/types'
import type { StreamMessage } from '../lib/nex/message-types'
import type { Tab } from '../types/tab'

const H = 'host-a'
const E = 'E1'
const KEY = compositeKey(H, 'exec:E1')

const summary = (over: Partial<ExecutionSummary> = {}): ExecutionSummary =>
  ({ id: E, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'b', labels: {},
    created_at: 1, updated_at: 5, duration_ms: null, event_count: 0, observers: 0, archived: false, ...over }) as ExecutionSummary

const execTab = (id = 't-exec', host: string | undefined = H, executionId = E): Tab => ({
  id, pinned: false, locked: false, createdAt: 0,
  layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'execution', executionId, ...(host ? { host } : {}) } } },
})

function setLive(patch: Partial<ExecutionState>) {
  useExecutionStore.setState((s) => ({
    executions: { ...s.executions, [executionKey(H, E)]: { ...(s.executions[executionKey(H, E)] ?? defaultExecutionState()), ...patch } },
  }))
}

const assistant = (text: string, parent: string | null = null): StreamMessage =>
  ({ type: 'assistant', parent_tool_use_id: parent, message: { role: 'assistant', content: [{ type: 'text', text }], stop_reason: null } }) as StreamMessage

let listUnsub: ReturnType<typeof vi.fn<() => void>>
let listSubscribe: ReturnType<typeof vi.fn<(hostId: string) => () => void>>

beforeEach(() => {
  useAgentStore.setState({ statuses: {}, agentTypes: {}, models: {}, subagents: {}, lastEvents: {}, oscTitles: {}, ccStatus: {}, unread: {} })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useExecutionStore.setState({ executions: {} })
  listUnsub = vi.fn<() => void>()
  listSubscribe = vi.fn<(hostId: string) => () => void>(() => listUnsub)
  useExecutionListStore.setState({ byHost: {}, subscribe: listSubscribe })
  useNexHostStore.setState({ ensure: vi.fn<(hostId: string) => Promise<void>>().mockResolvedValue(undefined) })
  useHostStore.setState({ hostOrder: [H] })
})

afterEach(() => vi.restoreAllMocks())

describe('useWorkerAgentProjection', () => {
  it('projects a live worker: running → idle (unread, Stop) → error (StopFailure)', () => {
    const { unmount } = renderHook(() => useWorkerAgentProjection())
    setLive({ summary: summary({ state: 'running' }), turnLive: true, turnStarts: [0], turnMeta: [{ startAt: 10, endAt: null, outcome: null, durationMs: null }] })
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'], activeTabId: null })

    const st = () => useAgentStore.getState()
    expect(st().statuses[KEY]).toBe('running')
    expect(st().agentTypes[KEY]).toBe('cc')
    expect(st().lastEvents[KEY].raw_event_name).toBe('UserPromptSubmit')

    setLive({
      summary: summary({ state: 'idle' }), turnLive: false,
      messages: [assistant('first'), assistant('sub', 'tu1'), assistant('x'.repeat(400)), assistant('sub again', 'tu2')],
      turnMeta: [{ startAt: 10, endAt: 20, outcome: 'ok', durationMs: 10 }],
    })
    expect(st().statuses[KEY]).toBe('idle')
    expect(st().unread[KEY]).toBe(true)
    expect(st().lastEvents[KEY].raw_event_name).toBe('Stop')
    expect(st().lastEvents[KEY].detail?.last_assistant_message).toBe('x'.repeat(300))

    setLive({
      summary: summary({ state: 'failed' }),
      messages: [{ type: 'result', subtype: 'error_max_turns', is_error: true } as StreamMessage],
      turnMeta: [{ startAt: 10, endAt: 30, outcome: 'failed', durationMs: 20 }],
    })
    expect(st().statuses[KEY]).toBe('error')
    expect(st().lastEvents[KEY].raw_event_name).toBe('StopFailure')
    expect(st().lastEvents[KEY].detail?.error).toBe('error_max_turns')
    unmount()
  })

  it('StopFailure without a failing result carries the lifecycle reason', () => {
    const stop = startWorkerAgentProjection()
    setLive({ summary: summary({ state: 'failed', last_turn_reason: 'auth_failed' }), turnStarts: [0], turnMeta: [{ startAt: 1, endAt: 2, outcome: 'failed', durationMs: 1 }] })
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
    expect(useAgentStore.getState().lastEvents[KEY].detail?.error).toBe('auth_failed')
    stop()
  })

  it('running subagent tasks become native subagent refs', () => {
    const stop = startWorkerAgentProjection()
    const task = (id: string, kind: WorkerTask['kind'], status: WorkerTask['status']): WorkerTask =>
      ({ task_id: id, turn_id: 't', kind, task_type: 'x', tool_use_id: null, parent_tool_use_id: null, description: '', backgrounded: false,
        status, provider_status: null, closed_by: null, started_at: 3, ended_at: null, startSeq: 1, subagent_type: 'Explore' })
    setLive({ summary: summary({ state: 'running' }), turnLive: true,
      tasks: { a: task('a', 'subagent', 'running'), b: task('b', 'shell', 'running'), c: task('c', 'subagent', 'completed') } })
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
    expect(useAgentStore.getState().subagents[KEY].map((s) => s.id)).toEqual(['a'])
    expect(useAgentStore.getState().subagents[KEY][0].type).toBe('Explore')
    stop()
  })

  it('falls back to the host list row when there is no live state', () => {
    const stop = startWorkerAgentProjection()
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
    expect(useAgentStore.getState().statuses[KEY]).toBeUndefined()
    useExecutionListStore.setState({ byHost: { [H]: { ...emptyListCache(), items: [summary({ state: 'running', provider: 'codex', turn_count: 1 })] } } })
    expect(useAgentStore.getState().statuses[KEY]).toBe('running')
    expect(useAgentStore.getState().agentTypes[KEY]).toBe('codex')
    useExecutionListStore.setState({ byHost: { [H]: { ...emptyListCache(), items: [summary({ state: 'failed', provider: 'codex', turn_count: 1, last_turn_reason: 'orphaned' })] } } })
    expect(useAgentStore.getState().statuses[KEY]).toBe('error')
    expect(useAgentStore.getState().lastEvents[KEY].detail?.error).toBe('orphaned')
    stop()
  })

  it('a pane without a host hint resolves to the first host', () => {
    const stop = startWorkerAgentProjection()
    setLive({ summary: summary({ state: 'running' }), turnLive: true })
    useTabStore.setState({ tabs: { 't-exec': execTab('t-exec', undefined) }, tabOrder: ['t-exec'] })
    expect(useAgentStore.getState().statuses[KEY]).toBe('running')
    stop()
  })

  it('a pane with an empty-string host hint resolves to the first host, same as no hint', () => {
    const stop = startWorkerAgentProjection()
    try {
      setLive({ summary: summary({ state: 'running' }), turnLive: true })
      const emptyHostTab: Tab = {
        id: 't-exec', pinned: false, locked: false, createdAt: 0,
        layout: { type: 'leaf', pane: { id: 'p-t-exec', content: { kind: 'execution', executionId: E, host: '' } } },
      }
      useTabStore.setState({ tabs: { 't-exec': emptyHostTab }, tabOrder: ['t-exec'] })
      expect(useAgentStore.getState().statuses[KEY]).toBe('running')
    } finally {
      stop()
    }
  })

  it('keeps one list subscription per host with a worker tab and releases it with the last tab', () => {
    const stop = startWorkerAgentProjection()
    useTabStore.setState({ tabs: { t1: execTab('t1'), t2: execTab('t2', H, 'E2') }, tabOrder: ['t1', 't2'] })
    expect(listSubscribe).toHaveBeenCalledTimes(1)
    expect(listSubscribe).toHaveBeenCalledWith(H)
    useTabStore.setState({ tabs: { t2: execTab('t2', H, 'E2') }, tabOrder: ['t2'] })
    expect(listUnsub).not.toHaveBeenCalled()
    useTabStore.setState({ tabs: {}, tabOrder: [] })
    expect(listUnsub).toHaveBeenCalledTimes(1)
    stop()
  })

  it('closing the worker tab clears its agent key', () => {
    const stop = startWorkerAgentProjection()
    setLive({ summary: summary({ state: 'idle' }) })
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
    expect(useAgentStore.getState().statuses[KEY]).toBe('idle')
    useTabStore.setState({ tabs: {}, tabOrder: [] })
    expect(useAgentStore.getState().statuses[KEY]).toBeUndefined()
    expect(useAgentStore.getState().lastEvents[KEY]).toBeUndefined()
    expect(useAgentStore.getState().unread[KEY]).toBeUndefined()
    stop()
  })

  it('the same execution open in two tabs keeps its key when one tab closes', () => {
    const stop = startWorkerAgentProjection()
    setLive({ summary: summary({ state: 'idle' }) })
    useTabStore.setState({ tabs: { t1: execTab('t1'), t2: execTab('t2') }, tabOrder: ['t1', 't2'] })
    expect(useAgentStore.getState().statuses[KEY]).toBe('idle')
    useTabStore.setState({ tabs: { t2: execTab('t2') }, tabOrder: ['t2'] })
    expect(useAgentStore.getState().statuses[KEY]).toBe('idle')
    expect(useAgentStore.getState().lastEvents[KEY].raw_event_name).toBe('Stop')
    stop()
  })

  it('an unchanged projection dispatches once', () => {
    const spy = vi.spyOn(useAgentStore.getState(), 'handleNormalizedEvent')
    const stop = startWorkerAgentProjection()
    setLive({ summary: summary({ state: 'running' }), turnLive: true })
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
    setLive({ lastSeq: 7, messages: [assistant('streaming')] })
    setLive({ lastSeq: 8 })
    expect(spy).toHaveBeenCalledTimes(1)
    setLive({ turnLive: false, summary: summary({ state: 'idle' }), turnStarts: [0], turnMeta: [{ startAt: 1, endAt: 2, outcome: 'ok', durationMs: 1 }] })
    expect(spy).toHaveBeenCalledTimes(2)
    stop()
  })

  it('broadcast_ts is stable across restarts for the same state (no replay notification)', () => {
    let stop = startWorkerAgentProjection()
    setLive({ summary: summary({ state: 'idle' }), turnStarts: [0], turnMeta: [{ startAt: 100, endAt: 200, outcome: 'ok', durationMs: 100 }] })
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
    const first = useAgentStore.getState().lastEvents[KEY].broadcast_ts
    expect(first).toBe(200)
    stop()
    stop = startWorkerAgentProjection()
    expect(useAgentStore.getState().lastEvents[KEY].broadcast_ts).toBe(first)
    stop()
  })

  describe('list-row stamps across a reload', () => {
    const listRow = (over: Partial<ExecutionSummary>) =>
      useExecutionListStore.setState({ byHost: { [H]: { ...emptyListCache(), items: [summary({ turn_count: 1, ...over })] } } })
    const resetStores = () => {
      useAgentStore.setState({ statuses: {}, agentTypes: {}, models: {}, subagents: {}, lastEvents: {}, oscTitles: {}, ccStatus: {}, unread: {} })
      useExecutionStore.setState({ executions: {} })
      useExecutionListStore.setState({ byHost: {} })
    }
    beforeEach(() => localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN))
    afterEach(() => localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN))

    it('a first list-row projection after a reload is a 0 baseline, never a replayed Stop', () => {
      let stop = startWorkerAgentProjection()
      setLive({ summary: summary({ state: 'idle' }), turnStarts: [0], turnMeta: [{ startAt: 100, endAt: 200, outcome: 'ok', durationMs: 100 }] })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      expect(useAgentStore.getState().lastEvents[KEY].broadcast_ts).toBe(200)
      shouldDispatch(KEY, 200) // the dispatcher records the stamp it saw
      stop()
      // Reload: in-memory stores are fresh, the dispatcher's seen map (localStorage) survives.
      resetStores()
      stop = startWorkerAgentProjection()
      // Nexen bumped updated_at (lease renew) without a status change.
      listRow({ state: 'idle', updated_at: 500 })
      const ev = useAgentStore.getState().lastEvents[KEY]
      expect(ev.raw_event_name).toBe('Stop')
      expect(ev.broadcast_ts).toBe(0)
      expect(shouldDispatch(KEY, ev.broadcast_ts)).toBe(false)
      stop()
    })

    it('a later list-row transition within the session stamps updated_at and notifies', () => {
      localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [KEY]: 200 }))
      const stop = startWorkerAgentProjection()
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      listRow({ state: 'running', updated_at: 300 })
      expect(useAgentStore.getState().lastEvents[KEY].broadcast_ts).toBe(0)
      listRow({ state: 'idle', updated_at: 600 })
      const ev = useAgentStore.getState().lastEvents[KEY]
      expect(ev.raw_event_name).toBe('Stop')
      expect(ev.broadcast_ts).toBe(600)
      expect(shouldDispatch(KEY, ev.broadcast_ts)).toBe(true)
      stop()
    })

    it('a first projection from the live source after a reload keeps the turn stamp', () => {
      const stop = startWorkerAgentProjection()
      setLive({ summary: summary({ state: 'idle', updated_at: 900 }), turnStarts: [0], turnMeta: [{ startAt: 100, endAt: 200, outcome: 'ok', durationMs: 100 }] })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      expect(useAgentStore.getState().lastEvents[KEY].broadcast_ts).toBe(200)
      stop()
    })
  })

  describe('queued sends (A1)', () => {
    let seq = 0
    const ev = (kind: string, payload: Record<string, unknown> = {}, created_at = 0): NexEvent =>
      ({ seq: ++seq, execution_id: E, kind, payload, created_at: created_at || seq * 10 })
    const apply = (...evs: NexEvent[]) => useExecutionStore.getState().applyEvents(H, E, evs)
    const names = (spy: { mock: { calls: unknown[][] } }) =>
      spy.mock.calls.map((c) => (c[2] as { raw_event_name: string }).raw_event_name)

    const runToTaEnd = () => {
      seq = 0
      const spy = vi.spyOn(useAgentStore.getState(), 'handleNormalizedEvent')
      spy.mockClear() // an earlier test's spy may be carried over on the store state
      const stop = startWorkerAgentProjection()
      setLive({ summary: summary({ state: 'running' }), sse: 'open' })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      apply(ev('execution.message_accepted', { text: 'a', turn_id: 'tA' }), ev('execution.running', { turn_id: 'tA' }))
      apply(ev('execution.message_accepted', { text: 'b', turn_id: 'tB' }))
      apply(ev('assistant', { type: 'assistant', parent_tool_use_id: null, message: { role: 'assistant', content: [{ type: 'text', text: 'A done' }], stop_reason: null } }))
      apply(ev('result', { type: 'result', subtype: 'success', is_error: false }))
      apply(ev('execution.terminal', { turn_id: 'tA', reason: 'final_response' }))
      // The refetched summary may already read idle while tB waits in the queue.
      setLive({ summary: summary({ state: 'idle' }) })
      return { spy, stop }
    }

    it('never projects idle between the first turn ending and the queued turn starting', () => {
      const { spy, stop } = runToTaEnd()
      expect(useAgentStore.getState().statuses[KEY]).toBe('running')
      expect(names(spy)).not.toContain('Stop')
      apply(ev('execution.running', { turn_id: 'tB' }))
      expect(names(spy)).toEqual(['UserPromptSubmit'])
      stop()
    })

    it('the queued turn ending ok dispatches Stop once', () => {
      const { spy, stop } = runToTaEnd()
      apply(ev('execution.running', { turn_id: 'tB' }))
      apply(ev('result', { type: 'result', subtype: 'success', is_error: false }))
      apply(ev('execution.terminal', { turn_id: 'tB', reason: 'final_response' }))
      expect(useAgentStore.getState().statuses[KEY]).toBe('idle')
      expect(names(spy)).toEqual(['UserPromptSubmit', 'Stop'])
      stop()
    })

    it('the queued turn failing dispatches StopFailure', () => {
      const { spy, stop } = runToTaEnd()
      apply(ev('execution.running', { turn_id: 'tB' }))
      apply(ev('execution.terminal', { turn_id: 'tB', reason: 'error' }))
      expect(useAgentStore.getState().statuses[KEY]).toBe('error')
      expect(names(spy)).toEqual(['UserPromptSubmit', 'StopFailure'])
      stop()
    })
  })

  describe('evicted live subscription (A2)', () => {
    const listRow = (over: Partial<ExecutionSummary>) =>
      useExecutionListStore.setState({ byHost: { [H]: { ...emptyListCache(), items: [summary({ turn_count: 1, ...over })] } } })
    const runningLive = () => setLive({ summary: summary({ state: 'running' }), turnLive: true, turnStarts: [0],
      turnMeta: [{ startAt: 10, endAt: null, outcome: null, durationMs: null }], sse: 'open' })

    it('a paused (evicted) pane follows the list row, and the live stream again once resumed', () => {
      const stop = startWorkerAgentProjection()
      runningLive()
      listRow({ state: 'running', updated_at: 20 })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      const st = () => useAgentStore.getState()
      expect(st().statuses[KEY]).toBe('running')

      // Eviction: SSE closed, store entry kept frozen at running.
      useExecutionStore.getState().setSse(H, E, 'paused')
      listRow({ state: 'idle', updated_at: 30 })
      expect(st().statuses[KEY]).toBe('idle')
      expect(st().lastEvents[KEY].raw_event_name).toBe('Stop')

      // Resuming but not delivering yet: the frozen running must not win.
      useExecutionStore.getState().setSse(H, E, 'connecting')
      expect(st().statuses[KEY]).toBe('idle')

      // Live again: a new turn runs on the live stream while the row still reads idle.
      setLive({ sse: 'open', turnStarts: [0, 1], turnMeta: [{ startAt: 10, endAt: 25, outcome: 'ok', durationMs: 15 }, { startAt: 40, endAt: null, outcome: null, durationMs: null }] })
      expect(st().statuses[KEY]).toBe('running')
      stop()
    })

    it('while evicted, a row that advances again keeps winning over the frozen live state', () => {
      const stop = startWorkerAgentProjection()
      runningLive()
      setLive({ lastEventAt: 15 })
      listRow({ state: 'running', updated_at: 20 })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      const st = () => useAgentStore.getState()
      useExecutionStore.getState().setSse(H, E, 'paused')
      listRow({ state: 'idle', updated_at: 30 })
      expect(st().statuses[KEY]).toBe('idle')
      listRow({ state: 'running', updated_at: 50 })
      expect(st().statuses[KEY]).toBe('running')
      listRow({ state: 'failed', updated_at: 60, last_turn_reason: 'orphaned' })
      expect(st().statuses[KEY]).toBe('error')
      stop()
    })

    describe('source choice by freshness (non-open SSE)', () => {
      const names = (spy: { mock: { calls: unknown[][] } }) =>
        spy.mock.calls.map((c) => (c[2] as { raw_event_name: string }).raw_event_name)
      const spyDispatch = () => {
        const spy = vi.spyOn(useAgentStore.getState(), 'handleNormalizedEvent')
        spy.mockClear()
        return spy
      }
      // Live snapshot is fresh as of t=100 (its last applied event).
      const freshRunningLive = (sse: ExecutionState['sse']) =>
        setLive({ summary: summary({ state: 'running', updated_at: 40 }), turnLive: true, turnStarts: [0],
          turnMeta: [{ startAt: 10, endAt: null, outcome: null, durationMs: null }], lastEventAt: 100, sse })

      it('open: the live state wins regardless of a newer list row', () => {
        const stop = startWorkerAgentProjection()
        freshRunningLive('open')
        listRow({ state: 'idle', updated_at: 500 })
        useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
        expect(useAgentStore.getState().statuses[KEY]).toBe('running')
        stop()
      })

      it('a brief reconnect with a row older than the live snapshot stays live (no Stop / UserPromptSubmit flip)', () => {
        const spy = spyDispatch()
        const stop = startWorkerAgentProjection()
        freshRunningLive('open')
        // Row is older than the last live event (100), though newer than the live summary (40).
        listRow({ state: 'idle', updated_at: 80 })
        useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
        useExecutionStore.getState().setSse(H, E, 'reconnecting')
        expect(useAgentStore.getState().statuses[KEY]).toBe('running')
        useExecutionStore.getState().setSse(H, E, 'open')
        expect(useAgentStore.getState().statuses[KEY]).toBe('running')
        expect(names(spy)).toEqual(['UserPromptSubmit'])
        stop()
      })

      it('a row older than a refetched live summary also stays live', () => {
        const stop = startWorkerAgentProjection()
        freshRunningLive('reconnecting')
        setLive({ summary: summary({ state: 'running', updated_at: 200 }) })
        listRow({ state: 'idle', updated_at: 150 })
        useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
        expect(useAgentStore.getState().statuses[KEY]).toBe('running')
        stop()
      })

      it('an equally fresh row does not displace the live state', () => {
        const stop = startWorkerAgentProjection()
        freshRunningLive('reconnecting')
        listRow({ state: 'idle', updated_at: 100 })
        useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
        expect(useAgentStore.getState().statuses[KEY]).toBe('running')
        stop()
      })

      it.each(['reconnecting', 'paused', 'connecting', 'closed'] as const)('%s with a row newer than the live snapshot: the row wins', (sse) => {
        const stop = startWorkerAgentProjection()
        freshRunningLive(sse)
        listRow({ state: 'idle', updated_at: 101 })
        useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
        expect(useAgentStore.getState().statuses[KEY]).toBe('idle')
        stop()
      })
    })

    it('a closed live stream falls back to the list row', () => {
      const stop = startWorkerAgentProjection()
      runningLive()
      listRow({ state: 'running', updated_at: 20 })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      useExecutionStore.getState().setSse(H, E, 'closed', 'boom')
      listRow({ state: 'failed', updated_at: 30, last_turn_reason: 'orphaned' })
      expect(useAgentStore.getState().statuses[KEY]).toBe('error')
      stop()
    })

    it('a paused pane with no list row keeps its live state as the source', () => {
      const stop = startWorkerAgentProjection()
      runningLive()
      useExecutionStore.getState().setSse(H, E, 'paused')
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      expect(useAgentStore.getState().statuses[KEY]).toBe('running')
      stop()
    })
  })

  it('stop releases subscriptions and clears the keys it set', () => {
    const stop = startWorkerAgentProjection()
    setLive({ summary: summary({ state: 'idle' }) })
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
    stop()
    expect(listUnsub).toHaveBeenCalledTimes(1)
    expect(useAgentStore.getState().statuses[KEY]).toBeUndefined()
    setLive({ summary: summary({ state: 'running' }), turnLive: true })
    expect(useAgentStore.getState().statuses[KEY]).toBeUndefined()
  })
})
