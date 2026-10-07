// spa/src/hooks/useWorkerAgentProjection.test.ts
// A worker's status comes ONLY from the host's list row (`useExecutionListStore`); the pane's live entry
// (`useExecutionStore`) is decoration — subagent refs, a Stop / StopFailure detail — while its stream is open, and the
// status source only when the host's list is truncated and has no row for the worker.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useWorkerAgentProjection, startWorkerAgentProjection as startProjection } from './useWorkerAgentProjection'
import { useAgentStore } from '../stores/useAgentStore'
import { useTabStore } from '../stores/useTabStore'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useHostStore } from '../stores/useHostStore'
import { compositeKey } from '../lib/composite-key'
import { shouldDispatch, useNotificationDispatcher } from './useNotificationDispatcher'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'
import { STORAGE_KEYS } from '../lib/storage'
import { defaultExecutionState, type ExecutionState } from '../lib/nex/event-reducer'
import { emptyListCache } from '../lib/nex/execution-list-effects'
import type { ExecutionSummary, NexEvent, WorkerTask } from '../lib/nex/types'
import type { StreamMessage } from '../lib/nex/message-types'
import type { Tab } from '../types/tab'

const H = 'host-a'
const E = 'E1'
const KEY = compositeKey(H, 'exec-E1')

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

/** The host's list answered with this worker's row (and only it). */
const listRow = (over: Partial<ExecutionSummary>) =>
  useExecutionListStore.setState({ byHost: { [H]: { ...emptyListCache(), phase: 'ready', items: [summary({ turn_count: 1, ...over })] } } })

/** The host's list answered without this worker's row; `truncated`: the walk hit its page cap. */
const listWithoutRow = (truncated = false) =>
  useExecutionListStore.setState({ byHost: { [H]: { ...emptyListCache(), phase: 'ready', truncated, items: [summary({ id: 'E-other' })] } } })

const assistant = (text: string, parent: string | null = null): StreamMessage =>
  ({ type: 'assistant', parent_tool_use_id: parent, message: { role: 'assistant', content: [{ type: 'text', text }], stop_reason: null } }) as StreamMessage

const subagentTask = (id: string, status: WorkerTask['status'] = 'running'): WorkerTask =>
  ({ task_id: id, turn_id: 't', kind: 'subagent', task_type: 'x', tool_use_id: null, parent_tool_use_id: null, description: '',
    backgrounded: true, status, provider_status: null, closed_by: null, started_at: 3, ended_at: null, startSeq: 1, subagent_type: 'Explore' })

const names = (spy: { mock: { calls: unknown[][] } }) =>
  spy.mock.calls.map((c) => (c[2] as { raw_event_name: string }).raw_event_name)

const spyDispatch = () => {
  const spy = vi.spyOn(useAgentStore.getState(), 'handleNormalizedEvent')
  spy.mockClear() // an earlier test's spy may be carried over on the store state
  return spy
}

/** Every projection a test starts is stopped after it, even one whose assertion threw (stop is idempotent). */
const started: Array<() => void> = []
function startWorkerAgentProjection(): () => void {
  const stop = startProjection()
  started.push(stop)
  return stop
}

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

afterEach(() => {
  for (const stop of started.splice(0)) stop()
  vi.restoreAllMocks()
})

