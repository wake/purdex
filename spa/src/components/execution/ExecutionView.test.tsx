import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import ExecutionView from './ExecutionView'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { useTabStore } from '../../stores/useTabStore'
import { useHostStore } from '../../stores/useHostStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { NexApiError } from '../../lib/nex/types'
import { useSessionStore } from '../../stores/useSessionStore'
import { HandoffApiError, nexTakeback, nexTakeToTerminal } from '../../lib/nex/handoff-api'
import { takeBack, takeToTerminal } from '../../lib/nex/handoff'
import { createTab } from '../../types/tab'
import { getPrimaryPane } from '../../lib/pane-tree'
import * as api from '../../lib/nex/nex-api'
import * as lease from '../../hooks/useExecutionLease'
import * as sub from '../../hooks/useExecutionSubscription'

vi.mock('../../lib/nex/nex-api', () => ({ sendMessage: vi.fn(), interruptExecution: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn() }))
vi.mock('../../hooks/useExecutionSubscription', () => ({ useExecutionSubscription: vi.fn(() => ({ problem: null, paused: false })) }))
vi.mock('../../hooks/useExecutionLease', () => ({ useExecutionLease: vi.fn() }))
vi.mock('../../lib/nex/client-id', () => ({ getNexClientId: () => 't-me000000' }))
// The take-back path runs the real orchestration (store swap, forget-before-
// swap) against a mocked daemon call; `takeBack` itself is a pass-through spy
// so the view's call shape (lease id, forgetLease identity) is observable.
vi.mock('../../lib/nex/handoff-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/nex/handoff-api')>()),
  nexTakeback: vi.fn(),
  nexTakeToTerminal: vi.fn(),
}))
vi.mock('../../lib/nex/handoff', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/nex/handoff')>()
  return { ...actual, takeBack: vi.fn(actual.takeBack), takeToTerminal: vi.fn(actual.takeToTerminal) }
})

