// Notification-path regressions of the worker projection (status from the host's list row only): a swallowed turn,
// the pane's live entry and its decoration never notifying, and `terminated`.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import { startWorkerAgentProjection as startProjection } from './useWorkerAgentProjection'
import { useAgentStore } from '../stores/useAgentStore'
import { useTabStore } from '../stores/useTabStore'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useHostStore } from '../stores/useHostStore'
import { compositeKey } from '../lib/composite-key'
import { __resetDebounceStateForTests, useNotificationDispatcher } from './useNotificationDispatcher'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'
import { STORAGE_KEYS } from '../lib/storage'
import { defaultExecutionState, type ExecutionState } from '../lib/nex/event-reducer'
import { emptyListCache } from '../lib/nex/execution-list-effects'
import type { ExecutionSummary, WorkerTask } from '../lib/nex/types'
import type { Tab } from '../types/tab'

const H = 'host-a'
const E = 'E1'
const KEY = compositeKey(H, 'exec-E1')

const summary = (over: Partial<ExecutionSummary> = {}): ExecutionSummary =>
  ({ id: E, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'b', labels: {},
    created_at: 1, updated_at: 5, duration_ms: null, event_count: 0, observers: 0, archived: false, ...over }) as ExecutionSummary

const execTab = (): Tab => ({
  id: 't-exec', pinned: false, locked: false, createdAt: 0,
  layout: { type: 'leaf', pane: { id: 'p-t-exec', content: { kind: 'execution', executionId: E, host: H } } },
})

const listRow = (over: Partial<ExecutionSummary>) =>
  useExecutionListStore.setState({ byHost: { [H]: { ...emptyListCache(), items: [summary({ turn_count: 1, ...over })] } } })

let showNotification: ReturnType<typeof vi.fn>
let dispatcher: { unmount: () => void }

/** Every projection a test starts is stopped after it, even one whose assertion threw (stop is idempotent). */
const started: Array<() => void> = []
function startWorkerAgentProjection(): () => void {
  const stop = startProjection()
  started.push(stop)
  return stop
}

beforeEach(() => {
  __resetDebounceStateForTests()
  useAgentStore.setState({ statuses: {}, agentTypes: {}, models: {}, subagents: {}, lastEvents: {}, oscTitles: {}, ccStatus: {}, unread: {} })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useExecutionStore.setState({ executions: {} })
  useExecutionListStore.setState({ byHost: {}, subscribe: vi.fn(() => () => {}) })
  useNexHostStore.setState({ ensure: vi.fn<(hostId: string) => Promise<void>>().mockResolvedValue(undefined) })
  useHostStore.setState({ hostOrder: [H] })
  useNotificationSettingsStore.setState({ agents: {} })
  localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify({ [KEY]: 1 }))
  showNotification = vi.fn()
  Object.defineProperty(window, 'electronAPI', { value: { showNotification }, writable: true, configurable: true })
  dispatcher = renderHook(() => useNotificationDispatcher())
})

afterEach(() => {
  for (const stop of started.splice(0)) stop()
  dispatcher.unmount()
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
  localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN)
  vi.restoreAllMocks()
})

describe('row-sourced projection: a turn missed between two refreshes', () => {
  it('idle → (running → idle unseen) → idle with a higher turn_count fires a fresh Stop', () => {
    const stop = startWorkerAgentProjection()
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'], activeTabId: null })
    listRow({ state: 'idle', turn_count: 1, updated_at: 100 })
    expect(useAgentStore.getState().statuses[KEY]).toBe('idle')
    expect(showNotification).not.toHaveBeenCalled() // the 0 baseline of a first row projection

    // The debounced refresh missed the running state: same status, one more turn.
    listRow({ state: 'idle', turn_count: 2, updated_at: 300 })
    expect(useAgentStore.getState().lastEvents[KEY].broadcast_ts).toBe(300)
    expect(showNotification).toHaveBeenCalledTimes(1)
    stop()
  })

  it('an unchanged row (same turn_count) is still deduped', () => {
    const stop = startWorkerAgentProjection()
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'], activeTabId: null })
    listRow({ state: 'idle', turn_count: 1, updated_at: 100 })
    listRow({ state: 'idle', turn_count: 1, updated_at: 400 }) // lease renew: updated_at only
    expect(useAgentStore.getState().lastEvents[KEY].broadcast_ts).toBe(0)
    expect(showNotification).not.toHaveBeenCalled()
    stop()
  })

  it('the pane\'s live entry turning up, freezing or resuming does not replay the Stop', () => {
    const stop = startWorkerAgentProjection()
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'], activeTabId: null })
    listRow({ state: 'running', turn_count: 1, updated_at: 100 })
    listRow({ state: 'idle', turn_count: 1, updated_at: 200 })
    expect(showNotification).toHaveBeenCalledTimes(1)
    // The pane opens on the ended turn (its own, different turn stamps), is evicted, resumes; the list refreshes.
    useExecutionStore.setState({
      executions: { [executionKey(H, E)]: { ...defaultExecutionState(), sse: 'open', summary: summary({ state: 'idle' }), turnStarts: [0],
        turnMeta: [{ startAt: 10, endAt: 900, outcome: 'ok', durationMs: 1 }] } },
    })
    useExecutionStore.getState().setSse(H, E, 'paused')
    listRow({ state: 'idle', turn_count: 1, updated_at: 900 })
    useExecutionStore.getState().setSse(H, E, 'open')
    expect(showNotification).toHaveBeenCalledTimes(1)
    stop()
  })
})

