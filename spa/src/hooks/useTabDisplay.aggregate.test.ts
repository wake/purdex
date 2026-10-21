// U1-3c: the tab light over all panes through useTabDisplay.
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useAgentStore } from '../stores/useAgentStore'
import type { NormalizedEvent, SubagentRef } from '../stores/useAgentStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { useWorkerTitlePrefetchStore } from '../stores/useWorkerTitlePrefetchStore'
import { defaultExecutionState } from '../lib/nex/event-reducer'
import { emptyListCache } from '../lib/nex/execution-list-effects'
import type { ExecutionSummary } from '../lib/nex/types'
import type { PaneLayout, Tab } from '../types/tab'
import { CC_ICON_VARIANTS } from '../lib/agent-icons'
import { useJobsStore } from '../lib/pool/jobs-store'
import type { PoolJob } from '../lib/pool/jobs-types'
import { useTabDisplay } from './useTabDisplay'

const tmux = (id: string, sessionCode: string, hostId = 'h1'): PaneLayout =>
  ({ type: 'leaf', pane: { id, content: { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: sessionCode, tmuxInstance: '' } } })
const exec = (id: string, executionId: string, host = 'h1'): PaneLayout =>
  ({ type: 'leaf', pane: { id, content: { kind: 'execution', executionId, host } } })
const split = (children: PaneLayout[]): PaneLayout =>
  ({ type: 'split', id: 's', direction: 'h', children, sizes: children.map(() => 100 / children.length) })
const tab = (layout: PaneLayout): Tab => ({ id: 't1', pinned: false, locked: false, createdAt: 0, layout })

const ref = (id: string): SubagentRef => ({ id, type: 'cc', started_at: 0, source_pid: 0, source_start_time: '' })
const summary = (over: Partial<ExecutionSummary> = {}): ExecutionSummary =>
  ({ id: 'e1', state: 'running', provider: 'claude', principal_id: 'p', cwd: '/w/repo', mount_kind: 'dev', brief: 'Brief', labels: {},
    created_at: 1, updated_at: 5, duration_ms: null, event_count: 0, observers: 0, archived: false, ...over }) as ExecutionSummary
const pending = { request_id: 'r1', tool_name: 'Bash', since: 1 }

beforeEach(() => {
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  useHostStore.setState({ runtime: {}, hostOrder: ['h1', 'h2'] })
  useAgentStore.setState({ unread: {}, statuses: {}, subagents: {}, agentTypes: {}, oscTitles: {}, lastEvents: {} })
  useUISettingsStore.setState({ tabIndicatorStyle: 'badge', ccIconVariant: 'bot', codexIconVariant: 'openai', dynamicTabName: false, stripAgentTitleMarker: true })
  useI18nStore.setState({ t: (k: string) => k })
  useExecutionStore.setState({ executions: {} })
  useExecutionListStore.setState({ byHost: {} })
  useWorkerTitlePrefetchStore.setState({ byKey: {} })
})