const H = 'h', E = 'exc_1', KEY = 'h:exc_1'
const base = { hostId: H, executionId: E, tabId: 't1', paneId: 'p1' }
const ensureLease = vi.fn(), release = vi.fn(), touch = vi.fn(), forget = vi.fn()
const summary = (extra = {}) => ({ id: E, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/Users/w/repo', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 2, archived: false, effective_profile: 'standard', turn_count: 3, ...extra })

beforeEach(() => {
  useExecutionStore.setState({ executions: {} })
  ensureLease.mockReset().mockResolvedValue('ls_1'); release.mockReset(); touch.mockReset(); forget.mockReset()
  vi.mocked(lease.useExecutionLease).mockReturnValue({ ensureLease, release, forget, touch })
  vi.mocked(sub.useExecutionSubscription).mockReturnValue({ problem: null, paused: false })
  vi.mocked(api.sendMessage).mockReset().mockResolvedValue({ turn_id: 't1', delivery: 'delivered' })
  vi.mocked(api.interruptExecution).mockReset().mockResolvedValue({ turn_id: 't1', state: 'idle' })
  vi.mocked(api.terminateExecution).mockReset().mockResolvedValue(undefined)
  useExecutionStore.getState().setSummary(H, E, summary() as never)
  useExecutionStore.getState().setHistoryLoaded(H, E, true)
  // Take back / Take to terminal re-point the pane only on a host shown in the workbench (host ownership H2d-3).
  useShownHostsStore.setState({ ids: [H] })
})

describe('ExecutionView', () => {
  it('renders header facts from the summary', () => {
    useExecutionStore.getState().setSummary(H, E, summary({ lease: { principal_id: 'pdx:mlab/t-me000000', expires_at: 1 } }) as never)
    render(<ExecutionView {...base} isActive />)
    expect(screen.getByTestId('execution-state')).toHaveTextContent('idle')
    expect(screen.getByText(/standard/)).toBeInTheDocument()
    expect(screen.getByText(/repo/)).toBeInTheDocument()
    expect(screen.getByText(/\(you\)/)).toBeInTheDocument()
  })

  it('send: optimistic bubble, lease acquired, message posted, queued tag shown', async () => {
    vi.mocked(api.sendMessage).mockResolvedValueOnce({ turn_id: 't1', delivery: 'queued' })
    render(<ExecutionView {...base} isActive />)
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'hello' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', 'hello'))
    expect(ensureLease).toHaveBeenCalledTimes(1)
    expect(touch).toHaveBeenCalled()
    expect(screen.getByText('hello')).toBeInTheDocument()
    expect(screen.getByText(/queued/i)).toBeInTheDocument()
    expect(useExecutionStore.getState().executions[KEY].pendingSend).toBe(true)
  })

  it('locks the input synchronously before the lease resolves, so a second submit while acquisition is in flight is a no-op', async () => {
    let resolveLease!: (v: string) => void
    ensureLease.mockReturnValueOnce(new Promise<string>((resolve) => { resolveLease = resolve }))
    render(<ExecutionView {...base} isActive />)
    const box = screen.getByRole('textbox')

    fireEvent.change(box, { target: { value: 'first' } })
    fireEvent.keyDown(box, { key: 'Enter' })

    // pendingSend must already be true synchronously, before ensureLease's
    // promise has had a chance to resolve — proves the lock is set before
    // the await, not after.
    expect(useExecutionStore.getState().executions[KEY].pendingSend).toBe(true)
    expect(ensureLease).toHaveBeenCalledTimes(1)
    expect(api.sendMessage).not.toHaveBeenCalled()

    // Drive a second submit while the first lease acquisition is still in
    // flight. Dispatch directly (bypassing the textarea's `disabled`
    // attribute) so this proves the re-entrancy guard itself, not just the
    // disabled input.
    fireEvent.change(box, { target: { value: 'second' } })
    fireEvent.keyDown(box, { key: 'Enter' })

    expect(ensureLease).toHaveBeenCalledTimes(1)

    await act(async () => { resolveLease('ls_1'); await Promise.resolve(); await Promise.resolve() })

    expect(api.sendMessage).toHaveBeenCalledTimes(1)
    expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', 'first')
  })

  it('does not resurrect pendingLocal once message_accepted already consumed it while the POST is still in flight (I12)', async () => {
    let resolveSend!: (v: { turn_id: string; delivery: 'delivered' | 'queued' }) => void
    vi.mocked(api.sendMessage).mockReturnValueOnce(new Promise((resolve) => { resolveSend = resolve }))
    render(<ExecutionView {...base} isActive />)
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'hello' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(useExecutionStore.getState().executions[KEY].pendingLocal?.text).toBe('hello'))

    // The durable event beats the POST response back (execution/service.go:794-807).
    act(() => {
      useExecutionStore.getState().applyEvents(H, E, [
        { seq: 1, execution_id: E, kind: 'execution.message_accepted', payload: { text: 'hello', turn_id: 't1' }, created_at: 0 },
      ])
    })
    expect(useExecutionStore.getState().executions[KEY].pendingLocal).toBeNull()

    await act(async () => { resolveSend({ turn_id: 't1', delivery: 'delivered' }); await Promise.resolve() })

    expect(useExecutionStore.getState().executions[KEY].pendingLocal).toBeNull()
    expect(screen.getAllByText('hello')).toHaveLength(1)
    expect(useExecutionStore.getState().executions[KEY].lastTurn).toEqual({ turnId: 't1', delivery: 'delivered' })
  })

  it('send failure withdraws the bubble, re-enables input, restores text, shows the error (I12)', async () => {
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(400, 'invalid_text', 'too long'))
    render(<ExecutionView {...base} isActive />)
    const box = screen.getByRole('textbox') as HTMLTextAreaElement
    fireEvent.change(box, { target: { value: 'hello' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(screen.getByTestId('send-error')).toBeInTheDocument())
    const st = useExecutionStore.getState().executions[KEY]
    expect(st.pendingSend).toBe(false)
    expect(st.pendingLocal).toBeNull()
    expect(st.sendError?.code).toBe('invalid_text')
    // The restore mechanism is a `key={draft}` remount (see StreamInput /
    // ExecutionView), which replaces the textarea DOM node; re-query rather
    // than reuse the stale `box` reference captured before the remount.
    const restored = screen.getByRole('textbox') as HTMLTextAreaElement
    expect(restored.value).toBe('hello')
    expect(restored.disabled).toBe(false)
  })

  it('lease_held shows the holder notice and keeps the input enabled', async () => {
    useExecutionStore.getState().setSummary(H, E, summary({ lease: { principal_id: 'pdx:mlab/t-other', expires_at: 1 } }) as never)
    // The real hook writes leaseError before rethrowing; the mock must too.
    ensureLease.mockImplementationOnce(async () => {
      useExecutionStore.getState().setLeaseError(H, E, { code: 'lease_held', heldBy: 'pdx:mlab/t-other' })
      throw new NexApiError(409, 'lease_held', 'held')
    })
    render(<ExecutionView {...base} isActive />)
    const box = screen.getByRole('textbox') as HTMLTextAreaElement
    fireEvent.change(box, { target: { value: 'x' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    // The header's own lease line also renders the holder's principal, so
    // /t-other/ matches two elements; scope to the dedicated notice.
    await waitFor(() => expect(screen.getByTestId('lease-held')).toHaveTextContent(/t-other/))
    // handleSend's catch always restores the draft via the `key={draft}`
    // remount (same as the send-failure test above), so `box` is a stale,
    // detached node here too — re-query before asserting on it.
    const restored = screen.getByRole('textbox') as HTMLTextAreaElement
    expect(restored.disabled).toBe(false)
    expect(api.sendMessage).not.toHaveBeenCalled()
    // lease_held is fully handled by the notice above — no redundant
    // "Send failed" banner.
    expect(screen.queryByTestId('send-error')).toBeNull()
  })

  it('lease_expired | lease_mismatch | lease_required from send drop the local lease via forget() so the next action re-acquires', async () => {
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(409, 'lease_mismatch', 'stale'))
    render(<ExecutionView {...base} isActive />)
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'hello' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(screen.getByTestId('send-error')).toBeInTheDocument())
    expect(forget).toHaveBeenCalledTimes(1)
    expect(useExecutionStore.getState().executions[KEY].sendError?.code).toBe('lease_mismatch')
  })

  it('interrupt acquires the lease and posts; no_live_turn is silent', async () => {
    render(<ExecutionView {...base} isActive />)
    fireEvent.click(screen.getByRole('button', { name: /interrupt/i }))
    await waitFor(() => expect(api.interruptExecution).toHaveBeenCalledWith(H, E, 'ls_1'))
    vi.mocked(api.interruptExecution).mockRejectedValueOnce(new NexApiError(409, 'no_live_turn', 'nothing'))
    fireEvent.click(screen.getByRole('button', { name: /interrupt/i }))
    await act(async () => {})
    expect(screen.queryByTestId('send-error')).not.toBeInTheDocument()
  })

  it('terminate needs two clicks, then acquires the lease and posts', async () => {
    render(<ExecutionView {...base} isActive />)
    fireEvent.click(screen.getByRole('button', { name: /^terminate$/i }))
    expect(api.terminateExecution).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: /confirm terminate/i }))
    await waitFor(() => expect(api.terminateExecution).toHaveBeenCalledWith(H, E, 'ls_1'))
  })

  it('disables input with a reason when archived or ended', () => {
    useExecutionStore.getState().setSummary(H, E, summary({ archived: true }) as never)
    const { rerender } = render(<ExecutionView {...base} isActive />)
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(true)
    expect(screen.getByRole('textbox')).toHaveAttribute('placeholder', expect.stringMatching(/archived/i))
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'terminated' }) as never)
    rerender(<ExecutionView {...base} isActive />)
    expect(screen.getByRole('textbox')).toHaveAttribute('placeholder', expect.stringMatching(/ended/i))
  })

  it('disables the input until history has loaded', () => {
    useExecutionStore.getState().setHistoryLoaded(H, E, false)
    render(<ExecutionView {...base} isActive />)
    // The loading placeholder replaces the conversation, but StreamInput is
    // still rendered below it — must stay disabled while spinner is up.
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(true)
  })

  it('a terminally closed live stream (with error) disables input with a disconnected placeholder', () => {
    useExecutionStore.getState().setSse(H, E, 'closed', 'forbidden')
    render(<ExecutionView {...base} isActive />)
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(true)
    expect(screen.getByRole('textbox')).toHaveAttribute('placeholder', expect.stringMatching(/lost/i))
  })

  it('sse closed with no error (e.g. an in-progress reconnect backoff) does not disable input', () => {
    useExecutionStore.getState().setSse(H, E, 'closed', null)
    render(<ExecutionView {...base} isActive />)
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(false)
  })

  it('archived: input is disabled but Terminate stays enabled (not a terminal state)', () => {
    useExecutionStore.getState().setSummary(H, E, summary({ archived: true }) as never)
    render(<ExecutionView {...base} isActive />)
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(true)
    expect(screen.getByRole('button', { name: /^terminate$/i })).not.toBeDisabled()
  })

  it('shows the thinking indicator once a delivered send has no reply yet', async () => {
    render(<ExecutionView {...base} isActive />)
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'hello' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(useExecutionStore.getState().executions[KEY].pendingLocal?.delivery).toBe('delivered'))
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
  })

  it('hides the thinking indicator while the send is still queued', async () => {
    vi.mocked(api.sendMessage).mockResolvedValueOnce({ turn_id: 't2', delivery: 'queued' })
    render(<ExecutionView {...base} isActive />)
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'hi' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(useExecutionStore.getState().executions[KEY].pendingLocal?.delivery).toBe('queued'))
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
  })

  it('renders the problem states instead of the conversation', () => {
    vi.mocked(sub.useExecutionSubscription).mockReturnValue({ problem: 'not_found', paused: false })
    const { rerender } = render(<ExecutionView {...base} isActive />)
    expect(screen.getByText(/not found/i)).toBeInTheDocument()
    vi.mocked(sub.useExecutionSubscription).mockReturnValue({ problem: 'host_removed', paused: false })
    rerender(<ExecutionView {...base} isActive />)
    expect(screen.getByText(/no host for this execution/i)).toBeInTheDocument()
    vi.mocked(sub.useExecutionSubscription).mockReturnValue({ problem: 'nex_disabled', paused: false })
    rerender(<ExecutionView {...base} isActive />)
    expect(screen.getByText(/not enabled/i)).toBeInTheDocument()
  })

  it('shows the loading state until history is loaded', () => {
    useExecutionStore.getState().setHistoryLoaded(H, E, false)
    render(<ExecutionView {...base} isActive />)
    expect(screen.getByTestId('execution-loading')).toBeInTheDocument()
  })

  it('shows the retrying error text under the loading line while a retry chain is failing', () => {
    useExecutionStore.getState().setHistoryLoaded(H, E, false)
    useExecutionStore.getState().setSse(H, E, 'closed', 'Failed to fetch')
    render(<ExecutionView {...base} isActive />)
    expect(screen.getByTestId('execution-loading')).toBeInTheDocument()
    expect(screen.getByTestId('execution-loading-error')).toHaveTextContent(/Failed to fetch/)
  })
})