describe('useWorkerAgentProjection', () => {
  it('projects a worker from its list row: running → idle (unread, Stop) → error (StopFailure)', () => {
    const { unmount } = renderHook(() => useWorkerAgentProjection())
    listRow({ state: 'running', updated_at: 10 })
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'], activeTabId: null })

    const st = () => useAgentStore.getState()
    expect(st().statuses[KEY]).toBe('running')
    expect(st().agentTypes[KEY]).toBe('cc')
    expect(st().lastEvents[KEY].raw_event_name).toBe('UserPromptSubmit')

    // The pane's stream delivers the turn's end first: it decides nothing — but its last reply decorates the Stop.
    setLive({
      sse: 'open', summary: summary({ state: 'idle' }), turnStarts: [0],
      messages: [assistant('first'), assistant('sub', 'tu1'), assistant('x'.repeat(400)), assistant('sub again', 'tu2')],
      turnMeta: [{ startAt: 10, endAt: 20, outcome: 'ok', durationMs: 10 }],
    })
    expect(st().statuses[KEY]).toBe('running')
    listRow({ state: 'idle', updated_at: 20 })
    expect(st().statuses[KEY]).toBe('idle')
    expect(st().unread[KEY]).toBe(true)
    expect(st().lastEvents[KEY].raw_event_name).toBe('Stop')
    expect(st().lastEvents[KEY].detail?.last_assistant_message).toBe('x'.repeat(300))

    setLive({ messages: [{ type: 'result', subtype: 'error_max_turns', is_error: true } as StreamMessage] })
    listRow({ state: 'failed', updated_at: 30 })
    expect(st().statuses[KEY]).toBe('error')
    expect(st().lastEvents[KEY].raw_event_name).toBe('StopFailure')
    expect(st().lastEvents[KEY].detail?.error).toBe('error_max_turns')
    unmount()
  })

  it('a frozen live entry (stream not open) decorates nothing: a Stop without its reply', () => {
    const stop = startWorkerAgentProjection()
    listRow({ state: 'running', updated_at: 10 })
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
    // The pane unmounted mid-turn: its entry holds an older reply that is not this turn's last.
    setLive({ sse: 'idle', summary: summary({ state: 'running' }), turnStarts: [0], messages: [assistant('let me look')] })
    listRow({ state: 'idle', updated_at: 20 })
    expect(useAgentStore.getState().lastEvents[KEY].raw_event_name).toBe('Stop')
    expect(useAgentStore.getState().lastEvents[KEY].detail).toEqual({})
    stop()
  })

  it('StopFailure without a failing result carries the lifecycle reason', () => {
    const stop = startWorkerAgentProjection()
    listRow({ state: 'failed', last_turn_reason: 'auth_failed' })
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
    expect(useAgentStore.getState().lastEvents[KEY].detail?.error).toBe('auth_failed')
    stop()
  })

  it('running subagent tasks of a delivering pane stream become native subagent refs (decoration)', () => {
    const stop = startWorkerAgentProjection()
    const task = (id: string, kind: WorkerTask['kind'], status: WorkerTask['status']): WorkerTask =>
      ({ ...subagentTask(id, status), kind })
    listRow({ state: 'running' })
    setLive({ sse: 'open', summary: summary({ state: 'running' }), turnLive: true,
      tasks: { a: task('a', 'subagent', 'running'), b: task('b', 'shell', 'running'), c: task('c', 'subagent', 'completed') } })
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
    expect(useAgentStore.getState().subagents[KEY].map((s) => s.id)).toEqual(['a'])
    expect(useAgentStore.getState().subagents[KEY][0].type).toBe('Explore')

    // The stream stops delivering (the pane unmounted): no decoration.
    setLive({ sse: 'idle' })
    expect(useAgentStore.getState().subagents[KEY]).toBeUndefined()
    expect(useAgentStore.getState().statuses[KEY]).toBe('running')
    stop()
  })

  it('a decoration-only change (subagent refs) writes the refs and dispatches nothing: no stamp, no unread', () => {
    const stop = startWorkerAgentProjection()
    listRow({ state: 'running', updated_at: 100 })
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
    listRow({ state: 'idle', updated_at: 300 })
    expect(useAgentStore.getState().lastEvents[KEY].broadcast_ts).toBe(300)
    useAgentStore.getState().markRead(H, 'exec-E1')
    // A lease renew bumps the row's updated_at with no status change.
    listRow({ state: 'idle', updated_at: 400 })
    const spy = spyDispatch()

    setLive({ sse: 'open', summary: summary({ state: 'idle' }), tasks: { a: subagentTask('a') } })
    expect(useAgentStore.getState().subagents[KEY].map((s) => s.id)).toEqual(['a'])
    setLive({ sse: 'idle' })
    expect(useAgentStore.getState().subagents[KEY]).toBeUndefined()

    expect(spy).not.toHaveBeenCalled()
    expect(useAgentStore.getState().lastEvents[KEY].broadcast_ts).toBe(300)
    expect(useAgentStore.getState().unread[KEY]).toBeUndefined()
    expect(useAgentStore.getState().statuses[KEY]).toBe('idle')
    stop()
  })

  it('reads the host list row (no live state needed)', () => {
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
    listRow({ state: 'running' })
    useTabStore.setState({ tabs: { 't-exec': execTab('t-exec', undefined) }, tabOrder: ['t-exec'] })
    expect(useAgentStore.getState().statuses[KEY]).toBe('running')
    stop()
  })

  it('a pane with an empty-string host hint resolves to the first host, same as no hint', () => {
    const stop = startWorkerAgentProjection()
    try {
      listRow({ state: 'running' })
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
    listRow({ state: 'idle' })
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
    listRow({ state: 'idle' })
    useTabStore.setState({ tabs: { t1: execTab('t1'), t2: execTab('t2') }, tabOrder: ['t1', 't2'] })
    expect(useAgentStore.getState().statuses[KEY]).toBe('idle')
    useTabStore.setState({ tabs: { t2: execTab('t2') }, tabOrder: ['t2'] })
    expect(useAgentStore.getState().statuses[KEY]).toBe('idle')
    expect(useAgentStore.getState().lastEvents[KEY].raw_event_name).toBe('Stop')
    stop()
  })

  it('an unchanged projection dispatches once: live churn and a same-state row refresh dispatch nothing', () => {
    const spy = spyDispatch()
    const stop = startWorkerAgentProjection()
    listRow({ state: 'running', updated_at: 10 })
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
    setLive({ sse: 'open', summary: summary({ state: 'running' }), lastSeq: 7, messages: [assistant('streaming')] })
    setLive({ lastSeq: 8 })
    listRow({ state: 'running', updated_at: 15 })
    expect(spy).toHaveBeenCalledTimes(1)
    listRow({ state: 'idle', updated_at: 20 })
    expect(spy).toHaveBeenCalledTimes(2)
    stop()
  })

  describe('every execution pane, not just the primary one (controller ruling, spec L1)', () => {
    const splitTab = (workerFirst: boolean): Tab => {
      const worker = { type: 'leaf' as const, pane: { id: 'p-w', content: { kind: 'execution' as const, executionId: E, host: H } } }
      const other = { type: 'leaf' as const, pane: { id: 'p-o', content: { kind: 'new-tab' as const } } }
      return {
        id: 't-split', pinned: false, locked: false, createdAt: 0,
        layout: { type: 'split', id: 's1', direction: 'h', sizes: [50, 50], children: workerFirst ? [worker, other] : [other, worker] },
      }
    }

    it('a worker as the second pane of a split is still projected (status in store, unread when not active)', () => {
      const stop = startWorkerAgentProjection()
      try {
        listRow({ state: 'running', updated_at: 10 })
        useTabStore.setState({ tabs: { 't-split': splitTab(false) }, tabOrder: ['t-split'] })
        expect(useAgentStore.getState().statuses[KEY]).toBe('running')
        listRow({ state: 'idle', updated_at: 20 })
        const st = useAgentStore.getState()
        expect(st.statuses[KEY]).toBe('idle')
        expect(st.unread[KEY]).toBe(true)
        expect(st.lastEvents[KEY].raw_event_name).toBe('Stop')
      } finally {
        stop()
      }
    })

    it('a worker as the primary pane of a split tab is projected the same way', () => {
      const stop = startWorkerAgentProjection()
      try {
        listRow({ state: 'running', updated_at: 10 })
        useTabStore.setState({ tabs: { 't-split': splitTab(true) }, tabOrder: ['t-split'] })
        expect(useAgentStore.getState().statuses[KEY]).toBe('running')
        listRow({ state: 'idle', updated_at: 20 })
        expect(useAgentStore.getState().unread[KEY]).toBe(true)
        expect(useAgentStore.getState().lastEvents[KEY].raw_event_name).toBe('Stop')
      } finally {
        stop()
      }
    })
  })

  describe('list-row stamps across a reload', () => {
    const resetStores = () => {
      useAgentStore.setState({ statuses: {}, agentTypes: {}, models: {}, subagents: {}, lastEvents: {}, oscTitles: {}, ccStatus: {}, unread: {} })
      useExecutionStore.setState({ executions: {} })
      useExecutionListStore.setState({ byHost: {} })
    }
    beforeEach(() => localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN))
    afterEach(() => localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN))

    it('a first list-row projection after a reload is a 0 baseline, never a replayed Stop', () => {
      let stop = startWorkerAgentProjection()
      listRow({ state: 'running', updated_at: 100 })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      listRow({ state: 'idle', updated_at: 200 })
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

    it('an open live entry beside the row changes no stamp: the first projection is still the 0 baseline', () => {
      const stop = startWorkerAgentProjection()
      setLive({ sse: 'open', summary: summary({ state: 'idle', updated_at: 900 }), turnStarts: [0], turnMeta: [{ startAt: 100, endAt: 200, outcome: 'ok', durationMs: 100 }] })
      listRow({ state: 'idle', updated_at: 900 })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      expect(useAgentStore.getState().lastEvents[KEY].broadcast_ts).toBe(0)
      stop()
    })
  })

  // The ONE fallback: the host's list hit its page cap and has no row for the worker — then the live entry decides,
  // exactly as it did before the list was the one source (per turn: queued sends, A1; turn stamps).
  describe('a truncated list without the row: the live entry is the source', () => {
    let seq = 0
    const ev = (kind: string, payload: Record<string, unknown> = {}, created_at = 0): NexEvent =>
      ({ seq: ++seq, execution_id: E, kind, payload, created_at: created_at || seq * 10 })
    const apply = (...evs: NexEvent[]) => useExecutionStore.getState().applyEvents(H, E, evs)

    const runToTaEnd = () => {
      seq = 0
      const spy = spyDispatch()
      const stop = startWorkerAgentProjection()
      listWithoutRow(true)
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

    it('keeps the live turn stamps', () => {
      const stop = startWorkerAgentProjection()
      listWithoutRow(true)
      setLive({ summary: summary({ state: 'idle', updated_at: 900 }), turnStarts: [0], turnMeta: [{ startAt: 100, endAt: 200, outcome: 'ok', durationMs: 100 }] })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      expect(useAgentStore.getState().statuses[KEY]).toBe('idle')
      expect(useAgentStore.getState().lastEvents[KEY].broadcast_ts).toBe(200)
      stop()
    })

    it('a stream that is not open still decides there (it is all there is)', () => {
      const stop = startWorkerAgentProjection()
      listWithoutRow(true)
      setLive({ sse: 'paused', summary: summary({ state: 'running' }), turnLive: true })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      expect(useAgentStore.getState().statuses[KEY]).toBe('running')
      stop()
    })

    it('with a row, the same live turn events decide nothing', () => {
      seq = 0
      const spy = spyDispatch()
      const stop = startWorkerAgentProjection()
      listRow({ state: 'running', updated_at: 10 })
      setLive({ summary: summary({ state: 'running' }), sse: 'open' })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      apply(ev('execution.message_accepted', { text: 'a', turn_id: 'tA' }), ev('execution.running', { turn_id: 'tA' }))
      apply(ev('result', { type: 'result', subtype: 'success', is_error: false }))
      apply(ev('execution.terminal', { turn_id: 'tA', reason: 'error' }))
      setLive({ summary: summary({ state: 'failed' }) })
      expect(useAgentStore.getState().statuses[KEY]).toBe('running')
      expect(names(spy)).toEqual(['UserPromptSubmit'])
      stop()
    })
  })

  // Before the host list was the one source, the live entry won while its stream was open and, frozen, until the row
  // was strictly newer (A2). Now the stream's state never matters to the status.
  describe('the pane\'s live stream never decides the status (open, evicted, retrying, resumed, closed, unmounted)', () => {
    it.each(['open', 'reconnecting', 'paused', 'connecting', 'closed', 'idle'] as const)('%s: the row decides, however fresh the live snapshot', (sse) => {
      const stop = startWorkerAgentProjection()
      // The live snapshot (as of t=100) says a turn is running; the older row says idle.
      setLive({ summary: summary({ state: 'running', updated_at: 40 }), turnLive: true, turnStarts: [0],
        turnMeta: [{ startAt: 10, endAt: null, outcome: null, durationMs: null }], lastEventAt: 100, sse })
      listRow({ state: 'idle', updated_at: 50 })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      expect(useAgentStore.getState().statuses[KEY]).toBe('idle')
      stop()
    })

    it('a brief reconnect never flips the light (no Stop / UserPromptSubmit)', () => {
      const spy = spyDispatch()
      const stop = startWorkerAgentProjection()
      listRow({ state: 'running', updated_at: 80 })
      setLive({ summary: summary({ state: 'running' }), turnLive: true, sse: 'open' })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      useExecutionStore.getState().setSse(H, E, 'reconnecting')
      setLive({ summary: summary({ state: 'idle', updated_at: 500 }), turnLive: false })
      useExecutionStore.getState().setSse(H, E, 'open')
      expect(useAgentStore.getState().statuses[KEY]).toBe('running')
      expect(names(spy)).toEqual(['UserPromptSubmit'])
      stop()
    })

    it('an evicted pane, resumed with a new turn on its stream while the row still reads idle: idle until the row moves', () => {
      const stop = startWorkerAgentProjection()
      listRow({ state: 'running', updated_at: 20 })
      setLive({ summary: summary({ state: 'running' }), turnLive: true, sse: 'open' })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      const st = () => useAgentStore.getState()
      useExecutionStore.getState().setSse(H, E, 'paused')
      listRow({ state: 'idle', updated_at: 30 })
      expect(st().statuses[KEY]).toBe('idle')
      expect(st().lastEvents[KEY].raw_event_name).toBe('Stop')

      setLive({ sse: 'open', turnStarts: [0, 1], turnMeta: [{ startAt: 10, endAt: 25, outcome: 'ok', durationMs: 15 }, { startAt: 40, endAt: null, outcome: null, durationMs: null }] })
      expect(st().statuses[KEY]).toBe('idle')
      listRow({ state: 'running', updated_at: 50 })
      expect(st().statuses[KEY]).toBe('running')
      listRow({ state: 'failed', updated_at: 60, last_turn_reason: 'orphaned' })
      expect(st().statuses[KEY]).toBe('error')
      stop()
    })

    it('no row before the list answers: no light, whatever the live entry says', () => {
      const stop = startWorkerAgentProjection()
      setLive({ summary: summary({ state: 'running' }), turnLive: true, sse: 'open' })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      expect(useAgentStore.getState().statuses[KEY]).toBeUndefined()
      stop()
    })

    it('a worker the list answered without, never listed (a new one not caught up yet): no light and no clear', () => {
      const spy = spyDispatch()
      const stop = startWorkerAgentProjection()
      setLive({ summary: summary({ state: 'running' }), turnLive: true, sse: 'open' })
      listWithoutRow()
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      expect(useAgentStore.getState().statuses[KEY]).toBeUndefined()
      expect(spy).not.toHaveBeenCalled()
      listRow({ state: 'running', updated_at: 10 })
      expect(useAgentStore.getState().statuses[KEY]).toBe('running')
      stop()
    })

    it('a row leaving a list that answered in full (archived) clears the light; one left out of a truncated list does not', () => {
      const stop = startWorkerAgentProjection()
      listRow({ state: 'idle', updated_at: 10 })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      expect(useAgentStore.getState().statuses[KEY]).toBe('idle')
      listWithoutRow(true)
      expect(useAgentStore.getState().statuses[KEY]).toBe('idle')
      listWithoutRow(false)
      expect(useAgentStore.getState().statuses[KEY]).toBeUndefined()
      expect(useAgentStore.getState().lastEvents[KEY]).toBeUndefined()
      // Unarchived: back.
      listRow({ state: 'idle', updated_at: 20 })
      expect(useAgentStore.getState().statuses[KEY]).toBe('idle')
      stop()
    })
  })

  // Permission channel PC2 / spec §5.4: the tab light is `waiting` while the row carries a pending request. As
  // amended 2026-10-07 (user), that request is an event of the same level as a terminal agent's ask and goes through
  // the same desktop-notification rules.
  describe('awaiting approval', () => {
    const pending = (since: number) => ({ request_id: 'r1', tool_name: 'Bash', since })

    it('the list row: running → waiting while pending → running again once it is null', () => {
      const stop = startWorkerAgentProjection()
      listRow({ state: 'running', updated_at: 40 })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      expect(useAgentStore.getState().statuses[KEY]).toBe('running')

      // Creating a request does not touch the row's updated_at.
      listRow({ state: 'running', updated_at: 40, pending_permission: pending(50) })
      const ev = useAgentStore.getState().lastEvents[KEY]
      expect(useAgentStore.getState().statuses[KEY]).toBe('waiting')
      expect(ev.status).toBe('waiting')
      // Named and shaped like a terminal Claude Code agent's ask (PdxPermissionRequest → PermissionRequest, tool_name),
      // plus the request id the dispatcher dedupes it by (one notification per request).
      expect(ev.raw_event_name).toBe('PermissionRequest')
      expect(ev.detail).toEqual({ tool_name: 'Bash', request_id: 'r1' })
      // State-tied stamp: the request's own start.
      expect(ev.broadcast_ts).toBe(50)

      listRow({ state: 'running', updated_at: 40, pending_permission: null })
      expect(useAgentStore.getState().statuses[KEY]).toBe('running')
      stop()
    })

    it('a list row (no pane state) that is awaiting approval projects waiting', () => {
      const stop = startWorkerAgentProjection()
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      listRow({ state: 'running', pending_permission: pending(70) })
      expect(useAgentStore.getState().statuses[KEY]).toBe('waiting')
      expect(useAgentStore.getState().lastEvents[KEY].detail).toEqual({ tool_name: 'Bash', request_id: 'r1' })
      stop()
    })

    it('a pending request on the live summary alone is not waiting: the row decides, both ways', () => {
      const stop = startWorkerAgentProjection()
      listRow({ state: 'running' })
      setLive({ sse: 'open', summary: summary({ state: 'running', pending_permission: pending(50) }), turnLive: true })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      expect(useAgentStore.getState().statuses[KEY]).toBe('running')
      listRow({ state: 'running', pending_permission: pending(50) })
      setLive({ summary: summary({ state: 'running', pending_permission: null }) })
      expect(useAgentStore.getState().statuses[KEY]).toBe('waiting')
      stop()
    })

    it('the field absent (old daemon) projects exactly as before', () => {
      const stop = startWorkerAgentProjection()
      listRow({ state: 'running' })
      useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
      const ev = useAgentStore.getState().lastEvents[KEY]
      expect(ev.status).toBe('running')
      expect(ev.raw_event_name).toBe('UserPromptSubmit')
      expect(ev.detail).toEqual({})
      stop()
    })

    // PC2 as amended 2026-10-07 (user): waiting for approval is an event of the same level as an agent ask (a
    // terminal agent waiting for the user, e.g. Claude Code's permission prompt) and goes through the same desktop-
    // notification rules with the same exceptions — none while the App is in the foreground AND that tab is the
    // current one, none when the user turned that event off — and the same request never notifies twice.
    describe('notifies like an agent ask (PC2 as amended 2026-10-07)', () => {
      const TMUX = 'ses001'
      const TMUX_KEY = compositeKey(H, TMUX)
      const tmuxTab = (): Tab => ({
        id: 't-tmux', pinned: false, locked: false, createdAt: 0,
        layout: { type: 'leaf', pane: { id: 'p-tmux', content: { kind: 'tmux-session', hostId: H, sessionCode: TMUX, mode: 'terminal', cachedName: '', tmuxInstance: '' } } },
      })
      const request = (request_id: string, since: number, tool_name = 'Bash') => ({ request_id, tool_name, since })
      const R1 = request('r1', 50)
      const runningRow = () => listRow({ state: 'running', updated_at: 40 })
      const openTabs = (activeTabId: string | null = null) =>
        useTabStore.setState({ tabs: { 't-exec': execTab(), 't-tmux': tmuxTab() }, tabOrder: ['t-exec', 't-tmux'], activeTabId })
      /** A terminal Claude Code agent's ask, as the host event stream delivers it (cc broadcasts the Pdx name). */
      const terminalAsk = (broadcast_ts: number) =>
        useAgentStore.getState().handleNormalizedEvent(H, TMUX, { agent_type: 'cc', status: 'waiting', raw_event_name: 'PdxPermissionRequest', broadcast_ts, detail: { tool_name: 'Bash' } })
      const focus = (focused: boolean) => vi.spyOn(document, 'hasFocus').mockReturnValue(focused)
      const notified = () => showNotification.mock.calls.map((c) => c[0] as { title: string; body: string; eventName: string; sessionCode: string; action: unknown })
      const workerNotices = () => notified().filter((n) => n.sessionCode === 'exec-E1')
      const askDispatches = (spy: { mock: { calls: unknown[][] } }) =>
        spy.mock.calls.filter((c) => c[1] === 'exec-E1' && (c[2] as { raw_event_name: string }).raw_event_name === 'PermissionRequest').length

      let showNotification: ReturnType<typeof vi.fn>
      let dispatcher: { unmount: () => void } | null
      let stop: (() => void) | null
      const mount = () => {
        dispatcher = renderHook(() => useNotificationDispatcher())
        stop = startWorkerAgentProjection()
      }
      /** An App reload: nothing is torn down (no clear reaches the dispatcher), in-memory stores start fresh, the
       *  dispatcher's seen map (localStorage) and the tabs survive. */
      const reload = () => {
        dispatcher?.unmount()
        stop?.()
        useAgentStore.setState({ statuses: {}, agentTypes: {}, models: {}, subagents: {}, lastEvents: {}, oscTitles: {}, ccStatus: {}, unread: {} })
        useExecutionStore.setState({ executions: {} })
        useExecutionListStore.setState({ byHost: {} })
        mount()
      }

      beforeEach(() => {
        localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [KEY]: 1, [TMUX_KEY]: 1 }))
        localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS)
        useNotificationSettingsStore.setState({ agents: {} })
        showNotification = vi.fn()
        Object.defineProperty(window, 'electronAPI', { value: { showNotification }, writable: true, configurable: true })
        focus(false)
        dispatcher = null
        stop = null
      })
      afterEach(() => {
        stop?.()
        dispatcher?.unmount()
        Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
        localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN)
        localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS)
      })

      it('the App in the background: a new pending request notifies exactly once, even on the current tab', () => {
        mount()
        runningRow()
        openTabs('t-exec')
        expect(showNotification).not.toHaveBeenCalled()

        listRow({ state: 'running', updated_at: 40, pending_permission: R1 })
        expect(useAgentStore.getState().statuses[KEY]).toBe('waiting')
        expect(showNotification).toHaveBeenCalledTimes(1)
        const n = notified()[0]
        expect(n.eventName).toBe('PermissionRequest')
        // The title is the worker title (what the tab shows), the body names the tool — the agent ask's content.
        expect(n.title).toBe('b - w')
        expect(n.body).toBe('Permission required: Bash')
        expect(n.action).toEqual({ kind: 'open-session', hostId: H, sessionCode: 'exec-E1' })
      })

      it('the App in the foreground on another tab: still notifies once', () => {
        focus(true)
        mount()
        runningRow()
        openTabs('t-tmux')
        listRow({ state: 'running', updated_at: 40, pending_permission: R1 })
        expect(showNotification).toHaveBeenCalledTimes(1)
      })

      it('the App in the foreground on that tab: none — and switching away later does not bring it back', () => {
        focus(true)
        mount()
        runningRow()
        openTabs('t-exec')
        listRow({ state: 'running', updated_at: 40, pending_permission: R1 })
        expect(useAgentStore.getState().statuses[KEY]).toBe('waiting')
        expect(showNotification).not.toHaveBeenCalled()

        // Tab switch, then the App goes to the background and the list is refetched: the request is not new.
        useTabStore.setState({ activeTabId: 't-tmux' })
        focus(false)
        listRow({ state: 'running', updated_at: 70, pending_permission: R1 })
        expect(showNotification).not.toHaveBeenCalled()
      })

      it('the toggle that silences a terminal Claude Code agent\'s ask (cc · PermissionRequest) silences the worker\'s', () => {
        useNotificationSettingsStore.getState().setEventEnabled('cc', 'PermissionRequest', false)
        mount()
        runningRow()
        openTabs()
        terminalAsk(2)
        expect(useAgentStore.getState().statuses[TMUX_KEY]).toBe('waiting')
        listRow({ state: 'running', updated_at: 40, pending_permission: R1 })
        expect(useAgentStore.getState().statuses[KEY]).toBe('waiting')
        expect(useAgentStore.getState().agentTypes[KEY]).toBe('cc')
        expect(showNotification).not.toHaveBeenCalled()

        // Back on: both notify again (the pipeline is live, it was only the setting).
        useNotificationSettingsStore.getState().setEventEnabled('cc', 'PermissionRequest', true)
        terminalAsk(3)
        listRow({ state: 'running', updated_at: 90, pending_permission: request('r2', 80) })
        expect(notified().map((n) => n.sessionCode)).toEqual([TMUX, 'exec-E1'])
      })

      it('a terminal Claude Code agent\'s ask still notifies exactly as before', () => {
        mount()
        openTabs()
        terminalAsk(2)
        expect(showNotification).toHaveBeenCalledTimes(1)
        expect(notified()[0]).toMatchObject({ title: TMUX, body: 'Permission required: Bash', eventName: 'PdxPermissionRequest', sessionCode: TMUX })
        // The same ask again (a reconnect snapshot): no second notification.
        terminalAsk(2)
        expect(showNotification).toHaveBeenCalledTimes(1)
        // The App in the foreground on its tab: a new ask is not pushed.
        focus(true)
        useTabStore.setState({ activeTabId: 't-tmux' })
        terminalAsk(3)
        expect(showNotification).toHaveBeenCalledTimes(1)
      })

      it('the same request seen again — list refreshes, the pane opening, reconnecting, evicted, resumed — is dispatched and notified once', () => {
        const spy = spyDispatch()
        mount()
        runningRow()
        openTabs()
        listRow({ state: 'running', updated_at: 40, pending_permission: R1 })
        expect(showNotification).toHaveBeenCalledTimes(1)

        // A refresh brings the same request back with a newer updated_at.
        listRow({ state: 'running', updated_at: 60, pending_permission: R1 })
        // The pane opens with the request on its own summary, its stream blips, is evicted and resumes.
        setLive({ sse: 'open', summary: summary({ state: 'running', pending_permission: R1 }), turnLive: true })
        useExecutionStore.getState().setSse(H, E, 'reconnecting')
        useExecutionStore.getState().setSse(H, E, 'open')
        useExecutionStore.getState().setSse(H, E, 'paused')
        listRow({ state: 'running', updated_at: 200, pending_permission: R1 })
        useExecutionStore.getState().setSse(H, E, 'open')
        expect(useAgentStore.getState().statuses[KEY]).toBe('waiting')

        expect(askDispatches(spy)).toBe(1)
        expect(showNotification).toHaveBeenCalledTimes(1)
      })

      it('subagent refs from the pane\'s stream coming and going are decoration: the request is never re-dispatched', () => {
        const spy = spyDispatch()
        mount()
        runningRow()
        setLive({ sse: 'open', summary: summary({ state: 'running' }), turnLive: true, tasks: { a: subagentTask('a') } })
        openTabs()
        listRow({ state: 'running', updated_at: 40, pending_permission: R1 })
        expect(showNotification).toHaveBeenCalledTimes(1)
        expect(useAgentStore.getState().subagents[KEY].map((s) => s.id)).toEqual(['a'])

        // The pane unmounts (its stream stops delivering): the refs go, the request stays — not dispatched again.
        useExecutionStore.getState().setSse(H, E, 'idle')
        expect(useAgentStore.getState().subagents[KEY]).toBeUndefined()
        // It comes back with another subagent running.
        setLive({ sse: 'open', tasks: { a: subagentTask('a'), b: subagentTask('b') } })
        expect(useAgentStore.getState().subagents[KEY].map((s) => s.id)).toEqual(['a', 'b'])

        expect(askDispatches(spy)).toBe(1)
        expect(useAgentStore.getState().lastEvents[KEY].detail?.request_id).toBe('r1')
        expect(showNotification).toHaveBeenCalledTimes(1)
      })

      it('a second, different request while still waiting notifies once more', () => {
        mount()
        runningRow()
        openTabs()
        listRow({ state: 'running', updated_at: 40, pending_permission: R1 })
        expect(showNotification).toHaveBeenCalledTimes(1)

        // R1 answered and R2 asked between two refreshes: the worker never leaves `waiting`.
        listRow({ state: 'running', updated_at: 40, pending_permission: request('r2', 80, 'Write') })
        expect(useAgentStore.getState().statuses[KEY]).toBe('waiting')
        expect(showNotification).toHaveBeenCalledTimes(2)
        expect(notified()[1].body).toBe('Permission required: Write')

        // R2 re-seen through a refresh: not a third.
        listRow({ state: 'running', updated_at: 95, pending_permission: request('r2', 80, 'Write') })
        expect(showNotification).toHaveBeenCalledTimes(2)
      })

      it('two requests stamped in the same millisecond each notify once, with distinct Electron stamps', () => {
        // Nexen serializes a worker's requests but does not make their `since` unique: R1 answered and R2 asked
        // within one millisecond carry the same stamp.
        mount()
        runningRow()
        openTabs()
        listRow({ state: 'running', updated_at: 40, pending_permission: R1 })
        listRow({ state: 'running', updated_at: 40, pending_permission: request('r2', 50, 'Write') })
        expect(notified().map((n) => n.body)).toEqual(['Permission required: Bash', 'Permission required: Write'])
        // Electron main drops a stamp it already showed in the last 5 s, so the two must differ.
        const [a, b] = showNotification.mock.calls.map((c) => (c[0] as { broadcastTs: number }).broadcastTs)
        expect(a).not.toBe(b)
        // Both re-seen through a refresh and a reload: neither again.
        listRow({ state: 'running', updated_at: 47, pending_permission: request('r2', 50, 'Write') })
        reload()
        listRow({ state: 'running', updated_at: 300, pending_permission: request('r2', 50, 'Write') })
        expect(showNotification).toHaveBeenCalledTimes(2)
      })

      describe('across an App reload — like an agent ask: a known worker\'s unseen request notifies once, a seen one never again', () => {
        it.each(['without', 'with'] as const)('a request already seen before the reload is not notified again (%s the pane\'s live entry)', (pane) => {
          mount()
          runningRow()
          openTabs()
          listRow({ state: 'running', updated_at: 40, pending_permission: R1 })
          expect(showNotification).toHaveBeenCalledTimes(1)

          reload()
          if (pane === 'with') setLive({ sse: 'open', summary: summary({ state: 'running', pending_permission: R1 }), turnLive: true })
          listRow({ state: 'running', updated_at: 300, pending_permission: R1 })
          expect(useAgentStore.getState().statuses[KEY]).toBe('waiting')
          expect(showNotification).toHaveBeenCalledTimes(1)
        })

        it('a request that arrived while the App was closed notifies once on reload, like a terminal agent\'s ask', () => {
          // Before the reload this client last saw the terminal agent at 20, and the worker's request r0 (since 50).
          localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [KEY]: 50, [TMUX_KEY]: 20 }))
          localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS, JSON.stringify({ [KEY]: ['r0'] }))
          mount()
          openTabs()
          // The terminal agent: the snapshot replays its ask (stamped 50, after 20) — it notifies once.
          terminalAsk(50)
          terminalAsk(50)
          expect(notified().filter((n) => n.sessionCode === TMUX)).toHaveLength(1)
          // The worker: while the App was closed r0 was answered and R1 asked in the same millisecond (since 50, no
          // newer than the stamp this client stored). R1 is a request this client has not seen — it notifies once,
          // and the pane's live entry turning up with it afterwards adds nothing.
          listRow({ state: 'running', updated_at: 300, pending_permission: R1 })
          setLive({ summary: summary({ state: 'running', updated_at: 45, pending_permission: R1 }), turnLive: true, sse: 'open' })
          expect(workerNotices()).toHaveLength(1)
        })

        it('a worker this client never saw: its first sight is recorded, not notified — like an agent ask\'s first event', () => {
          localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN)
          mount()
          openTabs()
          terminalAsk(50)
          listRow({ state: 'running', updated_at: 300, pending_permission: R1 })
          expect(showNotification).not.toHaveBeenCalled()
          // The next request is news — even one stamped in the same millisecond.
          listRow({ state: 'running', updated_at: 400, pending_permission: request('r2', 50) })
          expect(workerNotices()).toHaveLength(1)
        })
      })
    })
  })

  it('stop releases subscriptions and clears the keys it set', () => {
    const stop = startWorkerAgentProjection()
    listRow({ state: 'idle' })
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'] })
    stop()
    expect(listUnsub).toHaveBeenCalledTimes(1)
    expect(useAgentStore.getState().statuses[KEY]).toBeUndefined()
    listRow({ state: 'running' })
    expect(useAgentStore.getState().statuses[KEY]).toBeUndefined()
  })
})
