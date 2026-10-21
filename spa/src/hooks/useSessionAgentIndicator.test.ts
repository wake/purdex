import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useSessionAgentIndicator } from './useSessionAgentIndicator'
import { useAgentStore } from '../stores/useAgentStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { compositeKey } from '../lib/composite-key'
import type { SubagentRef } from '../stores/useAgentStore'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { defaultExecutionState } from '../lib/nex/event-reducer'
import { emptyListCache } from '../lib/nex/execution-list-effects'
import { execAgentCode } from '../lib/nex/worker-agent-status'
import type { ExecutionSummary } from '../lib/nex/types'

const ref: SubagentRef = { id: 'a', type: 'cc', started_at: 0, source_pid: 0, source_start_time: '' }

const summary = (over: Partial<ExecutionSummary> = {}): ExecutionSummary =>
  ({ id: 'e1', state: 'running', provider: 'claude', principal_id: 'p', cwd: '/w/repo', mount_kind: 'dev', brief: 'Brief', labels: {},
    created_at: 1, updated_at: 5, duration_ms: null, event_count: 0, observers: 0, archived: false, ...over }) as ExecutionSummary
const pending = { request_id: 'r1', tool_name: 'Bash', since: 1 }
const listWith = (items: ExecutionSummary[], over: Record<string, unknown> = {}) =>
  useExecutionListStore.setState({ byHost: { h1: { ...emptyListCache(), phase: 'ready', items, ...over } } })

beforeEach(() => {
  useExecutionListStore.setState({ byHost: {} })
  useExecutionStore.setState({ executions: {} })
  useAgentStore.setState({ statuses: {}, agentTypes: {}, subagents: {}, unread: {} })
  useUISettingsStore.setState({ tabIndicatorStyle: 'iconDot', ccIconVariant: 'bot', codexIconVariant: 'openai' })
})

describe('useSessionAgentIndicator', () => {
  it('returns empty indicator when sessionCode is undefined', () => {
    const { result } = renderHook(() => useSessionAgentIndicator('h1', undefined))
    expect(result.current.agentIcon).toBeUndefined()
    expect(result.current.agentStatus).toBeUndefined()
    expect(result.current.subagentRefs).toEqual([])
    expect(result.current.isUnread).toBe(false)
    expect(result.current.tabIndicatorStyle).toBe('iconDot')
  })

  it('resolves agent icon + status + refs + unread for a live session', () => {
    const ck = compositeKey('h1', 's1')
    useAgentStore.setState({
      statuses: { [ck]: 'running' },
      agentTypes: { [ck]: 'cc' }, // getAgentIcon only knows 'cc' | 'codex' | 'opencode'
      subagents: { [ck]: [ref] },
      unread: { [ck]: true },
    })
    const { result } = renderHook(() => useSessionAgentIndicator('h1', 's1'))
    expect(result.current.agentStatus).toBe('running')
    expect(result.current.agentIcon).toBeTypeOf('function') // getAgentIcon('cc', { ccVariant:'bot', ... }) resolved a component
    expect(result.current.subagentRefs).toHaveLength(1)
    expect(result.current.isUnread).toBe(true)
  })

  it('suppresses the agent icon when terminated', () => {
    const ck = compositeKey('h1', 's1')
    useAgentStore.setState({ agentTypes: { [ck]: 'cc' } })
    const { result } = renderHook(() => useSessionAgentIndicator('h1', 's1', { isTerminated: true }))
    expect(result.current.agentIcon).toBeUndefined()
  })

  // MON-3830: the same awaiting-approval projection as the tab bar's (useTabDisplay's isAwaitingApproval): a worker's list row says it waits on a permission request.
  describe('isAwaitingApproval (same source as the tab bar)', () => {
    const worker = execAgentCode('e1')

    it('is false for a plain session and for a worker with nothing pending', () => {
      expect(renderHook(() => useSessionAgentIndicator('h1', 's1')).result.current.isAwaitingApproval).toBe(false)
      listWith([summary()])
      expect(renderHook(() => useSessionAgentIndicator('h1', worker)).result.current.isAwaitingApproval).toBe(false)
    })

    it('is true when the worker\'s list row has a pending request, even though its agentStatus has not been written yet', () => {
      listWith([summary({ pending_permission: pending })])
      const { result } = renderHook(() => useSessionAgentIndicator('h1', worker))
      expect(result.current.agentStatus).toBeUndefined()
      expect(result.current.isAwaitingApproval).toBe(true)
    })

    it('follows the row: it goes when the request is answered, and an ended worker is never awaiting', () => {
      listWith([summary({ pending_permission: pending })])
      const { result } = renderHook(() => useSessionAgentIndicator('h1', worker))
      expect(result.current.isAwaitingApproval).toBe(true)
      act(() => listWith([summary({ pending_permission: pending, state: 'terminated' })]))
      expect(result.current.isAwaitingApproval).toBe(false)
      act(() => listWith([summary({ pending_permission: pending })]))
      expect(result.current.isAwaitingApproval).toBe(true)
      act(() => listWith([summary()]))
      expect(result.current.isAwaitingApproval).toBe(false)
    })

    it('reads the live summary where the list is cut short and has no row for the worker', () => {
      listWith([], { truncated: true })
      useExecutionStore.setState({ executions: { [executionKey('h1', 'e1')]: { ...defaultExecutionState(), summary: summary({ pending_permission: pending }) } } })
      expect(renderHook(() => useSessionAgentIndicator('h1', worker)).result.current.isAwaitingApproval).toBe(true)
    })

    it('is the tab bar\'s answer: the same helpers decide, for the same worker', async () => {
      const { awaitingKeys, listAwaitingFlags, liveAwaitingFlags } = await import('./useTabAgentAggregate')
      listWith([summary({ pending_permission: pending })])
      const pane = [{ key: compositeKey('h1', worker), hostId: 'h1', executionId: 'e1' }]
      const tabSays = awaitingKeys(pane, listAwaitingFlags(useExecutionListStore.getState().byHost, pane), liveAwaitingFlags({}, pane)).length > 0
      expect(renderHook(() => useSessionAgentIndicator('h1', worker)).result.current.isAwaitingApproval).toBe(tabSays)
    })
  })
})