// ---- P-B2.2 spec §4.4 R2 / R3 + the elapsed ticker ------------------------

type Exec = ReturnType<typeof useExecutionStore.getState>['executions'][string]
const patchExec = (extra: Partial<Exec>) =>
  useExecutionStore.setState((s) => ({ executions: { ...s.executions, [KEY]: { ...s.executions[KEY], ...extra } } }))
const textPartial = (text: string): Exec['partial'] =>
  ({ messageId: 'm', finalized: 0, blocks: { 0: { index: 0, type: 'text', text, thinking: '', partialJson: '' } } })
const toolUseFrame = (seq: number, created_at: number) => ({
  seq, execution_id: E, kind: 'assistant', created_at,
  payload: { type: 'assistant', parent_tool_use_id: null, message: { id: 'm1', role: 'assistant', content: [{ type: 'tool_use', id: 'tu1', name: 'Bash', input: { command: 'sleep 9' } }], stop_reason: null } },
})
const toolResultFrame = (seq: number, created_at: number) => ({
  seq, execution_id: E, kind: 'user', created_at,
  payload: { type: 'user', parent_tool_use_id: null, message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 'tu1', content: 'ok', is_error: false }], stop_reason: null } },
})

describe('ExecutionView — thinking indicator truth table (R3)', () => {
  it('R3: turnLive alone (observer) → thinking indicator present', () => {
    patchExec({ turnLive: true })
    render(<ExecutionView {...base} isActive />)
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
  })

  it('R3: turnLive with visible partial text → thinking indicator absent, typewriter present', () => {
    patchExec({ turnLive: true, partial: textPartial('tokens flowing') })
    render(<ExecutionView {...base} isActive />)
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
    expect(screen.getByTestId('partial-group')).toHaveTextContent('tokens flowing')
    expect(screen.getByTestId('stream-cursor')).toBeInTheDocument()
  })

  it('R3: turnLive with a partial whose blocks are all empty → thinking indicator still present', () => {
    patchExec({ turnLive: true, partial: textPartial('') })
    render(<ExecutionView {...base} isActive />)
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
  })

  it('R3: turnLive with a whitespace-only text partial → no bubble, thinking indicator still present', () => {
    patchExec({ turnLive: true, partial: textPartial(' \n ') })
    render(<ExecutionView {...base} isActive />)
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
    expect(screen.queryByTestId('stream-cursor')).not.toBeInTheDocument()
  })

  it('R3: turnLive with a started tool_use (no input_json_delta yet) → spinner row, thinking indicator absent', () => {
    patchExec({ turnLive: true, partial: { messageId: 'm', finalized: 0, blocks: { 0: { index: 0, type: 'tool_use', text: '', thinking: '', partialJson: '', toolId: 'tu9', toolName: 'Bash' } } } })
    render(<ExecutionView {...base} isActive />)
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
    expect(screen.getByTestId('tool-icon-spinner')).toBeInTheDocument()
  })

  it('R3: pendingSend queued without turnLive → thinking indicator absent', () => {
    patchExec({ pendingSend: true, pendingLocal: { text: 'hi', delivery: 'queued' } as Exec['pendingLocal'] })
    render(<ExecutionView {...base} isActive />)
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
  })

  it('R3: neither turnLive nor pendingSend → thinking indicator absent', () => {
    render(<ExecutionView {...base} isActive />)
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
  })

  it('R3: turnLive with a running tool → spinner only, no thinking indicator; dots return once the tool_result lands', () => {
    patchExec({ turnLive: true })
    render(<ExecutionView {...base} isActive />)
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
    act(() => { useExecutionStore.getState().applyEvents(H, E, [toolUseFrame(1, 5_000)]) })
    expect(screen.getByTestId('tool-icon-spinner')).toBeInTheDocument()
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
    // Turn still live, no partial: the model is silent again, so the dots come back.
    act(() => { useExecutionStore.getState().applyEvents(H, E, [toolResultFrame(2, 9_000)]) })
    expect(useExecutionStore.getState().executions[KEY].turnLive).toBe(true)
    expect(screen.queryByTestId('tool-icon-spinner')).not.toBeInTheDocument()
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
  })
})

