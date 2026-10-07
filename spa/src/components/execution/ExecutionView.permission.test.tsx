// spa/src/components/execution/ExecutionView.permission.test.tsx — the request card in the worker pane
// (permission channel plan Task 9; spec §5.3, §5.5; Review Focus #3 / #4).
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import ExecutionView from './ExecutionView'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { NexApiError, type NexEvent } from '../../lib/nex/types'
import { exitWorker } from '../../lib/nex/exit-worker'
import { takeBack } from '../../lib/nex/handoff'
import { clearAllPermissionCards } from '../../lib/nex/permission-card-memory'
import * as api from '../../lib/nex/nex-api'
import * as lease from '../../hooks/useExecutionLease'
import * as sub from '../../hooks/useExecutionSubscription'

vi.mock('../../lib/nex/nex-api', () => ({
  sendMessage: vi.fn(), interruptExecution: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), uploadWorkerFile: vi.fn(),
  fetchExecutionPrelude: vi.fn(), listExecutions: vi.fn(), fetchExecutionEvents: vi.fn(), answerPermission: vi.fn(),
}))
vi.mock('../../hooks/useExecutionSubscription', () => ({ useExecutionSubscription: vi.fn(() => ({ problem: null, paused: false })) }))
vi.mock('../../hooks/useExecutionLease', () => ({ useExecutionLease: vi.fn() }))
vi.mock('../../lib/nex/client-id', () => ({ getNexClientId: () => 't-me000000' }))
vi.mock('./WorkerEndedPane', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./WorkerEndedPane')>()),
  WorkerEndedPane: () => <div data-testid="worker-ended-pane" />,
}))
vi.mock('../../lib/nex/exit-worker', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/nex/exit-worker')>()),
  exitWorker: vi.fn(),
}))
vi.mock('../../lib/nex/handoff', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/nex/handoff')>()),
  takeBack: vi.fn(),
}))

const H = 'h', E = 'exc_1'
const base = { hostId: H, executionId: E, tabId: 't1', paneId: 'p1', onModeChange: () => {} }
const from = { sessionCode: 'zk16vd', tmuxInstance: 'inst-1', cachedName: 'purdex' }
const ensureLease = vi.fn(), release = vi.fn(), touch = vi.fn(), forget = vi.fn()
const caps = {
  phase: 'ready', sandbox_profiles: ['handoff_ask'],
  permissions: { profiles: ['handoff_ask'], answer: { method: 'POST', path: '/api/nex/v1/executions/{id}/permissions/{request_id}' }, timeout: { max_s: 86400 } },
}
const summary = (extra = {}) => ({ id: E, state: 'running', provider: 'claude', principal_id: 'p', cwd: '/Users/w/repo', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 1, archived: false, effective_profile: 'handoff_ask', ...extra })

let seq = 0
const apply = (kind: string, payload: Record<string, unknown>) => act(() => {
  seq += 1
  const ev: NexEvent = { seq, execution_id: E, kind, payload, created_at: Date.now() }
  useExecutionStore.getState().applyEvents(H, E, [ev])
})
const ask = (requestId: string, extra: Record<string, unknown> = {}) =>
  apply('permission.requested', { request_id: requestId, turn_id: 'trn_1', tool_name: 'Bash', input: { command: `echo ${requestId}` }, ...extra })
const resolve = (requestId: string, outcome: string, extra: Record<string, unknown> = {}) =>
  apply('permission.resolved', { request_id: requestId, turn_id: 'trn_1', outcome, ...extra })
const holds = () => vi.mocked(lease.useExecutionLease).mock.calls.map((c) => c[2]?.hold ?? false)
function deferred<T>() {
  let resolveP!: (v: T) => void
  const promise = new Promise<T>((r) => { resolveP = r })
  return { promise, resolve: resolveP }
}

beforeEach(() => {
  seq = 0
  clearAllPermissionCards()
  useI18nStore.getState().setLocale('en')
  useExecutionStore.setState({ executions: {} })
  ensureLease.mockReset().mockResolvedValue('ls_1'); release.mockReset(); touch.mockReset(); forget.mockReset()
  vi.mocked(lease.useExecutionLease).mockReset().mockReturnValue({ ensureLease, release, forget, touch })
  vi.mocked(sub.useExecutionSubscription).mockReturnValue({ problem: null, paused: false })
  vi.mocked(api.answerPermission).mockReset().mockResolvedValue({ request_id: 'req_a', outcome: 'allowed' })
  vi.mocked(exitWorker).mockReset().mockResolvedValue({ exited: true, terminated: true, archived: true, state: 'terminated' })
  vi.mocked(takeBack).mockReset()
  useUndoToast.setState({ toast: null, notice: null })
  useNexHostStore.setState({ byHost: { [H]: { phase: 'ready', capabilities: caps } } as never, ensure: async () => {} })
  useExecutionStore.getState().setSummary(H, E, summary() as never)
  useExecutionStore.getState().setHistoryLoaded(H, E, true)
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  apply('execution.delegated', { brief: 'go' })
  apply('execution.running', { turn_id: 'trn_1' })
})

