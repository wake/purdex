// Regression (user-observed in the Mac App): once a worker tab was switched away, its light stayed 「執行中」 until the
// tab was selected again, and a background worker never notified (no 等待核准, no Stop). The pane's live entry froze
// with `sse: 'open'` when the pane unmounted and kept deciding the status, while the host's list — kept fresh by its
// one site-wide stream — already knew. A worker's status now comes only from the host's list row, like a tmux agent's
// comes from the daemon whatever the panes do.
//
// Everything real but the network: the real TabContent (keepAliveCount 0, so the worker pane really unmounts), the
// real ExecutionView and its live subscription, the real host list store with its site-wide stream, the worker
// projection, the notification dispatcher and the tab's display hook. Faked: the REST calls, the two SSE streams and
// the list walk.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, renderHook, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { useState } from 'react'
import { TabContent } from '../components/TabContent'
import ExecutionView from '../components/execution/ExecutionView'
import { registerModule, clearModuleRegistry, type PaneRendererProps } from '../lib/module-registry'
import { startWorkerAgentProjection } from './useWorkerAgentProjection'
import { __resetDebounceStateForTests, useNotificationDispatcher } from './useNotificationDispatcher'
import { useTabDisplay } from './useTabDisplay'
import { useAgentStore } from '../stores/useAgentStore'
import { useTabStore } from '../stores/useTabStore'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { resetExecutionListForTests, useExecutionListStore } from '../stores/useExecutionListStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useHostStore } from '../stores/useHostStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { useHostConfigStore } from '../stores/useHostConfigStore'
import { useNotificationSettingsStore } from '../stores/useNotificationSettingsStore'
import { useWorkerTitlePrefetchStore } from '../stores/useWorkerTitlePrefetchStore'
import { subscriptionSlots } from '../lib/nex/subscription-slots'
import { compositeKey } from '../lib/composite-key'
import { STORAGE_KEYS } from '../lib/storage'
import { createTab, type ExecutionViewMode, type Tab } from '../types/tab'
import type { ExecutionSummary, PendingPermission } from '../lib/nex/types'
import type { NexSseOptions } from '../lib/nex/nex-sse'
import { forgetWorkerDraft } from '../lib/nex/worker-draft-memory'
import * as api from '../lib/nex/nex-api'
import * as sse from '../lib/nex/nex-sse'
import * as list from '../lib/nex/list-all-executions'
import * as lease from './useExecutionLease'

vi.mock('../lib/nex/nex-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/nex/nex-api')>()),
  getExecution: vi.fn(), attachObserve: vi.fn(), fetchExecutionEvents: vi.fn(), fetchExecutionTasks: vi.fn(),
  sendMessage: vi.fn(), fetchExecutionPrelude: vi.fn(), releaseLease: vi.fn(),
}))
vi.mock('../lib/nex/nex-sse', async (importOriginal) => ({ ...(await importOriginal<typeof import('../lib/nex/nex-sse')>()), openNexSse: vi.fn() }))
vi.mock('../lib/nex/list-all-executions', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/nex/list-all-executions')>()), listAllExecutions: vi.fn(),
}))
vi.mock('./useExecutionLease', () => ({ useExecutionLease: vi.fn() }))
vi.mock('../lib/nex/client-id', () => ({ getNexClientId: () => 't-me000000' }))

const H = 'h', E = 'exc_1'
const KEY = compositeKey(H, 'exec-exc_1')
const PANE_URL = `/api/nex/v1/events?execution_id=${E}`
/** The host's site-wide stream: the events endpoint without an execution (whatever `kind=` filter it asks for). */
const isSiteStream = (url: string) => url.startsWith('/api/nex/v1/events') && !url.includes('execution_id=')
const R1: PendingPermission = { request_id: 'r1', tool_name: 'Bash', since: 250 }

const summary = (over: Partial<ExecutionSummary> = {}): ExecutionSummary =>
  ({ id: E, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/Users/w/repo', mount_kind: 'dev', brief: 'b', labels: {},
    created_at: 0, updated_at: 100, duration_ms: null, event_count: 0, observers: 1, archived: false, effective_profile: 'handoff_ask',
    turn_count: 0, pending_permission: null, ...over }) as ExecutionSummary

// The real pane wrapper owns the mode in the tab store; a local state stands in for it (as in ExecutionView.tab-switch).
function ExecutionRenderer({ pane, isActive, isFocusTarget = false }: PaneRendererProps) {
  const [mode, setMode] = useState<ExecutionViewMode>('room')
  if (pane.content.kind !== 'execution') return null
  return <ExecutionView key={`${H}:${pane.content.executionId}`} hostId={H} executionId={pane.content.executionId} isActive={isActive}
    isFocusTarget={isFocusTarget} tabId="t-exec" paneId={pane.id} mode={mode} onModeChange={setMode} />
}
const Other = () => <div data-testid="other-tab" />