describe('ExecutionView — tool activity (R2) and the elapsed ticker', () => {
  it('R2: a running tool is marked aborted after execution.turn_orphaned', () => {
    render(<ExecutionView {...base} isActive />)
    act(() => { useExecutionStore.getState().applyEvents(H, E, [toolUseFrame(1, 5_000)]) })
    expect(screen.getByTestId('tool-icon-spinner')).toBeInTheDocument()
    act(() => {
      useExecutionStore.getState().applyEvents(H, E, [{ seq: 2, execution_id: E, kind: 'execution.turn_orphaned', payload: { turn_id: 't1' }, created_at: 9_000 }])
    })
    expect(screen.getByTestId('tool-aborted')).toBeInTheDocument()
    expect(screen.queryByTestId('tool-icon-spinner')).not.toBeInTheDocument()
    expect(screen.queryByTestId('tool-elapsed')).not.toBeInTheDocument()
  })

  it('ticker: the elapsed badge advances every second while a tool runs and stops once its result lands', () => {
    vi.useFakeTimers()
    try {
      vi.setSystemTime(10_000)
      render(<ExecutionView {...base} isActive />)
      const idleTimers = vi.getTimerCount()
      act(() => { useExecutionStore.getState().applyEvents(H, E, [toolUseFrame(1, 10_000)]) })
      expect(screen.getByTestId('tool-elapsed')).toHaveTextContent('0.0s')
      expect(vi.getTimerCount()).toBe(idleTimers + 1)
      act(() => { vi.advanceTimersByTime(1_000) })
      expect(screen.getByTestId('tool-elapsed')).toHaveTextContent('1.0s')
      act(() => { vi.advanceTimersByTime(1_000) })
      expect(screen.getByTestId('tool-elapsed')).toHaveTextContent('2.0s')
      // Any one-shot timers from mount have fired by now; only the interval is left.
      expect(vi.getTimerCount()).toBe(1)

      act(() => { useExecutionStore.getState().applyEvents(H, E, [toolResultFrame(2, 16_200)]) })
      expect(screen.queryByTestId('tool-elapsed')).not.toBeInTheDocument()
      expect(screen.getByTestId('tool-duration')).toHaveTextContent('6.2s')
      // The interval is cleared: no tool is running any more.
      expect(vi.getTimerCount()).toBe(0)
      act(() => { vi.advanceTimersByTime(5_000) })
      expect(screen.getByTestId('tool-duration')).toHaveTextContent('6.2s')
    } finally {
      vi.useRealTimers()
    }
  })
})

// ---- P-C.3b task 4: "Take back to terminal" ------------------------------

const from = { sessionCode: 'zk16vd', tmuxInstance: 'inst-1', cachedName: 'purdex' }
const takebackOk = { session_id: 'sid-1', archived: true }
const mockedTakeback = vi.mocked(nexTakeback)
const mockedTakeBack = vi.mocked(takeBack)

/** An execution tab whose primary pane carries `from`; returns the ids the view needs. */
function executionTab(): { tabId: string; paneId: string } {
  const tab = createTab({ kind: 'execution', executionId: E, host: H, from })
  useTabStore.getState().addTab(tab)
  return { tabId: tab.id, paneId: getPrimaryPane(tab.layout).id }
}
const paneContent = (tabId: string) => getPrimaryPane(useTabStore.getState().tabs[tabId].layout).content
const toast = () => useUndoToast.getState().toast
const takeBackBtn = () => screen.getByTestId('take-back') as HTMLButtonElement

function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((res) => { resolve = res })
  return { promise, resolve }
}