describe('useTabDisplay — the light over all panes (U1-3c)', () => {
  it('a split tab shows the highest-priority pane: an older waiting pane beats a newer running one', () => {
    useAgentStore.setState({ statuses: { 'h1:a': 'waiting', 'h1:b': 'running' }, unread: { 'h1:b': true }, agentTypes: { 'h1:a': 'cc', 'h1:b': 'cc' } })
    const { result } = renderHook(() => useTabDisplay(tab(split([tmux('p1', 'a'), tmux('p2', 'b')]))))
    expect(result.current.agentStatus).toBe('waiting')
    expect(result.current.isUnread).toBe(true) // OR over panes
  })

  it('a split pane with the only agent lights the tab, the primary pane being a shell', () => {
    useAgentStore.setState({ statuses: { 'h1:agent': 'running' }, agentTypes: { 'h1:agent': 'cc' }, subagents: { 'h1:agent': [ref('s1'), ref('s2')] } })
    const { result } = renderHook(() => useTabDisplay(tab(split([tmux('p1', 'shell'), tmux('p2', 'agent')]))))
    expect(result.current.agentStatus).toBe('running')
    expect(result.current.subagentCount).toBe(2) // the dots come from the representative pane
  })

  it('the icon and the title stay the primary pane\'s when another pane represents the light', () => {
    useAgentStore.setState({ statuses: { 'h1:agent': 'error' }, agentTypes: { 'h1:agent': 'cc' } })
    const { result } = renderHook(() => useTabDisplay(tab(split([tmux('p1', 'shell'), tmux('p2', 'agent')]))))
    expect(result.current.agentStatus).toBe('error')
    expect(result.current.IconComponent).not.toBe(CC_ICON_VARIANTS.bot) // the shell pane's icon, not the agent's
    expect(result.current.displayTitle).toBe('shell')
  })

  it('background is the highest kind over the panes', () => {
    const e = (b: NormalizedEvent['background']): NormalizedEvent => ({ agent_type: 'cc', status: 'idle', raw_event_name: 'x', broadcast_ts: 1, background: b })
    useAgentStore.setState({ statuses: { 'h1:a': 'idle', 'h1:b': 'idle' }, lastEvents: { 'h1:a': e('schedule'), 'h1:b': e('monitor') } })
    const { result } = renderHook(() => useTabDisplay(tab(split([tmux('p1', 'a'), tmux('p2', 'b')]))))
    expect(result.current.background).toBe('monitor')
  })

  it('primary tmux pane + secondary execution pane with a pending request → the hand is shown', () => {
    useExecutionListStore.setState({ byHost: { h1: { ...emptyListCache(), phase: 'ready', items: [summary({ pending_permission: pending })] } } })
    useAgentStore.setState({ statuses: { 'h1:a': 'idle', 'h1:exec-e1': 'running' } })
    const { result } = renderHook(() => useTabDisplay(tab(split([tmux('p1', 'a'), exec('p2', 'e1')]))))
    expect(result.current.isAwaitingApproval).toBe(true)
    expect(result.current.agentStatus).toBe('waiting') // the hand forces waiting
  })

  it('the same with the execution pane on another host', () => {
    useExecutionListStore.setState({ byHost: { h2: { ...emptyListCache(), phase: 'ready', items: [summary({ pending_permission: pending })] } } })
    const { result } = renderHook(() => useTabDisplay(tab(split([tmux('p1', 'a'), exec('p2', 'e1', 'h2')]))))
    expect(result.current.isAwaitingApproval).toBe(true)
  })

  it('a truncated list with no row falls back to the live pane state; a stale pending request on an ended worker is ignored', () => {
    useExecutionListStore.setState({ byHost: { h1: { ...emptyListCache(), phase: 'ready', truncated: true, items: [] } } })
    useExecutionStore.setState({ executions: { [executionKey('h1', 'e1')]: { ...defaultExecutionState(), summary: summary({ pending_permission: pending }) } } })
    const view = renderHook(() => useTabDisplay(tab(split([tmux('p1', 'a'), exec('p2', 'e1')]))))
    expect(view.result.current.isAwaitingApproval).toBe(true)
    act(() => { useExecutionStore.setState({ executions: { [executionKey('h1', 'e1')]: { ...defaultExecutionState(), summary: summary({ pending_permission: pending, state: 'terminated' }) } } }) })
    expect(view.result.current.isAwaitingApproval).toBe(false)
  })

  it('a hostless execution pane re-resolves when the first host loads', () => {
    useHostStore.setState({ hostOrder: [] })
    const hostless: PaneLayout = { type: 'leaf', pane: { id: 'p2', content: { kind: 'execution', executionId: 'e1' } } }
    const t = tab(split([tmux('p1', 'a'), hostless]))
    const { result } = renderHook(() => useTabDisplay(t))
    expect(result.current.agentStatus).toBeUndefined()
    act(() => {
      useAgentStore.setState({ statuses: { 'h1:exec-e1': 'error' } })
      useHostStore.setState({ hostOrder: ['h1'] })
    })
    expect(result.current.agentStatus).toBe('error')
  })

  it('an event for another session does not re-render the tab', () => {
    useAgentStore.setState({ statuses: { 'h1:a': 'idle', 'h1:b': 'running' } })
    let renders = 0
    const layout = split([tmux('p1', 'a'), tmux('p2', 'b')])
    const t = tab(layout)
    renderHook(() => { renders++; return useTabDisplay(t) })
    const before = renders
    act(() => { useAgentStore.setState({ statuses: { ...useAgentStore.getState().statuses, 'h1:zzz': 'error' }, unread: { 'h1:zzz': true } }) })
    expect(renders).toBe(before)
  })
})