const execTab: Tab = { ...createTab({ kind: 'execution', executionId: E, host: H }), id: 't-exec' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [execTab, dashTab]

/** What the list walk returns next: the host's rows, and whether the walk hit the page cap. */
let rows: ExecutionSummary[]
let truncated: boolean
/** What the pane's `getExecution` returns next. */
let paneSummary: ExecutionSummary
interface FakeStream { opts: NexSseOptions; close: ReturnType<typeof vi.fn<() => void>> }
let streams: FakeStream[]
let seq: number
let showNotification: ReturnType<typeof vi.fn>
let stopProjection: (() => void) | null
let dispatcherHook: { unmount: () => void } | null
let rerenderTabs: ((tab: Tab) => void) | null

const openStreams = (match: (url: string) => boolean) => streams.filter((s) => match(s.opts.url) && s.close.mock.calls.length === 0)
const flush = (ms = 0) => act(async () => { await vi.advanceTimersByTimeAsync(ms) })

/** The site-wide stream delivers a frame (its contents are never applied); the list refetch lands after the debounce. */
async function hostEvent(kind: string) {
  const [site] = openStreams(isSiteStream)
  expect(site).toBeDefined()
  act(() => { seq += 1; site.opts.onFrame({ id: String(seq), event: kind, data: '{}' }) })
  await flush(600)
}

/** The pane's own live stream delivers a durable frame (the bare provider payload, as Nexen's live `data:`). */
function paneEvent(kind: string, payload: Record<string, unknown>) {
  const [pane] = openStreams((url) => url === PANE_URL)
  expect(pane).toBeDefined()
  act(() => { seq += 1; pane.opts.onFrame({ id: String(seq), event: kind, data: JSON.stringify(payload) }) })
}

function paneStatus(status: Parameters<NexSseOptions['onStatus']>[0]) {
  const [pane] = openStreams((url) => url === PANE_URL)
  act(() => { pane.opts.onStatus(status) })
}

/** The App with the worker tab selected and its pane live. */
async function boot() {
  dispatcherHook = renderHook(() => useNotificationDispatcher())
  stopProjection = startWorkerAgentProjection()
  useTabStore.setState({ tabs: { [execTab.id]: execTab, [dashTab.id]: dashTab }, tabOrder: [execTab.id, dashTab.id], activeTabId: execTab.id })
  const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
  rerenderTabs = (tab) => rerender(<TabContent activeTab={tab} allTabs={all} />)
  await flush()
  paneStatus('open')
  await flush()
}

function switchTo(tab: Tab) {
  act(() => { useTabStore.setState({ activeTabId: tab.id }) })
  rerenderTabs!(tab)
}

/** The worker's tab as both tab surfaces show it (useTabDisplay): its light and whether it shows the hand. */
function workerTab() {
  const { result, unmount } = renderHook(() => useTabDisplay(execTab))
  const out = { light: result.current.agentStatus, hand: result.current.isAwaitingApproval }
  unmount()
  return out
}

const notices = () => showNotification.mock.calls.map((c) => c[0] as { eventName: string; sessionCode: string; body: string })
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement

/** A send from the pane, and the pane's stream seeing its turn start. */
async function send() {
  fireEvent.change(box(), { target: { value: 'go' } })
  fireEvent.keyDown(box(), { key: 'Enter' })
  await flush()
  expect(api.sendMessage).toHaveBeenCalledTimes(1)
  paneSummary = summary({ state: 'running', turn_count: 1, updated_at: 200 })
  paneEvent('execution.message_accepted', { turn_id: 't1', text: 'go' })
  paneEvent('execution.running', { turn_id: 't1' })
}

/** A send from the pane makes the worker run: the pane sees its turn start, the host list then reads running. */
async function sendAndRun() {
  await send()
  rows = [summary({ state: 'running', turn_count: 1, updated_at: 200 })]
  await hostEvent('execution.running')
  expect(workerTab().light).toBe('running')
}

beforeEach(() => {
  cleanup()
  vi.useFakeTimers()
  __resetDebounceStateForTests()
  subscriptionSlots.resetForTests()
  resetExecutionListForTests()
  clearModuleRegistry()
  registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: ExecutionRenderer }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  useHostStore.setState({ hosts: { [H]: { id: H, name: 'mlab', ip: '1', port: 1 } } as never, hostOrder: [H], runtime: {} })
  useNexHostStore.setState({
    byHost: { [H]: { info: { configured: true, mounted: true, ready: true, init_error: '', effective: null }, capabilities: {} as never,
      phase: 'ready', error: null, fetchedAt: 0, generation: 0, fingerprint: '' } },
    ensure: vi.fn<(hostId: string) => Promise<void>>().mockResolvedValue(undefined),
  })
  useAgentStore.setState({ statuses: {}, agentTypes: {}, models: {}, subagents: {}, lastEvents: {}, oscTitles: {}, ccStatus: {}, unread: {} })
  useExecutionStore.setState({ executions: {} })
  useExecutionListStore.setState({ byHost: {} })
  useWorkerTitlePrefetchStore.setState({ byKey: {} })
  useNotificationSettingsStore.setState({ agents: {} })
  localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN)
  localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS)

  rows = [summary()]
  truncated = false
  paneSummary = summary()
  streams = []
  seq = 100
  vi.mocked(sse.openNexSse).mockReset().mockImplementation((opts) => {
    const s: FakeStream = { opts, close: vi.fn<() => void>() }
    streams.push(s)
    return { close: s.close }
  })
  vi.mocked(list.listAllExecutions).mockReset().mockImplementation(async () =>
    ({ items: rows.map((r) => ({ ...r })), pages: [], dropped: 0, truncated, stuck: false, stuckPage: null }))
  vi.mocked(api.getExecution).mockReset().mockImplementation(async () => ({ ...paneSummary }))
  vi.mocked(api.attachObserve).mockReset().mockResolvedValue({ mode: 'observe', stream_url: PANE_URL, cursor: 0, state: 'idle' } as never)
  vi.mocked(api.fetchExecutionEvents).mockReset().mockResolvedValue({ items: [], next_cursor: 0 })
  vi.mocked(api.sendMessage).mockReset().mockResolvedValue({ turn_id: 't1', delivery: 'delivered' })
  vi.mocked(lease.useExecutionLease).mockReturnValue({ ensureLease: vi.fn().mockResolvedValue('ls_1'), release: vi.fn(), forget: vi.fn(), touch: vi.fn() })

  showNotification = vi.fn()
  Object.defineProperty(window, 'electronAPI', { value: { showNotification }, writable: true, configurable: true })
  // The App is in the foreground: only the tab on screen keeps a worker quiet.
  vi.spyOn(document, 'hasFocus').mockReturnValue(true)
  stopProjection = null
  dispatcherHook = null
  rerenderTabs = null
})