describe('ExecutionView — take back to terminal', () => {
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useUndoToast.setState({ toast: null })
    mockedTakeback.mockReset()
    mockedTakeBack.mockClear()
    vi.mocked(api.releaseLease).mockReset().mockResolvedValue(undefined)
    useExecutionStore.getState().setLease(H, E, { leaseId: 'ls_1', expiresAt: Date.now() + 100_000 })
  })

  it('no `from` → no take-back control', () => {
    render(<ExecutionView {...base} isActive />)
    expect(screen.queryByTestId('take-back')).toBeNull()
  })

  it("with `from` → the control is there; idle execution → no confirm, takeBack called with the held lease id and the hook's forget", async () => {
    mockedTakeback.mockResolvedValueOnce(takebackOk)
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    await act(async () => { fireEvent.click(takeBackBtn()) })
    expect(screen.queryByTestId('takeback-dialog')).toBeNull()
    expect(mockedTakeBack).toHaveBeenCalledTimes(1)
    expect(mockedTakeBack).toHaveBeenCalledWith({ hostId: H, executionId: E, from, leaseId: 'ls_1', tabId: ids.tabId, paneId: ids.paneId, forgetLease: forget })
    expect(mockedTakeback).toHaveBeenCalledWith(H, from.sessionCode, {
      expected_tmux_instance: from.tmuxInstance, execution_id: E, resume_command: 'claude --resume {id}', lease_id: 'ls_1',
    })
    expect(forget).toHaveBeenCalledTimes(1)
  })

  it('omits leaseId when this tab holds no lease', async () => {
    useExecutionStore.getState().setLease(H, E, null)
    mockedTakeback.mockResolvedValueOnce(takebackOk)
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    await act(async () => { fireEvent.click(takeBackBtn()) })
    expect(mockedTakeBack.mock.calls[0][0].leaseId).toBeUndefined()
    expect(mockedTakeback.mock.calls[0][2]).not.toHaveProperty('lease_id')
  })

  it('running execution → confirm dialog first; Cancel sends nothing, Confirm sends the take-back', async () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'running' }) as never)
    mockedTakeback.mockResolvedValueOnce(takebackOk)
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)

    fireEvent.click(takeBackBtn())
    expect(screen.getByTestId('takeback-dialog')).toBeInTheDocument()
    expect(screen.getByText(/taking it back interrupts it/i)).toBeInTheDocument()
    expect(mockedTakeBack).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('takeback-cancel'))
    expect(screen.queryByTestId('takeback-dialog')).toBeNull()
    expect(mockedTakeBack).not.toHaveBeenCalled()

    fireEvent.click(takeBackBtn())
    await act(async () => { fireEvent.click(screen.getByTestId('takeback-confirm')) })
    expect(screen.queryByTestId('takeback-dialog')).toBeNull()
    expect(mockedTakeBack).toHaveBeenCalledTimes(1)
    expect(mockedTakeBack.mock.calls[0][0]).toMatchObject({ leaseId: 'ls_1', forgetLease: forget })
  })

  it('success → the pane is the tmux-session content built from `from`, and the success toast shows', async () => {
    mockedTakeback.mockResolvedValueOnce(takebackOk)
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    await act(async () => { fireEvent.click(takeBackBtn()) })
    expect(paneContent(ids.tabId)).toEqual({
      kind: 'tmux-session', hostId: H, sessionCode: from.sessionCode, mode: 'terminal', cachedName: from.cachedName, tmuxInstance: from.tmuxInstance,
    })
    expect(toast()?.message).toBe('In the terminal now; the execution is archived.')
    expect(toast()?.action).toBeUndefined()
  })

  it('the button is busy while the request is in flight and a second click is ignored', async () => {
    const d = deferred<typeof takebackOk>()
    mockedTakeback.mockReturnValueOnce(d.promise)
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    fireEvent.click(takeBackBtn())
    await waitFor(() => expect(mockedTakeback).toHaveBeenCalledTimes(1))
    expect(takeBackBtn().disabled).toBe(true)
    fireEvent.click(takeBackBtn())
    expect(mockedTakeBack).toHaveBeenCalledTimes(1)
    await act(async () => { d.resolve(takebackOk) })
    expect(paneContent(ids.tabId).kind).toBe('tmux-session')
  })

  it('freezes execution writes while the take-back is pending: input, Interrupt and Terminate are disabled (R1-1)', async () => {
    const d = deferred<typeof takebackOk>()
    mockedTakeback.mockReturnValueOnce(d.promise)
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    const textbox = () => screen.getByRole('textbox') as HTMLTextAreaElement
    const interrupt = () => screen.getByRole('button', { name: /interrupt/i }) as HTMLButtonElement
    const terminate = () => screen.getByRole('button', { name: /^terminate$/i }) as HTMLButtonElement
    expect(textbox().disabled).toBe(false)
    expect(interrupt().disabled).toBe(false)
    expect(terminate().disabled).toBe(false)

    fireEvent.click(takeBackBtn())
    await waitFor(() => expect(mockedTakeback).toHaveBeenCalledTimes(1))
    expect(textbox().disabled).toBe(true)
    expect(interrupt().disabled).toBe(true)
    expect(terminate().disabled).toBe(true)
    // Clicks on the frozen controls must not reach the daemon.
    fireEvent.click(interrupt())
    fireEvent.click(terminate())
    expect(api.interruptExecution).not.toHaveBeenCalled()
    expect(api.terminateExecution).not.toHaveBeenCalled()

    await act(async () => { d.resolve(takebackOk) })
    expect(paneContent(ids.tabId).kind).toBe('tmux-session')
  })

  it('a failed take-back thaws the input, Interrupt and Terminate again (R1-1)', async () => {
    let reject!: (e: unknown) => void
    const failing = new Promise<typeof takebackOk>((_, rej) => { reject = rej })
    mockedTakeback.mockReturnValueOnce(failing)
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    fireEvent.click(takeBackBtn())
    await waitFor(() => expect(mockedTakeback).toHaveBeenCalledTimes(1))
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(true)
    await act(async () => { reject(new HandoffApiError(409, 'held_by', { code: 'held_by', principal: 'x' })) })
    expect(toast()?.message).toBe('The execution lease is held by x.')
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(false)
    expect((screen.getByRole('button', { name: /interrupt/i }) as HTMLButtonElement).disabled).toBe(false)
    expect((screen.getByRole('button', { name: /^terminate$/i }) as HTMLButtonElement).disabled).toBe(false)
    expect(paneContent(ids.tabId).kind).toBe('execution')
  })

  it('swapped:false (pane closed while in flight) → the "archived, pane gone" toast', async () => {
    const d = deferred<typeof takebackOk>()
    mockedTakeback.mockReturnValueOnce(d.promise)
    const ids = executionTab()
    const { unmount } = render(<ExecutionView {...base} {...ids} from={from} isActive />)
    fireEvent.click(takeBackBtn())
    await waitFor(() => expect(mockedTakeback).toHaveBeenCalledTimes(1))
    useTabStore.getState().closeTab(ids.tabId)
    unmount()
    await act(async () => { d.resolve(takebackOk) })
    expect(toast()?.message).toMatch(/archived, but its pane was already closed/)
  })

  it('HandoffApiError → error toast; pane untouched; lease not forgotten; button re-enabled', async () => {
    mockedTakeback.mockRejectedValueOnce(new HandoffApiError(404, 'session_missing', { code: 'session_missing' }))
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    await act(async () => { fireEvent.click(takeBackBtn()) })
    expect(toast()?.message).toBe('The tmux session no longer exists.')
    expect(paneContent(ids.tabId).kind).toBe('execution')
    expect(forget).not.toHaveBeenCalled()
    await waitFor(() => expect(takeBackBtn().disabled).toBe(false))
  })

  it('an error carrying session_id adds the manual-resume line', async () => {
    mockedTakeback.mockRejectedValueOnce(new HandoffApiError(409, 'cc_already_running', { code: 'cc_already_running', session_id: 'sid-4' }))
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    await act(async () => { fireEvent.click(takeBackBtn()) })
    expect(toast()?.message).toBe('Claude Code is already running in that session.\nResume by hand: claude --resume sid-4')
  })

  it('a non-API error falls back to the generic toast', async () => {
    mockedTakeback.mockRejectedValueOnce(new TypeError('boom'))
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    await act(async () => { fireEvent.click(takeBackBtn()) })
    expect(toast()?.message).toBe('Handoff failed (unknown).')
  })
})

