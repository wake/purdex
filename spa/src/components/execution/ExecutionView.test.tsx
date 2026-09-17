import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import ExecutionView from './ExecutionView'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { NexApiError } from '../../lib/nex/types'
import * as api from '../../lib/nex/nex-api'
import * as lease from '../../hooks/useExecutionLease'
import * as sub from '../../hooks/useExecutionSubscription'

vi.mock('../../lib/nex/nex-api', () => ({ sendMessage: vi.fn(), interruptExecution: vi.fn(), terminateExecution: vi.fn() }))
vi.mock('../../hooks/useExecutionSubscription', () => ({ useExecutionSubscription: vi.fn(() => ({ problem: null, paused: false })) }))
vi.mock('../../hooks/useExecutionLease', () => ({ useExecutionLease: vi.fn() }))
vi.mock('../../lib/nex/client-id', () => ({ getNexClientId: () => 't-me000000' }))

const H = 'h', E = 'exc_1', KEY = 'h:exc_1'
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
})

describe('ExecutionView', () => {
  it('renders header facts from the summary', () => {
    useExecutionStore.getState().setSummary(H, E, summary({ lease: { principal_id: 'pdx:mlab/t-me000000', expires_at: 1 } }) as never)
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    expect(screen.getByTestId('execution-state')).toHaveTextContent('idle')
    expect(screen.getByText(/standard/)).toBeInTheDocument()
    expect(screen.getByText(/repo/)).toBeInTheDocument()
    expect(screen.getByText(/\(you\)/)).toBeInTheDocument()
  })

  it('send: optimistic bubble, lease acquired, message posted, queued tag shown', async () => {
    vi.mocked(api.sendMessage).mockResolvedValueOnce({ turn_id: 't1', delivery: 'queued' })
    render(<ExecutionView hostId={H} executionId={E} isActive />)
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
    render(<ExecutionView hostId={H} executionId={E} isActive />)
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
    render(<ExecutionView hostId={H} executionId={E} isActive />)
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
    render(<ExecutionView hostId={H} executionId={E} isActive />)
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
    render(<ExecutionView hostId={H} executionId={E} isActive />)
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
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'hello' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(screen.getByTestId('send-error')).toBeInTheDocument())
    expect(forget).toHaveBeenCalledTimes(1)
    expect(useExecutionStore.getState().executions[KEY].sendError?.code).toBe('lease_mismatch')
  })

  it('interrupt acquires the lease and posts; no_live_turn is silent', async () => {
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    fireEvent.click(screen.getByRole('button', { name: /interrupt/i }))
    await waitFor(() => expect(api.interruptExecution).toHaveBeenCalledWith(H, E, 'ls_1'))
    vi.mocked(api.interruptExecution).mockRejectedValueOnce(new NexApiError(409, 'no_live_turn', 'nothing'))
    fireEvent.click(screen.getByRole('button', { name: /interrupt/i }))
    await act(async () => {})
    expect(screen.queryByTestId('send-error')).not.toBeInTheDocument()
  })

  it('terminate needs two clicks, then acquires the lease and posts', async () => {
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    fireEvent.click(screen.getByRole('button', { name: /^terminate$/i }))
    expect(api.terminateExecution).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: /confirm terminate/i }))
    await waitFor(() => expect(api.terminateExecution).toHaveBeenCalledWith(H, E, 'ls_1'))
  })

  it('disables input with a reason when archived or ended', () => {
    useExecutionStore.getState().setSummary(H, E, summary({ archived: true }) as never)
    const { rerender } = render(<ExecutionView hostId={H} executionId={E} isActive />)
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(true)
    expect(screen.getByRole('textbox')).toHaveAttribute('placeholder', expect.stringMatching(/archived/i))
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'terminated' }) as never)
    rerender(<ExecutionView hostId={H} executionId={E} isActive />)
    expect(screen.getByRole('textbox')).toHaveAttribute('placeholder', expect.stringMatching(/ended/i))
  })

  it('disables the input until history has loaded', () => {
    useExecutionStore.getState().setHistoryLoaded(H, E, false)
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    // The loading placeholder replaces the conversation, but StreamInput is
    // still rendered below it — must stay disabled while spinner is up.
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(true)
  })

  it('a terminally closed live stream (with error) disables input with a disconnected placeholder', () => {
    useExecutionStore.getState().setSse(H, E, 'closed', 'forbidden')
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(true)
    expect(screen.getByRole('textbox')).toHaveAttribute('placeholder', expect.stringMatching(/lost/i))
  })

  it('sse closed with no error (e.g. an in-progress reconnect backoff) does not disable input', () => {
    useExecutionStore.getState().setSse(H, E, 'closed', null)
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(false)
  })

  it('archived: input is disabled but Terminate stays enabled (not a terminal state)', () => {
    useExecutionStore.getState().setSummary(H, E, summary({ archived: true }) as never)
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(true)
    expect(screen.getByRole('button', { name: /^terminate$/i })).not.toBeDisabled()
  })

  it('shows the thinking indicator once a delivered send has no reply yet', async () => {
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'hello' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(useExecutionStore.getState().executions[KEY].pendingLocal?.delivery).toBe('delivered'))
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
  })

  it('hides the thinking indicator while the send is still queued', async () => {
    vi.mocked(api.sendMessage).mockResolvedValueOnce({ turn_id: 't2', delivery: 'queued' })
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'hi' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(useExecutionStore.getState().executions[KEY].pendingLocal?.delivery).toBe('queued'))
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
  })

  it('renders the problem states instead of the conversation', () => {
    vi.mocked(sub.useExecutionSubscription).mockReturnValue({ problem: 'not_found', paused: false })
    const { rerender } = render(<ExecutionView hostId={H} executionId={E} isActive />)
    expect(screen.getByText(/not found/i)).toBeInTheDocument()
    vi.mocked(sub.useExecutionSubscription).mockReturnValue({ problem: 'host_removed', paused: false })
    rerender(<ExecutionView hostId={H} executionId={E} isActive />)
    expect(screen.getByText(/host removed/i)).toBeInTheDocument()
    vi.mocked(sub.useExecutionSubscription).mockReturnValue({ problem: 'nex_disabled', paused: false })
    rerender(<ExecutionView hostId={H} executionId={E} isActive />)
    expect(screen.getByText(/not enabled/i)).toBeInTheDocument()
  })

  it('shows the loading state until history is loaded', () => {
    useExecutionStore.getState().setHistoryLoaded(H, E, false)
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    expect(screen.getByTestId('execution-loading')).toBeInTheDocument()
  })

  it('shows the retrying error text under the loading line while a retry chain is failing', () => {
    useExecutionStore.getState().setHistoryLoaded(H, E, false)
    useExecutionStore.getState().setSse(H, E, 'closed', 'Failed to fetch')
    render(<ExecutionView hostId={H} executionId={E} isActive />)
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
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
  })

  it('R3: turnLive with visible partial text → thinking indicator absent, typewriter present', () => {
    patchExec({ turnLive: true, partial: textPartial('tokens flowing') })
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
    expect(screen.getByTestId('partial-group')).toHaveTextContent('tokens flowing')
    expect(screen.getByTestId('stream-cursor')).toBeInTheDocument()
  })

  it('R3: turnLive with a partial whose blocks are all empty → thinking indicator still present', () => {
    patchExec({ turnLive: true, partial: textPartial('') })
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
  })

  it('R3: pendingSend queued without turnLive → thinking indicator absent', () => {
    patchExec({ pendingSend: true, pendingLocal: { text: 'hi', delivery: 'queued' } as Exec['pendingLocal'] })
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
  })

  it('R3: neither turnLive nor pendingSend → thinking indicator absent', () => {
    render(<ExecutionView hostId={H} executionId={E} isActive />)
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
  })
})

describe('ExecutionView — tool activity (R2) and the elapsed ticker', () => {
  it('R2: a running tool is marked aborted after execution.turn_orphaned', () => {
    render(<ExecutionView hostId={H} executionId={E} isActive />)
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
      render(<ExecutionView hostId={H} executionId={E} isActive />)
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