afterEach(() => {
  cleanup()
  stopProjection?.()
  dispatcherHook?.unmount()
  forgetWorkerDraft(`${H}:${E}`)
  Object.defineProperty(window, 'electronAPI', { value: undefined, writable: true, configurable: true })
  localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN)
  localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS)
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('a worker tab switched away keeps its status and notifies (status from the host list only)', () => {
  it('a permission request while away: one notification, the light turns waiting and the tab shows the hand', async () => {
    await boot()
    expect(workerTab().light).toBe('idle')
    await sendAndRun()

    switchTo(dashTab)
    // The pane really is gone.
    expect(screen.queryByTestId('execution-view')).toBeNull()
    // The entry it leaves behind no longer claims a live stream.
    expect(useExecutionStore.getState().executions[executionKey(H, E)].sse).not.toBe('open')

    rows = [summary({ state: 'running', turn_count: 1, updated_at: 200, pending_permission: R1 })]
    await hostEvent('permission.requested')
    expect(workerTab()).toEqual({ light: 'waiting', hand: true })
    expect(notices()).toHaveLength(1)
    expect(notices()[0]).toMatchObject({ eventName: 'PermissionRequest', sessionCode: 'exec-exc_1', body: 'Permission required: Bash' })

    // The same request seen again through another refresh: still one.
    await hostEvent('tool_use')
    expect(notices()).toHaveLength(1)
  })

  it('the turn ending while away: the light turns idle and exactly one Stop is sent', async () => {
    await boot()
    await sendAndRun()
    switchTo(dashTab)

    rows = [summary({ state: 'idle', turn_count: 1, updated_at: 300 })]
    await hostEvent('execution.terminal')
    expect(workerTab()).toEqual({ light: 'idle', hand: false })
    expect(notices().map((n) => n.eventName)).toEqual(['Stop'])
    expect(useAgentStore.getState().unread[KEY]).toBe(true)
  })

  it('the request answered elsewhere while away: the light leaves waiting, no further notification', async () => {
    await boot()
    await sendAndRun()
    switchTo(dashTab)
    rows = [summary({ state: 'running', turn_count: 1, updated_at: 200, pending_permission: R1 })]
    await hostEvent('permission.requested')
    expect(workerTab().light).toBe('waiting')
    expect(notices()).toHaveLength(1)

    // Another client allowed it; the turn goes on.
    rows = [summary({ state: 'running', turn_count: 1, updated_at: 200, pending_permission: null })]
    await hostEvent('permission.resolved')
    expect(workerTab()).toEqual({ light: 'running', hand: false })
    expect(notices()).toHaveLength(1)
  })

  it('switching back (the pane remounts and its stream opens) does not notify again, and the light keeps the row\'s status', async () => {
    await boot()
    await sendAndRun()
    switchTo(dashTab)
    rows = [summary({ state: 'running', turn_count: 1, updated_at: 200, pending_permission: R1 })]
    await hostEvent('permission.requested')
    expect(notices()).toHaveLength(1)

    // The request is answered elsewhere; the remounting pane reads that first, before the list refresh lands.
    paneSummary = summary({ state: 'running', turn_count: 1, updated_at: 200, pending_permission: null })
    switchTo(execTab)
    await flush()
    paneStatus('open')
    await flush()
    expect(screen.getByTestId('execution-view')).toBeInTheDocument()
    expect(useExecutionStore.getState().executions[executionKey(H, E)].summary?.pending_permission).toBeNull()
    // The light is the host list's: still waiting until the list says otherwise.
    expect(workerTab()).toEqual({ light: 'waiting', hand: true })
    expect(notices()).toHaveLength(1)

    rows = [summary({ state: 'running', turn_count: 1, updated_at: 200, pending_permission: null })]
    await hostEvent('permission.resolved')
    expect(workerTab()).toEqual({ light: 'running', hand: false })
    expect(notices()).toHaveLength(1)
  })

  it('a reconnecting pane stream or a stale live entry never makes a transition of its own', async () => {
    await boot()
    await sendAndRun()
    const spy = vi.spyOn(useAgentStore.getState(), 'handleNormalizedEvent')
    spy.mockClear()

    // The pane's stream blips, and on reopening its own entry reads the turn as over (a summary read that says idle,
    // the turn's end replayed) while the host list still says running.
    paneStatus('reconnecting')
    expect(workerTab().light).toBe('running')
    paneSummary = summary({ state: 'idle', turn_count: 1, updated_at: 150 })
    paneStatus('open')
    paneEvent('result', { type: 'result', subtype: 'success', is_error: false })
    paneEvent('execution.terminal', { turn_id: 't1', reason: 'final_response' })
    await flush(400)
    expect(useExecutionStore.getState().executions[executionKey(H, E)].summary?.state).toBe('idle')
    expect(workerTab().light).toBe('running')
    expect(spy).not.toHaveBeenCalled()

    // Away, the frozen entry is overwritten with anything at all: still nothing.
    switchTo(dashTab)
    act(() => { useExecutionStore.getState().setSummary(H, E, summary({ state: 'failed', turn_count: 1, updated_at: 900 })) })
    expect(workerTab().light).toBe('running')
    expect(spy).not.toHaveBeenCalled()
    expect(notices()).toHaveLength(0)

    // Only the host list moves the light.
    rows = [summary({ state: 'idle', turn_count: 1, updated_at: 300 })]
    await hostEvent('execution.terminal')
    expect(workerTab().light).toBe('idle')
    expect(spy.mock.calls.map((c) => (c[2] as { raw_event_name: string }).raw_event_name)).toEqual(['Stop'])
    expect(notices().map((n) => n.eventName)).toEqual(['Stop'])
  })

  it('while the user looks at the tab, the light follows the host list and nothing is notified (foreground exception)', async () => {
    await boot()
    await sendAndRun()
    // The pane's own stream has not caught up; the host list has.
    rows = [summary({ state: 'running', turn_count: 1, updated_at: 200, pending_permission: R1 })]
    await hostEvent('permission.requested')
    expect(workerTab()).toEqual({ light: 'waiting', hand: true })

    rows = [summary({ state: 'idle', turn_count: 1, updated_at: 300 })]
    await hostEvent('execution.terminal')
    expect(workerTab()).toEqual({ light: 'idle', hand: false })
    expect(notices()).toHaveLength(0)
  })

  it('a truncated list without the worker\'s row falls back to the live entry', async () => {
    rows = [summary({ id: 'exc_other' })]
    truncated = true
    await boot()
    expect(workerTab().light).toBe('idle')
    // The list never carries this worker: the light comes from the pane's live entry.
    await send()
    await flush(400)
    expect(workerTab().light).toBe('running')

    paneSummary = summary({ state: 'idle', turn_count: 1, updated_at: 300 })
    paneEvent('result', { type: 'result', subtype: 'success', is_error: false })
    paneEvent('execution.terminal', { turn_id: 't1', reason: 'final_response' })
    await flush(400)
    expect(workerTab().light).toBe('idle')
  })
})