describe('ExecutionView — take back with the real lease hook', () => {
  // The daemon consumed the lease as part of the take-back; the pane swap
  // unmounts this view, and its lease cleanup must NOT DELETE the lease again.
  beforeEach(async () => {
    const actual = await vi.importActual<typeof import('../../hooks/useExecutionLease')>('../../hooks/useExecutionLease')
    vi.mocked(lease.useExecutionLease).mockImplementation(actual.useExecutionLease)
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useUndoToast.setState({ toast: null })
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, [H]: { id: H, name: 'h' } as never } }))
    mockedTakeback.mockReset()
    vi.mocked(api.releaseLease).mockReset().mockResolvedValue(undefined)
    useExecutionStore.getState().setLease(H, E, { leaseId: 'ls_1', expiresAt: Date.now() + 100_000 })
  })

  it('success → unmount does not call releaseLease (the lease was forgotten before the swap)', async () => {
    mockedTakeback.mockResolvedValueOnce(takebackOk)
    const ids = executionTab()
    const { unmount } = render(<ExecutionView {...base} {...ids} from={from} isActive />)
    await act(async () => { fireEvent.click(takeBackBtn()) })
    expect(paneContent(ids.tabId).kind).toBe('tmux-session')
    expect(useExecutionStore.getState().executions[KEY].lease).toBeNull()
    unmount()
    await act(async () => {})
    expect(api.releaseLease).not.toHaveBeenCalled()
  })

  it('control: failure leaves the lease held, so unmount releases it (proves the spy is live)', async () => {
    mockedTakeback.mockRejectedValueOnce(new HandoffApiError(404, 'session_missing', { code: 'session_missing' }))
    const ids = executionTab()
    const { unmount } = render(<ExecutionView {...base} {...ids} from={from} isActive />)
    await act(async () => { fireEvent.click(takeBackBtn()) })
    expect(useExecutionStore.getState().executions[KEY].lease?.leaseId).toBe('ls_1')
    unmount()
    await act(async () => {})
    expect(api.releaseLease).toHaveBeenCalledWith(H, E, 'ls_1')
  })
})

describe('ExecutionView — take-back is refused while another write is in flight (re-review)', () => {
  beforeEach(() => { mockedTakeback.mockReset() })

  it('a pending send disables the take-back control', () => {
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    expect((takeBackBtn() as HTMLButtonElement).disabled).toBe(false)
    act(() => { patchExec({ pendingSend: true, pendingLocal: { text: 'hi', delivery: null } as Exec['pendingLocal'] }) })
    expect((takeBackBtn() as HTMLButtonElement).disabled).toBe(true)
    fireEvent.click(takeBackBtn())
    expect(mockedTakeback).not.toHaveBeenCalled()
  })

  it('an in-flight interrupt disables the take-back control until it settles', async () => {
    let resolveInterrupt!: (v: { turn_id: string; state: string }) => void
    vi.mocked(api.interruptExecution).mockReturnValueOnce(new Promise((res) => { resolveInterrupt = res }))
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    fireEvent.click(screen.getByRole('button', { name: /interrupt/i }))
    await waitFor(() => expect((takeBackBtn() as HTMLButtonElement).disabled).toBe(true))
    await act(async () => { resolveInterrupt({ turn_id: 't1', state: 'idle' }) })
    await waitFor(() => expect((takeBackBtn() as HTMLButtonElement).disabled).toBe(false))
  })
})