describe('ExecutionView — permission request card', () => {
  it('no request → no card and no hold', () => {
    render(<ExecutionView {...base} isActive />)
    expect(screen.queryByTestId('permission-card')).toBeNull()
    expect(holds().every((h) => h === false)).toBe(true)
  })

  it('a pending request shows the card between the worker dock and the input, and holds the lease', () => {
    render(<ExecutionView {...base} isActive />)
    ask('req_a')
    const card = screen.getByTestId('permission-card')
    expect(card).toHaveTextContent('echo req_a')
    const dock = screen.getByTestId('worker-dock')
    const input = screen.getByRole('textbox')
    expect(dock.compareDocumentPosition(card) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(card.compareDocumentPosition(input) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(holds().at(-1)).toBe(true)
  })

  it('chat view shows the card too', () => {
    render(<ExecutionView {...base} isActive mode="chat" />)
    ask('req_a')
    expect(screen.getByTestId('permission-card')).toBeInTheDocument()
  })

  it('同意 → one answer with the pane lease and the host capabilities; the card goes at once; the hold ends with the resolution', async () => {
    render(<ExecutionView {...base} isActive />)
    ask('req_a')
    await act(async () => { fireEvent.click(screen.getByTestId('permission-allow')) })
    expect(api.answerPermission).toHaveBeenCalledTimes(1)
    expect(api.answerPermission).toHaveBeenCalledWith(H, E, 'req_a', { decision: 'allow', leaseId: 'ls_1' }, useNexHostStore.getState().byHost[H]!.capabilities)
    expect(screen.queryByTestId('permission-card')).toBeNull()
    expect(holds().at(-1)).toBe(true)
    resolve('req_a', 'allowed', { principal_id: 'p' })
    expect(holds().at(-1)).toBe(false)
  })

  it('拒絕 with a note sends it as the message', async () => {
    vi.mocked(api.answerPermission).mockResolvedValueOnce({ request_id: 'req_a', outcome: 'denied' })
    render(<ExecutionView {...base} isActive />)
    ask('req_a')
    fireEvent.click(screen.getByTestId('permission-deny'))
    fireEvent.change(screen.getByTestId('permission-deny-note'), { target: { value: '不要動 prod' } })
    await act(async () => { fireEvent.click(screen.getByTestId('permission-deny')) })
    expect(vi.mocked(api.answerPermission).mock.calls[0][3]).toEqual({ decision: 'deny', message: '不要動 prod', leaseId: 'ls_1' })
    expect(screen.queryByTestId('permission-card')).toBeNull()
  })

  it('a double click on 同意 sends one answer', async () => {
    const d = deferred<{ request_id: string; outcome: 'allowed' }>()
    vi.mocked(api.answerPermission).mockReturnValueOnce(d.promise)
    render(<ExecutionView {...base} isActive />)
    ask('req_a')
    const allow = screen.getByTestId('permission-allow')
    fireEvent.click(allow)
    fireEvent.click(allow)
    await waitFor(() => expect(api.answerPermission).toHaveBeenCalledTimes(1))
    expect(screen.getByTestId('permission-allow')).toBeDisabled()
    expect(screen.getByTestId('permission-deny')).toBeDisabled()
    await act(async () => { d.resolve({ request_id: 'req_a', outcome: 'allowed' }) })
    expect(api.answerPermission).toHaveBeenCalledTimes(1)
  })

  it('409 permission_not_pending → the card closes quietly: no error line', async () => {
    vi.mocked(api.answerPermission).mockRejectedValueOnce(new NexApiError(409, 'permission_not_pending', 'already ended'))
    render(<ExecutionView {...base} isActive />)
    ask('req_a')
    await act(async () => { fireEvent.click(screen.getByTestId('permission-allow')) })
    expect(screen.queryByTestId('permission-card')).toBeNull()
    expect(screen.queryByTestId('permission-error')).toBeNull()
    expect(screen.queryByTestId('send-error')).toBeNull()
  })

  it('permission_not_found keeps the card with an error line', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    vi.mocked(api.answerPermission).mockRejectedValueOnce(new NexApiError(404, 'permission_not_found', 'no such request'))
    render(<ExecutionView {...base} isActive />)
    ask('req_a')
    await act(async () => { fireEvent.click(screen.getByTestId('permission-allow')) })
    expect(screen.getByTestId('permission-card')).toBeInTheDocument()
    expect(screen.getByTestId('permission-error')).toBeInTheDocument()
    expect(warn).toHaveBeenCalled()
    warn.mockRestore()
  })

  it('a resolution from elsewhere removes the card without any click', () => {
    render(<ExecutionView {...base} isActive />)
    ask('req_a')
    expect(screen.getByTestId('permission-card')).toBeInTheDocument()
    resolve('req_a', 'allowed', { principal_id: 'pdx:other' })
    expect(screen.queryByTestId('permission-card')).toBeNull()
    expect(screen.queryByTestId('permission-expired')).toBeNull()
  })

  it('cancelled → the card disappears and no line is left', () => {
    render(<ExecutionView {...base} isActive />)
    ask('req_a')
    expect(screen.getByTestId('permission-card')).toBeInTheDocument()
    resolve('req_a', 'cancelled', { reason: 'interrupt', interrupt_source: 'user' })
    expect(screen.queryByTestId('permission-card')).toBeNull()
    expect(screen.queryByTestId('permission-expired')).toBeNull()
  })

  it('execution.terminated with no permission.resolved and a failing summary refetch → card gone, lease hold released', () => {
    vi.mocked(api.listExecutions).mockRejectedValue(new Error('boom'))
    render(<ExecutionView {...base} isActive />)
    ask('req_a')
    expect(screen.getByTestId('permission-card')).toBeInTheDocument()
    expect(holds().at(-1)).toBe(true)
    apply('execution.terminated', { principal_id: 'p' })
    expect(screen.queryByTestId('permission-card')).toBeNull()
    expect(screen.queryByTestId('permission-expired')).toBeNull()
    expect(holds().at(-1)).toBe(false)
  })

  it('execution.terminal -> idle with no permission.resolved and a failing summary refetch → card gone, lease hold released', () => {
    vi.mocked(api.listExecutions).mockRejectedValue(new Error('boom'))
    render(<ExecutionView {...base} isActive />)
    ask('req_a')
    expect(screen.getByTestId('permission-card')).toBeInTheDocument()
    expect(holds().at(-1)).toBe(true)
    apply('execution.terminal', { turn_id: 'trn_1', reason: 'done', state: 'idle' })
    expect(screen.queryByTestId('permission-card')).toBeNull()
    expect(screen.queryByTestId('permission-expired')).toBeNull()
    expect(holds().at(-1)).toBe(false)
  })

  it('expired → a muted 「已逾時自動拒絕（N 分鐘）」 line that stays through the turn\'s end and goes with the next turn', () => {
    useI18nStore.getState().setLocale('zh-TW')
    render(<ExecutionView {...base} isActive />)
    ask('req_a')
    resolve('req_a', 'expired', { timeout_s: 300, message: 'timed out' })
    expect(screen.queryByTestId('permission-card')).toBeNull()
    expect(screen.getByTestId('permission-expired')).toHaveTextContent('已逾時自動拒絕（5 分鐘）')
    apply('result', { type: 'result', subtype: 'success' })
    apply('execution.terminal', { turn_id: 'trn_1', reason: 'final_response', state: 'idle' })
    expect(screen.getByTestId('permission-expired')).toHaveTextContent('已逾時自動拒絕（5 分鐘）')
    apply('execution.message_accepted', { text: 'next', turn_id: 'trn_2' })
    expect(screen.queryByTestId('permission-expired')).toBeNull()
  })

  it('an expiry of one request is not left on screen after the user handles another', () => {
    useI18nStore.getState().setLocale('zh-TW')
    render(<ExecutionView {...base} isActive />)
    ask('req_a')
    ask('req_b')
    resolve('req_a', 'expired', { timeout_s: 300 })
    resolve('req_b', 'allowed')
    expect(screen.queryByText(/已逾時自動拒絕/)).toBeNull()
    expect(screen.queryByTestId('permission-card')).toBeNull()
  })

  it('a request asked after the turn\'s result (a background subagent) shows the card, naming the subagent by its task', () => {
    render(<ExecutionView {...base} isActive />)
    apply('task_start', { task_id: 'a8fb', turn_id: 'trn_1', kind: 'subagent', task_type: 'local_agent', tool_use_id: 'toolu_ag', parent_tool_use_id: null, description: 'Probe the repo', backgrounded: true, started_at: 1 })
    apply('result', { type: 'result', subtype: 'success' })
    ask('req_bg', { agent_id: 'a8fb' })
    expect(screen.getByTestId('permission-card')).toHaveTextContent('subagent: Probe the repo')
  })

  it('two pending: the earliest first; once answered, the next one shows before its resolution arrives', async () => {
    render(<ExecutionView {...base} isActive />)
    ask('req_a')
    ask('req_b')
    expect(screen.getByTestId('permission-card')).toHaveTextContent('echo req_a')
    await act(async () => { fireEvent.click(screen.getByTestId('permission-allow')) })
    expect(screen.getByTestId('permission-card')).toHaveTextContent('echo req_b')
  })

  it('buttons are disabled while 退出 runs', async () => {
    const d = deferred<{ exited: boolean; terminated: boolean; archived: boolean; state: string }>()
    vi.mocked(exitWorker).mockReturnValueOnce(d.promise)
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'idle' }) as never)
    render(<ExecutionView {...base} isActive />)
    ask('req_a')
    expect(screen.getByTestId('permission-allow')).not.toBeDisabled()
    fireEvent.click(screen.getByTestId('header-exit'))
    await waitFor(() => expect(exitWorker).toHaveBeenCalledTimes(1))
    expect(screen.getByTestId('permission-allow')).toBeDisabled()
    expect(screen.getByTestId('permission-deny')).toBeDisabled()
    fireEvent.click(screen.getByTestId('permission-allow'))
    expect(api.answerPermission).not.toHaveBeenCalled()
    await act(async () => { d.resolve({ exited: true, terminated: true, archived: true, state: 'terminated' }) })
  })

  // Spec §5.4 / N7 (acceptance e1): the turn this pane sent is waiting on a request — 退出 in the header and the
  // overflow is available, asks first, exits once with the pane's own lease, and freezes the card meanwhile.
  it('a worker waiting for approval on the turn this pane sent can be exited from the pane', async () => {
    const d = deferred<{ exited: boolean; terminated: boolean; archived: boolean; state: string }>()
    vi.mocked(exitWorker).mockReturnValueOnce(d.promise)
    vi.mocked(api.sendMessage).mockResolvedValueOnce({ turn_id: 'trn_2', delivery: 'delivered' })
    useExecutionStore.getState().setLease(H, E, { leaseId: 'ls_1', expiresAt: Date.now() + 100_000 })
    useI18nStore.getState().setLocale('zh-TW')
    render(<ExecutionView {...base} isActive />)
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'run the marker command' } })
    await act(async () => { fireEvent.keyDown(box, { key: 'Enter' }) })
    expect(api.sendMessage).toHaveBeenCalledTimes(1)
    apply('execution.message_accepted', { text: 'run the marker command', turn_id: 'trn_2' })
    ask('req_a')
    act(() => { useExecutionStore.getState().applySummaryPatch(H, E, { pending_permission: { request_id: 'req_a', tool_name: 'Bash', since: Date.now() } }) })
    expect(screen.getByTestId('execution-state')).toHaveTextContent('等待核准')
    expect(screen.getByTestId('header-exit')).toBeEnabled()
    fireEvent.click(screen.getByTestId('header-overflow'))
    expect(screen.getByTestId('overflow-exit')).toBeEnabled()
    fireEvent.click(screen.getByTestId('overflow-exit'))
    expect(exitWorker).not.toHaveBeenCalled()
    expect(screen.getByTestId('exit-dialog')).toHaveTextContent('退出 worker？')
    expect(screen.getByTestId('exit-dialog')).toHaveTextContent('這一輪會被中斷。')
    fireEvent.click(screen.getByTestId('exit-confirm'))
    await waitFor(() => expect(exitWorker).toHaveBeenCalledTimes(1))
    expect(exitWorker).toHaveBeenCalledWith(expect.objectContaining({ hostId: H, executionId: E, leaseId: 'ls_1', forgetLease: forget }))
    expect(screen.getByTestId('permission-allow')).toBeDisabled()
    expect(screen.getByTestId('permission-deny')).toBeDisabled()
    expect(screen.getByTestId('header-exit')).toBeDisabled()
    fireEvent.click(screen.getByTestId('header-exit'))
    fireEvent.click(screen.getByTestId('permission-allow'))
    expect(exitWorker).toHaveBeenCalledTimes(1)
    expect(api.answerPermission).not.toHaveBeenCalled()
    await act(async () => { d.resolve({ exited: true, terminated: true, archived: true, state: 'terminated' }) })
  })

  it('buttons are disabled while take-back (take-to-terminal) runs', async () => {
    const d = deferred<never>()
    vi.mocked(takeBack).mockReturnValueOnce(d.promise)
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'idle' }) as never)
    render(<ExecutionView {...base} from={from} isActive />)
    ask('req_a')
    fireEvent.click(screen.getByTestId('view-mode'))
    fireEvent.click(screen.getByTestId('view-mode-terminal'))
    await waitFor(() => expect(takeBack).toHaveBeenCalledTimes(1))
    expect(screen.getByTestId('permission-allow')).toBeDisabled()
    expect(screen.getByTestId('permission-deny')).toBeDisabled()
  })
})