describe('useTabDisplay — the queue hourglass (W5b2)', () => {
  const agentTmux = (sessionId: string): PaneLayout => ({ type: 'leaf', pane: { id: 'p1', content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'a', mode: 'terminal', cachedName: 'a', tmuxInstance: '',
    rebuild: { sessionName: 'a', tmuxInstance: '', agent: { type: 'cc', sessionId, updatedAt: 1 }, capturedAt: 1 } } } })
  const e = (b: NormalizedEvent['background']): NormalizedEvent => ({ agent_type: 'cc', status: 'idle', raw_event_name: 'x', broadcast_ts: 1, background: b })
  const echoT = (k: string, p?: Record<string, string | number>) => (p ? `${k}${JSON.stringify(p)}` : k)

  beforeEach(() => {
    useJobsStore.getState().reset()
    useI18nStore.setState({ t: echoT })
  })

  it('no job: no hourglass', () => {
    useAgentStore.setState({ statuses: { 'h1:a': 'idle' } })
    const { result } = renderHook(() => useTabDisplay(tab(agentTmux('sid-1'))))
    expect(result.current.background).toBeUndefined()
    expect(result.current.backgroundWaiters).toBeUndefined()
  })

  it('a job ranks the hourglass above a workflow', () => {
    useAgentStore.setState({ statuses: { 'h1:a': 'idle' }, lastEvents: { 'h1:a': e('workflow') } })
    const { result } = renderHook(() => useTabDisplay(tab(agentTmux('sid-1'))))
    expect(result.current.background).toBe('workflow')
    act(() => withJobs([poolJob()]))
    expect(result.current.background).toBe('queued')
    act(() => withJobs([]))
    expect(result.current.background).toBe('workflow')
    expect(result.current.backgroundWaiters).toBeUndefined()
  })

  // WWD-7b1-6: a pool job of the pane's session lights the hourglass
  const poolJob = (over: Partial<PoolJob> = {}): PoolJob => ({
    id: 'j1', origin: 'mlab', repo: { name: 'purdex', root: '', remote: '' }, snapshot: { commit: 'c', tree: '', base: '', rel_cwd: '' }, task: 'spa-tsc', args: [], class: 'dev', labels: [], locks: [], cacheable: false,
    timeout_s: 0, supersede_key: '', interruptible: false, queue_timeout_s: 0, adhoc: false, local: false, submitter: { session: 'sid-1', ref: '', team: '', host: '' }, created_at: Date.now() - 125_000, state: 'queued',
    claim: null, result: null, manual: '', manual_seq: 0, ...over,
  })
  const withJobs = (jobs: PoolJob[]) => useJobsStore.setState({ byOrigin: { mlab: { jobs: Object.fromEntries(jobs.map((j) => [j.id, j])), cursor: '', epoch: '', via: 'd' } }, errors: {} } as never)
  const jobMarks = (items: unknown[] | undefined) => (items ?? []).filter((x) => typeof x === 'object' && x !== null && 'jobId' in x)

  it('a pool job of the pane session lights the hourglass and hands its mark to the symbol', () => {
    useJobsStore.getState().reset()
    useAgentStore.setState({ statuses: { 'h1:a': 'idle' } })
    withJobs([poolJob()])
    const { result } = renderHook(() => useTabDisplay(tab(agentTmux('SID-1'))))
    expect(result.current.background).toBe('queued')
    expect(jobMarks(result.current.backgroundWaiters)).toEqual([expect.objectContaining({ jobId: 'j1', state: 'queued', place: 1 })])
  })

  it('goes when the job ends, and a job of another session or with no session lights nothing', () => {
    useJobsStore.getState().reset()
    useAgentStore.setState({ statuses: { 'h1:a': 'idle' } })
    withJobs([poolJob({ submitter: { session: 'sid-2', ref: '', team: '', host: '' } }), poolJob({ id: 'j2', submitter: { session: '', ref: '', team: '', host: '' } })])
    const { result } = renderHook(() => useTabDisplay(tab(agentTmux('sid-1'))))
    expect(result.current.background).toBeUndefined()
    act(() => withJobs([poolJob()]))
    expect(result.current.background).toBe('queued')
    act(() => withJobs([poolJob({ state: 'succeeded' })]))
    expect(result.current.background).toBeUndefined()
    expect(result.current.backgroundWaiters).toBeUndefined()
  })

  it('a tab with no hourglass holds no per-minute timer', () => {
    vi.useFakeTimers()
    try {
      useAgentStore.setState({ statuses: { 'h1:a': 'idle' } })
      renderHook(() => useTabDisplay(tab(agentTmux('sid-1'))))
      expect(vi.getTimerCount()).toBe(0)
    } finally { vi.useRealTimers() }
  })
})