// ---- exec-to-terminal spec §4.2: "Take to terminal" on every claude execution ----

const mockedToTerminal = vi.mocked(nexTakeToTerminal)
const mockedTakeToTerminal = vi.mocked(takeToTerminal)
const newSession = { code: 'nw1234', name: 'repo-1', cwd: '/Users/w/repo', mode: 'terminal', tmux_instance: 'inst-9' }
const toTerminalOk = { session: newSession, session_id: 'sid-1', archived: true }

/** An execution tab with NO `from` (headless / CLI-delegated). */
function headlessTab(): { tabId: string; paneId: string } {
  const tab = createTab({ kind: 'execution', executionId: E, host: H })
  useTabStore.getState().addTab(tab)
  return { tabId: tab.id, paneId: getPrimaryPane(tab.layout).id }
}

describe('ExecutionView — take to terminal (no `from`)', () => {
  let fetchHost: ReturnType<typeof vi.fn>
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useUndoToast.setState({ toast: null })
    mockedTakeback.mockReset()
    mockedToTerminal.mockReset()
    mockedTakeBack.mockClear()
    mockedTakeToTerminal.mockClear()
    fetchHost = vi.fn().mockResolvedValue(undefined)
    useSessionStore.setState({ sessions: {}, fetchHost } as never)
    useExecutionStore.getState().setLease(H, E, { leaseId: 'ls_1', expiresAt: Date.now() + 100_000 })
  })

  describe('visibility: provider claude, a session id, and a state the daemon can settle', () => {
    const cases: Array<[string, Record<string, unknown>, boolean]> = [
      ['running + session_id', { state: 'running', session_id: 'sid' }, true],
      ['idle + resume_session_id only', { state: 'idle', resume_session_id: 'rsid' }, true],
      ['failed + session_id', { state: 'failed', session_id: 'sid' }, true],
      ['terminated + session_id', { state: 'terminated', session_id: 'sid' }, true],
      ['queued + resume_session_id (daemon would answer execution_not_settled)', { state: 'queued', resume_session_id: 'rsid' }, false],
      ['rejected + session_id', { state: 'rejected', session_id: 'sid' }, false],
      ['idle without any session id', { state: 'idle' }, false],
      ['idle + session_id but provider codex', { state: 'idle', session_id: 'sid', provider: 'codex' }, false],
      ['idle + session_id but archived (daemon answers execution_archived; codex R1 P1)', { state: 'idle', session_id: 'sid', archived: true }, false],
    ]
    it.each(cases)('%s → %s', (_name, extra, shown) => {
      useExecutionStore.getState().setSummary(H, E, summary(extra) as never)
      render(<ExecutionView {...base} {...headlessTab()} isActive />)
      expect(screen.queryByTestId('take-back') !== null).toBe(shown)
    })

    it('no summary yet → hidden', () => {
      useExecutionStore.setState({ executions: {} })
      useExecutionStore.getState().setHistoryLoaded(H, E, true)
      render(<ExecutionView {...base} {...headlessTab()} isActive />)
      expect(screen.queryByTestId('take-back')).toBeNull()
    })

    it('with `from` the control is there regardless (codex, queued): the session-bound path decides', () => {
      useExecutionStore.getState().setSummary(H, E, summary({ state: 'queued', provider: 'codex' }) as never)
      render(<ExecutionView {...base} {...executionTab()} from={from} isActive />)
      expect(screen.getByTestId('take-back')).toBeInTheDocument()
    })

    it('the label is "Take to terminal" in both cases', () => {
      useExecutionStore.getState().setSummary(H, E, summary({ state: 'idle', session_id: 'sid' }) as never)
      const { unmount } = render(<ExecutionView {...base} {...headlessTab()} isActive />)
      expect(takeBackBtn().textContent).toContain('Take to terminal')
      unmount()
      render(<ExecutionView {...base} {...executionTab()} from={from} isActive />)
      expect(takeBackBtn().textContent).toContain('Take to terminal')
    })
  })

  it('idle → no confirm; takeToTerminal (not takeBack) called with the summary cwd, the held lease id and the hook\'s forget; pane becomes the new session; success toast', async () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'idle', session_id: 'sid', cwd: '/Users/w/repo' }) as never)
    mockedToTerminal.mockResolvedValueOnce(toTerminalOk)
    const ids = headlessTab()
    render(<ExecutionView {...base} {...ids} isActive />)
    await act(async () => { fireEvent.click(takeBackBtn()) })
    expect(screen.queryByTestId('takeback-dialog')).toBeNull()
    expect(mockedTakeBack).not.toHaveBeenCalled()
    expect(mockedTakeToTerminal).toHaveBeenCalledTimes(1)
    expect(mockedTakeToTerminal).toHaveBeenCalledWith({ hostId: H, executionId: E, cwd: '/Users/w/repo', leaseId: 'ls_1', tabId: ids.tabId, paneId: ids.paneId, forgetLease: forget })
    expect(mockedToTerminal).toHaveBeenCalledWith(H, E, { session_name: 'repo-1', resume_command: 'claude --resume {id}', lease_id: 'ls_1' })
    expect(forget).toHaveBeenCalledTimes(1)
    expect(paneContent(ids.tabId)).toEqual({ kind: 'tmux-session', hostId: H, sessionCode: 'nw1234', mode: 'terminal', cachedName: 'repo-1', tmuxInstance: 'inst-9' })
    expect(toast()?.message).toBe('In the terminal now; the execution is archived.')
    expect(fetchHost).toHaveBeenCalledWith(H)
  })

  it('omits leaseId when this tab holds no lease', async () => {
    useExecutionStore.getState().setLease(H, E, null)
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'idle', session_id: 'sid' }) as never)
    mockedToTerminal.mockResolvedValueOnce(toTerminalOk)
    render(<ExecutionView {...base} {...headlessTab()} isActive />)
    await act(async () => { fireEvent.click(takeBackBtn()) })
    expect(mockedTakeToTerminal.mock.calls[0][0].leaseId).toBeUndefined()
    expect(mockedToTerminal.mock.calls[0][2]).not.toHaveProperty('lease_id')
  })

  it('running → the shared confirm dialog first; Cancel sends nothing, Confirm calls takeToTerminal', async () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'running', session_id: 'sid' }) as never)
    mockedToTerminal.mockResolvedValueOnce(toTerminalOk)
    render(<ExecutionView {...base} {...headlessTab()} isActive />)
    fireEvent.click(takeBackBtn())
    expect(screen.getByTestId('takeback-dialog')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('takeback-cancel'))
    expect(mockedTakeToTerminal).not.toHaveBeenCalled()
    fireEvent.click(takeBackBtn())
    await act(async () => { fireEvent.click(screen.getByTestId('takeback-confirm')) })
    expect(mockedTakeToTerminal).toHaveBeenCalledTimes(1)
    expect(mockedTakeBack).not.toHaveBeenCalled()
  })

  it('the control is busy while the request is in flight; a failure re-enables it, leaves the pane and the lease alone and toasts the mapped message', async () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'idle', session_id: 'sid' }) as never)
    const d = deferred<typeof toTerminalOk>()
    mockedToTerminal.mockReturnValueOnce(d.promise)
    const ids = headlessTab()
    render(<ExecutionView {...base} {...ids} isActive />)
    fireEvent.click(takeBackBtn())
    await waitFor(() => expect(takeBackBtn().disabled).toBe(true))
    fireEvent.click(takeBackBtn())
    expect(mockedToTerminal).toHaveBeenCalledTimes(1)
    await act(async () => { d.resolve(toTerminalOk) })

    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    mockedToTerminal.mockRejectedValueOnce(new HandoffApiError(409, 'cwd_missing', { code: 'cwd_missing' }))
    const ids2 = headlessTab()
    render(<ExecutionView {...base} {...ids2} isActive />)
    await act(async () => { fireEvent.click(screen.getAllByTestId('take-back').at(-1)!) })
    expect(toast()?.message).toBe("The execution's working directory no longer exists on the host.")
    expect(paneContent(ids2.tabId).kind).toBe('execution')
    await waitFor(() => expect((screen.getAllByTestId('take-back').at(-1) as HTMLButtonElement).disabled).toBe(false))
  })

  it('cc_start_timeout with session_id → manual-resume line (the daemon killed the session it created)', async () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'idle', session_id: 'sid' }) as never)
    mockedToTerminal.mockRejectedValueOnce(new HandoffApiError(504, 'cc_start_timeout', { code: 'cc_start_timeout', session_id: 'sid-4' }))
    render(<ExecutionView {...base} {...headlessTab()} isActive />)
    await act(async () => { fireEvent.click(takeBackBtn()) })
    expect(toast()?.message).toBe('Claude Code did not start in the pane in time.\nResume by hand: claude --resume sid-4')
    expect(forget).not.toHaveBeenCalled()
  })

  it('session_create_failed with session_alive → the session list is refreshed so the orphan shows up', async () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'idle', session_id: 'sid' }) as never)
    mockedToTerminal.mockRejectedValueOnce(new HandoffApiError(500, 'session_create_failed', { code: 'session_create_failed', session_name: 'repo-1', session_alive: true }))
    render(<ExecutionView {...base} {...headlessTab()} isActive />)
    await act(async () => { fireEvent.click(takeBackBtn()) })
    expect(toast()?.message).toBe('Could not create the tmux session repo-1; check the session list.')
    expect(fetchHost).toHaveBeenCalledWith(H)
  })
})