describe('decoration (the running-subagent refs) never notifies', () => {
  const subagent = (id: string): WorkerTask =>
    ({ task_id: id, turn_id: 't', kind: 'subagent', task_type: 'x', tool_use_id: null, parent_tool_use_id: null, description: '',
      backgrounded: true, status: 'running', provider_status: null, closed_by: null, started_at: 3, ended_at: null, startSeq: 1, subagent_type: 'Explore' })
  const setLive = (patch: Partial<ExecutionState>) => useExecutionStore.setState((s) => ({
    executions: { ...s.executions, [executionKey(H, E)]: { ...(s.executions[executionKey(H, E)] ?? defaultExecutionState()), ...patch } },
  }))

  it('refs coming and going after a Stop, past a lease renew that moved updated_at: one Stop, and the read tab stays read', () => {
    const stop = startWorkerAgentProjection()
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'], activeTabId: null })
    listRow({ state: 'running', turn_count: 1, updated_at: 100 })
    listRow({ state: 'idle', turn_count: 1, updated_at: 300 })
    expect(showNotification).toHaveBeenCalledTimes(1)
    useAgentStore.getState().markRead(H, 'exec-E1')
    // A lease renew: updated_at moves, nothing else.
    listRow({ state: 'idle', turn_count: 1, updated_at: 400 })

    // A background subagent runs after the turn's result, shown while the pane's stream delivers ...
    setLive({ sse: 'open', summary: summary({ state: 'idle' }), tasks: { a: subagent('a') } })
    expect(useAgentStore.getState().subagents[KEY]?.map((s) => s.id)).toEqual(['a'])
    // ... and the pane unmounts.
    useExecutionStore.getState().setSse(H, E, 'idle')
    expect(useAgentStore.getState().subagents[KEY]).toBeUndefined()

    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(useAgentStore.getState().unread[KEY]).toBeUndefined()
    stop()
  })
})

describe('terminated', () => {
  it('notifies once (explicit event) for a worker seen alive, then clears the key', () => {
    const stop = startWorkerAgentProjection()
    listRow({ state: 'running' })
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'], activeTabId: null })
    expect(showNotification).not.toHaveBeenCalled()

    listRow({ state: 'terminated', updated_at: 50 })
    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(showNotification.mock.calls[0][0].eventName).toBe('WorkerTerminated')
    expect(showNotification.mock.calls[0][0].body).toBe('Worker terminated')
    expect(useAgentStore.getState().statuses[KEY]).toBeUndefined()
    expect(useAgentStore.getState().lastEvents[KEY]).toBeUndefined()
    stop()
  })

  it('a worker first seen already terminated (reload) is history: no notification', () => {
    const stop = startWorkerAgentProjection()
    listRow({ state: 'terminated' })
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'], activeTabId: null })
    expect(showNotification).not.toHaveBeenCalled()
    stop()
  })

  it('archiving (not terminated) only clears: the row leaves the list (it lists unarchived executions only)', () => {
    const stop = startWorkerAgentProjection()
    listRow({ state: 'running' })
    useTabStore.setState({ tabs: { 't-exec': execTab() }, tabOrder: ['t-exec'], activeTabId: null })
    listRow({ state: 'idle', updated_at: 60 })
    expect(showNotification).toHaveBeenCalledTimes(1) // the Stop
    useExecutionListStore.setState({ byHost: { [H]: { ...emptyListCache(), phase: 'ready', items: [] } } })
    expect(showNotification).toHaveBeenCalledTimes(1)
    expect(useAgentStore.getState().statuses[KEY]).toBeUndefined()
    stop()
  })
})