// ---- P-B4 spec §4.2: header cost anchor through the real store wiring ----

describe('ExecutionView — header cost (P-B4 H1, P6, G3)', () => {
  const resultFrame = (seq: number, total_cost_usd: number, parent_tool_use_id: string | null = null) => ({
    seq, execution_id: E, kind: 'result', created_at: seq,
    payload: { type: 'result', subtype: 'success', is_error: false, parent_tool_use_id, total_cost_usd, duration_ms: 1, duration_api_ms: 1, num_turns: 1 },
  })
  const costBtn = () => screen.getByTestId('execution-cost') as HTMLButtonElement

  it('(a) historyLoaded=false → `$…`, disabled', () => {
    useExecutionStore.getState().setHistoryLoaded(H, E, false)
    render(<ExecutionView {...base} isActive />)
    expect(costBtn().textContent).toBe('$…')
    expect(costBtn().disabled).toBe(true)
  })

  it('(b)–(d) sums top-level results once loaded, updates live, ignores subagent results', () => {
    act(() => { useExecutionStore.getState().applyEvents(H, E, [resultFrame(1, 0.01), resultFrame(2, 0.02)]) })
    useExecutionStore.getState().setHistoryLoaded(H, E, true)
    render(<ExecutionView {...base} isActive />)
    expect(costBtn().textContent).toBe('$0.03')
    expect(costBtn().disabled).toBe(false)

    act(() => { useExecutionStore.getState().applyEvents(H, E, [resultFrame(3, 0.03)]) })
    expect(costBtn().textContent).toBe('$0.06')

    act(() => { useExecutionStore.getState().applyEvents(H, E, [resultFrame(4, 1, 'toolu_x')]) })
    expect(costBtn().textContent).toBe('$0.06')
  })
})
