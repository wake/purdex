import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { useEffect, useState } from 'react'
import { render, screen, fireEvent, act, waitFor, within } from '@testing-library/react'
import ExecutionView from './ExecutionView'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { useTabStore } from '../../stores/useTabStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useHostStore } from '../../stores/useHostStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { useHostConfigStore, emptyHostConfigEntry } from '../../stores/useHostConfigStore'
import { NexApiError } from '../../lib/nex/types'
import { useSessionStore } from '../../stores/useSessionStore'
import { HandoffApiError, nexTakeback, nexTakeToTerminal } from '../../lib/nex/handoff-api'
import { takeBack, takeToTerminal } from '../../lib/nex/handoff'
import { exitWorker } from '../../lib/nex/exit-worker'
import { getTakeToTerminal } from '../../lib/nex/take-to-terminal-registry'
import { createTab } from '../../types/tab'
import { getPrimaryPane } from '../../lib/pane-tree'
import * as api from '../../lib/nex/nex-api'
import * as lease from '../../hooks/useExecutionLease'
import * as sub from '../../hooks/useExecutionSubscription'

vi.mock('../../lib/nex/nex-api', () => ({ sendMessage: vi.fn(), interruptExecution: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), uploadWorkerFile: vi.fn(), fetchExecutionPrelude: vi.fn() }))
vi.mock('../../hooks/useExecutionSubscription', () => ({ useExecutionSubscription: vi.fn(() => ({ problem: null, paused: false })) }))
vi.mock('../../hooks/useExecutionLease', () => ({ useExecutionLease: vi.fn() }))
vi.mock('../../lib/nex/client-id', () => ({ getNexClientId: () => 't-me000000' }))
// The take-back path runs the real orchestration (store swap, forget-before-
// swap) against a mocked daemon call; `takeBack` itself is a pass-through spy
// so the view's call shape (lease id, forgetLease identity) is observable.
vi.mock('../../lib/nex/exit-worker', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/nex/exit-worker')>()),
  exitWorker: vi.fn(),
}))
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
const base = { hostId: H, executionId: E, tabId: 't1', paneId: 'p1', onModeChange: () => {} }
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
  vi.mocked(exitWorker).mockReset().mockResolvedValue({ exited: true, terminated: true, archived: true, state: 'terminated' })
  useUndoToast.setState({ toast: null, notice: null })
  useExecutionStore.getState().setSummary(H, E, summary() as never)
  useExecutionStore.getState().setHistoryLoaded(H, E, true)
  // Take back / Take to terminal re-point the pane only on a host shown in the workbench (host ownership H2d-3).
  useShownHostsStore.setState({ ids: [H] })
  // Quick replies (R3): no daemon here — the host's list is whatever a test seeds.
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
})

describe('ExecutionView', () => {
  // Worker pane theme spec §4.1: the pane root carries the theme id and its
  // `--wt-*` vars, defaulting to Purdex.
  it('sets the worker theme on the pane root', () => {
    render(<ExecutionView {...base} isActive />)
    const root = screen.getByTestId('execution-view')
    expect(root.dataset.workerTheme).toBe('purdex')
    expect(root.style.getPropertyValue('--wt-font-size')).toBe('14px')
  })

  it('renders header facts from the summary', () => {
    useExecutionStore.getState().setSummary(H, E, summary({ lease: { principal_id: 'pdx:mlab/t-me000000', expires_at: 1 } }) as never)
    render(<ExecutionView {...base} isActive />)
    expect(screen.getByTestId('execution-state')).toHaveTextContent('idle')
    expect(screen.getByTestId('worker-name')).toHaveTextContent('repo')
    // Worker pane spec §4.7: provider/profile moved to the name's popover.
    expect(screen.queryByText(/standard/)).toBeNull()
  })

  // Worker pane spec §4.6/§4.7 (Q3): observers, lease and SSE left the header
  // for the dock, which sits inside the pane between the transcript and the input.
  it('shows observers, lease and sse in the dock between the transcript and the input, not in the header', () => {
    useExecutionStore.getState().setSummary(H, E, summary({ lease: { principal_id: 'pdx:mlab/t-me000000', expires_at: 1 } }) as never)
    render(<ExecutionView {...base} isActive />)
    const dock = screen.getByTestId('worker-dock')
    expect(within(dock).getByTestId('worker-dock-row')).toHaveTextContent(/2 observers · lease: you/)
    expect(screen.queryByTestId('execution-sse')).toBeNull()
    expect(screen.queryByTestId('execution-lease')).toBeNull()
    expect(screen.queryByText(/3 turns/)).toBeNull()
    const transcript = screen.getByText('No messages yet.')
    const input = screen.getByRole('textbox')
    expect(transcript.compareDocumentPosition(dock) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(dock.compareDocumentPosition(input) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
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
    // The restore mechanism is a `key={draft}` remount (see WorkerInput /
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
    // The dock's lease entry also renders the holder's principal, so
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

  it('exits an idle worker without a confirm and passes the held lease', async () => {
    useExecutionStore.getState().setLease(H, E, { leaseId: 'L1', expiresAt: Date.now() + 100_000 })
    render(<ExecutionView {...base} isActive />)
    fireEvent.click(screen.getByTestId('header-exit'))
    await waitFor(() => expect(exitWorker).toHaveBeenCalledWith(expect.objectContaining({ hostId: H, executionId: E, leaseId: 'L1', forgetLease: forget })))
    expect(screen.queryByTestId('exit-confirm')).toBeNull()
  })

  it('asks before exiting a running worker', async () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'running' }) as never)
    render(<ExecutionView {...base} isActive />)
    fireEvent.click(screen.getByTestId('header-exit'))
    expect(exitWorker).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('exit-confirm'))
    await waitFor(() => expect(exitWorker).toHaveBeenCalledTimes(1))
  })

  it('lets a failed worker exit; a terminated or archived one cannot', () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'failed' }) as never)
    const { rerender } = render(<ExecutionView {...base} isActive />)
    expect(screen.getByTestId('header-exit')).toBeEnabled()
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'terminated' }) as never)
    rerender(<ExecutionView {...base} isActive />)
    expect(screen.getByTestId('header-exit')).toBeDisabled()
  })

  it('shows the held_by message on refusal and thaws the button', async () => {
    vi.mocked(exitWorker).mockRejectedValueOnce(new HandoffApiError(409, 'held_by', { principal: 'ploom:agent-7' }))
    render(<ExecutionView {...base} isActive />)
    fireEvent.click(screen.getByTestId('header-exit'))
    await waitFor(() => expect(useUndoToast.getState().toast?.message).toContain('ploom:agent-7'))
    await waitFor(() => expect(screen.getByTestId('header-exit')).toBeEnabled())
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
    // The loading placeholder replaces the conversation, but WorkerInput is
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

  it('archived: input and 退出 are disabled (not a live row)', () => {
    useExecutionStore.getState().setSummary(H, E, summary({ archived: true }) as never)
    render(<ExecutionView {...base} isActive />)
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(true)
    expect(screen.getByTestId('header-exit')).toBeDisabled()
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

// ---- worker pane R1 T4.4: the pane renders the room transcript ------------

describe('ExecutionView — room transcript (T4.4)', () => {
  const said = (text: string) =>
    ({ type: 'user', message: { role: 'user', content: [{ type: 'text', text }], stop_reason: null } }) as Exec['messages'][number]

  it('draws the optimistic line at the left edge, dimmed, inside a provisional turn', () => {
    patchExec({ messages: [said('first')], turnStarts: [0], pendingLocal: { text: 'second', delivery: 'queued' } as Exec['pendingLocal'] })
    const { container } = render(<ExecutionView {...base} isActive />)
    const turns = screen.getAllByTestId('room-turn')
    expect(turns).toHaveLength(2)
    expect(turns[1]).toHaveAttribute('data-turn-index', '1')
    const line = within(turns[1]).getByTestId('room-user-line')
    expect(line).toHaveTextContent('second')
    expect(line.className).toContain('opacity-60')
    expect(within(line).getByTestId('room-user-prefix')).toBeInTheDocument()
    expect(within(line).getByText(/queued/i)).toBeInTheDocument()
    expect(container.querySelector('.justify-end')).toBeNull()
  })

  it('groups the transcript by the turns the reducer recorded', () => {
    patchExec({ messages: [said('one'), said('two')], turnStarts: [0, 1] })
    render(<ExecutionView {...base} isActive />)
    const turns = screen.getAllByTestId('room-turn')
    expect(turns).toHaveLength(2)
    expect(within(turns[0]).getByTestId('room-user-line')).toHaveTextContent('one')
    expect(within(turns[1]).getByTestId('room-user-line')).toHaveTextContent('two')
  })
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
    expect(screen.getByTestId('op-dot')).toHaveClass('animate-spin')
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
    expect(screen.getByTestId('op-dot')).toHaveClass('animate-spin')
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
    // Turn still live, no partial: the model is silent again, so the dots come back.
    act(() => { useExecutionStore.getState().applyEvents(H, E, [toolResultFrame(2, 9_000)]) })
    expect(useExecutionStore.getState().executions[KEY].turnLive).toBe(true)
    expect(screen.getByTestId('op-dot')).not.toHaveClass('animate-spin')
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
  })
})

describe('ExecutionView — tool activity (R2) and the elapsed ticker', () => {
  it('R2: a running tool is marked aborted after execution.turn_orphaned', () => {
    render(<ExecutionView {...base} isActive />)
    act(() => { useExecutionStore.getState().applyEvents(H, E, [toolUseFrame(1, 5_000)]) })
    expect(screen.getByTestId('op-dot')).toHaveClass('animate-spin')
    act(() => {
      useExecutionStore.getState().applyEvents(H, E, [{ seq: 2, execution_id: E, kind: 'execution.turn_orphaned', payload: { turn_id: 't1' }, created_at: 9_000 }])
    })
    expect(screen.getByTestId('op-aborted')).toBeInTheDocument()
    expect(screen.getByTestId('op-dot')).not.toHaveClass('animate-spin')
    expect(screen.queryByTestId('op-elapsed')).not.toBeInTheDocument()
  })

  it('ticker: the elapsed badge advances every second while a tool runs and stops once its result lands', () => {
    vi.useFakeTimers()
    try {
      vi.setSystemTime(10_000)
      render(<ExecutionView {...base} isActive />)
      const idleTimers = vi.getTimerCount()
      act(() => { useExecutionStore.getState().applyEvents(H, E, [toolUseFrame(1, 10_000)]) })
      // Sub-second elapsed is not shown at all (spec §3.1.1 #3), but the
      // ticker is already running — that is what the timer count proves.
      expect(screen.queryByTestId('op-elapsed')).not.toBeInTheDocument()
      expect(vi.getTimerCount()).toBe(idleTimers + 1)
      act(() => { vi.advanceTimersByTime(1_000) })
      expect(screen.getByTestId('op-elapsed')).toHaveTextContent('1.0s')
      act(() => { vi.advanceTimersByTime(1_000) })
      expect(screen.getByTestId('op-elapsed')).toHaveTextContent('2.0s')
      // Any one-shot timers from mount have fired by now; only the interval is left.
      expect(vi.getTimerCount()).toBe(1)

      act(() => { useExecutionStore.getState().applyEvents(H, E, [toolResultFrame(2, 16_200)]) })
      expect(screen.queryByTestId('op-elapsed')).not.toBeInTheDocument()
      expect(screen.getByTestId('op-duration')).toHaveTextContent('6.2s')
      // The interval is cleared: no tool is running any more.
      expect(vi.getTimerCount()).toBe(0)
      act(() => { vi.advanceTimersByTime(5_000) })
      expect(screen.getByTestId('op-duration')).toHaveTextContent('6.2s')
    } finally {
      vi.useRealTimers()
    }
  })
})

// ---- worker pane R1 T5.3 (#1228): the ticker and the dots see child tools --
//
// `anyRunning` spans the whole `tools` map, so once the reducer keeps a
// subagent's tool entries they drive the ticker and suppress the dots too.
// Each test isolates the child's own entry (no parent Task tool_use is
// recorded), so the ticker's state is decided by the child alone.

const PARENT_TOOL = 'tu_task'
const childToolUseFrame = (seq: number, created_at: number) => ({
  seq, execution_id: E, kind: 'assistant', created_at,
  payload: { type: 'assistant', parent_tool_use_id: PARENT_TOOL, message: { id: 'm_child', role: 'assistant', content: [{ type: 'tool_use', id: 'tu_c1', name: 'Read', input: { file_path: '/a' } }], stop_reason: null } },
})
const childToolResultFrame = (seq: number, created_at: number) => ({
  seq, execution_id: E, kind: 'user', created_at,
  payload: { type: 'user', parent_tool_use_id: PARENT_TOOL, message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 'tu_c1', content: 'ok', is_error: false }], stop_reason: null } },
})
const resultFrame = (seq: number, created_at: number, parent: string | null) => ({
  seq, execution_id: E, kind: 'result', created_at,
  payload: { type: 'result', subtype: 'success', parent_tool_use_id: parent },
})

describe('ExecutionView — subagent tools drive the ticker and the dots (T5.3)', () => {
  const withFakeTimers = (body: () => void) => {
    vi.useFakeTimers()
    try {
      vi.setSystemTime(10_000)
      body()
    } finally {
      vi.useRealTimers()
    }
  }
  const apply = (...frames: Parameters<ReturnType<typeof useExecutionStore.getState>['applyEvents']>[2]) =>
    act(() => { useExecutionStore.getState().applyEvents(H, E, frames) })
  const childTool = () => useExecutionStore.getState().executions[KEY].tools.tu_c1

  it("the ticker stops when a child's tool result arrives", () => {
    withFakeTimers(() => {
      patchExec({ turnLive: true })
      render(<ExecutionView {...base} isActive />)
      const idleTimers = vi.getTimerCount()
      apply(childToolUseFrame(1, 10_000))
      expect(childTool().status).toBe('running')
      expect(vi.getTimerCount()).toBe(idleTimers + 1)
      apply(childToolResultFrame(2, 12_000))
      expect(childTool().status).toBe('done')
      expect(vi.getTimerCount()).toBe(idleTimers)
    })
  })

  it('the main result stops the ticker even when a child tool never reported', () => {
    withFakeTimers(() => {
      patchExec({ turnLive: true })
      render(<ExecutionView {...base} isActive />)
      const idleTimers = vi.getTimerCount()
      apply(childToolUseFrame(1, 10_000))
      expect(vi.getTimerCount()).toBe(idleTimers + 1)
      // No child tool_result ever lands; the parent turn's own result is the backstop.
      apply(resultFrame(2, 15_000, null))
      expect(childTool().status).toBe('aborted')
      expect(useExecutionStore.getState().executions[KEY].turnLive).toBe(false)
      expect(vi.getTimerCount()).toBe(idleTimers)
    })
  })

  it("a child's own result frame does not stop the ticker while the parent turn is still running", () => {
    withFakeTimers(() => {
      patchExec({ turnLive: true })
      render(<ExecutionView {...base} isActive />)
      const idleTimers = vi.getTimerCount()
      apply(childToolUseFrame(1, 10_000))
      expect(vi.getTimerCount()).toBe(idleTimers + 1)
      // The subagent says it is done, but its tool never reported: that is
      // not a turn end, so the tool stays running and the clock keeps going.
      apply(resultFrame(2, 12_000, PARENT_TOOL))
      expect(useExecutionStore.getState().executions[KEY].turnLive).toBe(true)
      expect(childTool().status).toBe('running')
      expect(vi.getTimerCount()).toBe(idleTimers + 1)
    })
  })

  it('a running child tool suppresses the thinking dots', () => {
    patchExec({ turnLive: true })
    render(<ExecutionView {...base} isActive />)
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
    act(() => { useExecutionStore.getState().applyEvents(H, E, [childToolUseFrame(1, 5_000)]) })
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
    act(() => { useExecutionStore.getState().applyEvents(H, E, [childToolResultFrame(2, 6_000)]) })
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
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
/**
 * The header's view menu, opened unless it already is (R2 plan T1.2 moved
 * "Take to terminal" there as the 終端機 item). The last trigger / item wins,
 * so a test that renders a second view reaches that view's menu.
 */
const openViewMenu = () => {
  const trigger = screen.getAllByTestId('view-mode').at(-1)!
  if (trigger.getAttribute('aria-expanded') !== 'true') fireEvent.click(trigger)
}
const takeBackBtn = () => {
  openViewMenu()
  return screen.getAllByTestId('view-mode-terminal').at(-1) as HTMLButtonElement
}
/**
 * Clicks 終端機 and lets the take-back settle. The menu is opened outside the
 * async `act` — inside it the trigger's state update would not flush before
 * the item is looked up.
 */
const clickTakeBack = async () => {
  const item = takeBackBtn()
  await act(async () => { fireEvent.click(item) })
}
/** Whether the view menu offers 終端機 (the trigger is always there: the pane can always switch views). */
const offersTerminal = () => {
  openViewMenu()
  return screen.queryByTestId('view-mode-terminal') !== null
}

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
    expect(offersTerminal()).toBe(false)
  })

  it("with `from` → the control is there; idle execution → no confirm, takeBack called with the held lease id and the hook's forget", async () => {
    mockedTakeback.mockResolvedValueOnce(takebackOk)
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    await clickTakeBack()
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
    await clickTakeBack()
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
    await clickTakeBack()
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

  it('freezes execution writes while the take-back is pending: input, Interrupt and 退出 are disabled (R1-1)', async () => {
    const d = deferred<typeof takebackOk>()
    mockedTakeback.mockReturnValueOnce(d.promise)
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    const textbox = () => screen.getByRole('textbox') as HTMLTextAreaElement
    const interrupt = () => screen.getByRole('button', { name: /interrupt/i }) as HTMLButtonElement
    const terminate = () => screen.getByTestId('header-exit') as HTMLButtonElement
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
    expect(exitWorker).not.toHaveBeenCalled()

    await act(async () => { d.resolve(takebackOk) })
    expect(paneContent(ids.tabId).kind).toBe('tmux-session')
  })

  it('a take-back whose worker could not exit leaves a persistent notice; exited:true leaves none', async () => {
    mockedTakeback.mockResolvedValueOnce({ ...takebackOk, exited: false, exit_error: 'terminate_failed' } as never)
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    await clickTakeBack()
    expect(useUndoToast.getState().notice?.message).toBe('Resumed in the terminal, but the worker could not exit; exit it manually from the list.')
  })

  it('a take-back with exited:true shows no notice', async () => {
    mockedTakeback.mockResolvedValueOnce({ ...takebackOk, exited: true } as never)
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    await clickTakeBack()
    expect(useUndoToast.getState().notice).toBeNull()
  })

  it('a failed take-back thaws the input, Interrupt and 退出 again (R1-1)', async () => {
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
    expect((screen.getByTestId('header-exit') as HTMLButtonElement).disabled).toBe(false)
    expect(paneContent(ids.tabId).kind).toBe('execution')
  })

  // P5 review A2: a take-back ending thaws the input; that is not a send
  // coming back, so the reply box of the focus target stays unfocused.
  it('a failed take-back thawing the input does not pull focus into the reply box', async () => {
    let reject!: (e: unknown) => void
    const failing = new Promise<typeof takebackOk>((_, rej) => { reject = rej })
    mockedTakeback.mockReturnValueOnce(failing)
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive isFocusTarget />)
    await act(() => new Promise<void>((r) => requestAnimationFrame(() => r()))) // the activation frame
    fireEvent.click(takeBackBtn())
    await waitFor(() => expect(mockedTakeback).toHaveBeenCalledTimes(1))
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(true)
    ;(document.activeElement as HTMLElement | null)?.blur()
    await act(async () => { reject(new HandoffApiError(409, 'held_by', { code: 'held_by', principal: 'x' })) })
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(false)
    expect(document.activeElement).toBe(document.body)
    await act(() => new Promise<void>((r) => requestAnimationFrame(() => r())))
    expect(screen.getByRole('textbox')).not.toHaveFocus()
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
    await clickTakeBack()
    expect(toast()?.message).toBe('The tmux session no longer exists.')
    expect(paneContent(ids.tabId).kind).toBe('execution')
    expect(forget).not.toHaveBeenCalled()
    await waitFor(() => expect(takeBackBtn().disabled).toBe(false))
  })

  it('an error carrying session_id adds the manual-resume line', async () => {
    mockedTakeback.mockRejectedValueOnce(new HandoffApiError(409, 'cc_already_running', { code: 'cc_already_running', session_id: 'sid-4' }))
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    await clickTakeBack()
    expect(toast()?.message).toBe('Claude Code is already running in that session.\nResume by hand: claude --resume sid-4')
  })

  it('a non-API error falls back to the generic toast', async () => {
    mockedTakeback.mockRejectedValueOnce(new TypeError('boom'))
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    await clickTakeBack()
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
    await clickTakeBack()
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
    await clickTakeBack()
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
      expect(offersTerminal()).toBe(shown)
    })

    it('no summary yet → hidden', () => {
      useExecutionStore.setState({ executions: {} })
      useExecutionStore.getState().setHistoryLoaded(H, E, true)
      render(<ExecutionView {...base} {...headlessTab()} isActive />)
      expect(offersTerminal()).toBe(false)
    })

    it('with `from` the control is there regardless (codex, queued): the session-bound path decides', () => {
      useExecutionStore.getState().setSummary(H, E, summary({ state: 'queued', provider: 'codex' }) as never)
      render(<ExecutionView {...base} {...executionTab()} from={from} isActive />)
      expect(offersTerminal()).toBe(true)
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

  it('take to terminal whose worker could not exit leaves the persistent notice', async () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'idle', session_id: 'sid' }) as never)
    mockedToTerminal.mockResolvedValueOnce({ ...toTerminalOk, exited: false } as never)
    render(<ExecutionView {...base} {...headlessTab()} isActive />)
    await clickTakeBack()
    expect(useUndoToast.getState().notice?.message).toMatch(/could not exit/)
  })

  it('idle → no confirm; takeToTerminal (not takeBack) called with the summary cwd, the held lease id and the hook\'s forget; pane becomes the new session; success toast', async () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'idle', session_id: 'sid', cwd: '/Users/w/repo' }) as never)
    mockedToTerminal.mockResolvedValueOnce(toTerminalOk)
    const ids = headlessTab()
    render(<ExecutionView {...base} {...ids} isActive />)
    await clickTakeBack()
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
    await clickTakeBack()
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
    await clickTakeBack()
    expect(toast()?.message).toBe("The execution's working directory no longer exists on the host.")
    expect(paneContent(ids2.tabId).kind).toBe('execution')
    await waitFor(() => expect(takeBackBtn().disabled).toBe(false))
  })

  it('cc_start_timeout with session_id → manual-resume line (the daemon killed the session it created)', async () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'idle', session_id: 'sid' }) as never)
    mockedToTerminal.mockRejectedValueOnce(new HandoffApiError(504, 'cc_start_timeout', { code: 'cc_start_timeout', session_id: 'sid-4' }))
    render(<ExecutionView {...base} {...headlessTab()} isActive />)
    await clickTakeBack()
    expect(toast()?.message).toBe('Claude Code did not start in the pane in time.\nResume by hand: claude --resume sid-4')
    expect(forget).not.toHaveBeenCalled()
  })

  it('session_create_failed with session_alive → the session list is refreshed so the orphan shows up', async () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'idle', session_id: 'sid' }) as never)
    mockedToTerminal.mockRejectedValueOnce(new HandoffApiError(500, 'session_create_failed', { code: 'session_create_failed', session_name: 'repo-1', session_alive: true }))
    render(<ExecutionView {...base} {...headlessTab()} isActive />)
    await clickTakeBack()
    expect(toast()?.message).toBe('Could not create the tmux session repo-1; check the session list.')
    expect(fetchHost).toHaveBeenCalledWith(H)
  })
})

// ---- shell cleanup spec §9.5: the status bar runs the view's own take ----

describe('ExecutionView — take-to-terminal registry (shell cleanup §9.5)', () => {
  const entryOf = (paneId: string) => getTakeToTerminal(paneId)
  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    useUndoToast.setState({ toast: null })
    mockedTakeback.mockReset()
    mockedToTerminal.mockReset()
    mockedTakeBack.mockClear()
    mockedTakeToTerminal.mockClear()
    useSessionStore.setState({ sessions: {}, fetchHost: vi.fn().mockResolvedValue(undefined) } as never)
    useExecutionStore.getState().setLease(H, E, { leaseId: 'ls_1', expiresAt: Date.now() + 100_000 })
  })

  it('canTake follows whether the header offers Take to terminal', () => {
    // no `from`, no session id → not offered
    const { unmount } = render(<ExecutionView {...base} isActive />)
    expect(offersTerminal()).toBe(false)
    expect(entryOf('p1')).toMatchObject({ canTake: false, busy: false })
    unmount()
    // with `from` → offered
    const ids = executionTab()
    const view = render(<ExecutionView {...base} {...ids} from={from} isActive />)
    expect(entryOf(ids.paneId)).toMatchObject({ canTake: true, busy: false })
    view.unmount()
    // headless claude execution with a session id → offered; once archived it is not
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'idle', session_id: 'sid' }) as never)
    const headless = headlessTab()
    render(<ExecutionView {...base} {...headless} isActive />)
    expect(entryOf(headless.paneId)).toMatchObject({ canTake: true })
    act(() => { useExecutionStore.getState().setSummary(H, E, summary({ state: 'idle', session_id: 'sid', archived: true }) as never) })
    expect(entryOf(headless.paneId)).toMatchObject({ canTake: false })
  })

  it('a pane showing a problem (no header) does not offer it', () => {
    vi.mocked(sub.useExecutionSubscription).mockReturnValue({ problem: 'not_found', paused: false })
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    expect(screen.getByTestId('execution-problem')).toBeInTheDocument()
    expect(entryOf(ids.paneId)).toMatchObject({ canTake: false })
  })

  it('busy while a write is in flight, and while the take itself is in flight', async () => {
    const ids = executionTab()
    render(<ExecutionView {...base} {...ids} from={from} isActive />)
    expect(entryOf(ids.paneId)?.busy).toBe(false)
    act(() => { patchExec({ pendingSend: true, pendingLocal: { text: 'hi', delivery: null } as Exec['pendingLocal'] }) })
    expect(entryOf(ids.paneId)?.busy).toBe(true)
    // the registered handler keeps the view's guard: nothing goes out meanwhile
    act(() => { entryOf(ids.paneId)!.takeToTerminal() })
    expect(mockedTakeback).not.toHaveBeenCalled()
    act(() => { patchExec({ pendingSend: false, pendingLocal: null }) })
    expect(entryOf(ids.paneId)?.busy).toBe(false)

    const d = deferred<typeof takebackOk>()
    mockedTakeback.mockReturnValueOnce(d.promise)
    act(() => { entryOf(ids.paneId)!.takeToTerminal() })
    await waitFor(() => expect(mockedTakeback).toHaveBeenCalledTimes(1))
    expect(entryOf(ids.paneId)?.busy).toBe(true)
    // a second take meanwhile is refused by the view's single-flight guard
    act(() => { entryOf(ids.paneId)!.takeToTerminal() })
    expect(mockedTakeBack).toHaveBeenCalledTimes(1)
    await act(async () => { d.resolve(takebackOk) })
    expect(paneContent(ids.tabId).kind).toBe('tmux-session')
  })

  it('on a running turn the registered handler asks first, like the header item; Confirm runs the take', async () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'running', session_id: 'sid' }) as never)
    mockedToTerminal.mockResolvedValueOnce(toTerminalOk)
    const ids = headlessTab()
    render(<ExecutionView {...base} {...ids} isActive />)
    act(() => { entryOf(ids.paneId)!.takeToTerminal() })
    expect(screen.getByTestId('takeback-dialog')).toBeInTheDocument()
    expect(mockedTakeToTerminal).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('takeback-cancel'))
    expect(mockedTakeToTerminal).not.toHaveBeenCalled()
    act(() => { entryOf(ids.paneId)!.takeToTerminal() })
    await act(async () => { fireEvent.click(screen.getByTestId('takeback-confirm')) })
    expect(mockedTakeToTerminal).toHaveBeenCalledTimes(1)
    expect(mockedTakeToTerminal.mock.calls[0][0]).toMatchObject({ tabId: ids.tabId, paneId: ids.paneId, leaseId: 'ls_1', forgetLease: forget })
    expect(paneContent(ids.tabId).kind).toBe('tmux-session')
  })

  it('the registered handler runs the latest flow, even when the entry was not re-registered', async () => {
    mockedTakeback.mockResolvedValueOnce(takebackOk)
    const ids = executionTab()
    const { rerender } = render(<ExecutionView {...base} paneId={ids.paneId} tabId="stale-tab" from={from} isActive />)
    const entry = entryOf(ids.paneId)!
    rerender(<ExecutionView {...base} {...ids} from={from} isActive />)
    expect(entryOf(ids.paneId)).toBe(entry) // canTake / busy unchanged → same entry
    await act(async () => { entry.takeToTerminal() })
    expect(mockedTakeBack).toHaveBeenCalledWith(expect.objectContaining({ tabId: ids.tabId, paneId: ids.paneId }))
  })

  it('unregisters on unmount', () => {
    const ids = executionTab()
    const { unmount } = render(<ExecutionView {...base} {...ids} from={from} isActive />)
    expect(entryOf(ids.paneId)).not.toBeNull()
    unmount()
    expect(entryOf(ids.paneId)).toBeNull()
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

// ---- worker pane R2 T1.4 / T1.3b: the pane switches between room and chat ----

describe('ExecutionView — room and chat (R2 T1.4)', () => {
  const said = (text: string) =>
    ({ type: 'user', message: { role: 'user', content: [{ type: 'text', text }], stop_reason: null } }) as Exec['messages'][number]
  const thinkingPartial = (thinking: string): Exec['partial'] =>
    ({ messageId: 'm', finalized: 0, blocks: { 0: { index: 0, type: 'thinking', text: '', thinking, partialJson: '' } } })

  /** The pane as the wrapper drives it: the view menu's choice comes back as the `mode` prop. */
  function Switchable(props: Omit<Parameters<typeof ExecutionView>[0], 'mode' | 'onModeChange'>) {
    const [mode, setMode] = useState<'room' | 'chat'>('room')
    return <ExecutionView {...props} mode={mode} onModeChange={setMode} />
  }

  it('renders the room by default', () => {
    patchExec({ messages: [said('hello')], turnStarts: [0] })
    render(<ExecutionView {...base} isActive />)
    expect(screen.getByTestId('room-user-line')).toHaveTextContent('hello')
    expect(screen.queryByTestId('chat-bubble-user')).toBeNull()
    expect(screen.getByTestId('worker-dock')).toBeInTheDocument()
  })

  it('renders chat when the pane says chat', () => {
    patchExec({ messages: [said('hello')], turnStarts: [0] })
    render(<ExecutionView {...base} mode="chat" isActive />)
    expect(screen.getByTestId('chat-bubble-user')).toHaveTextContent('hello')
    expect(screen.queryByTestId('room-user-line')).toBeNull()
  })

  it('switching does not resubscribe', () => {
    let subscribes = 0
    function useCountingSubscription(hostId: string, executionId: string, isActive: boolean) {
      useEffect(() => { subscribes++ }, [hostId, executionId, isActive])
      return { problem: null, paused: false }
    }
    vi.mocked(sub.useExecutionSubscription).mockImplementation(useCountingSubscription)
    patchExec({ messages: [said('hello')], turnStarts: [0] })
    render(<Switchable {...base} isActive />)
    expect(subscribes).toBe(1)
    fireEvent.click(screen.getByTestId('view-mode'))
    fireEvent.click(screen.getByTestId('view-mode-chat'))
    expect(screen.getByTestId('chat-bubble-user')).toHaveTextContent('hello')
    // Back to the room through chat's overflow (chat has no inline trigger).
    fireEvent.click(screen.getByTestId('header-overflow'))
    fireEvent.click(screen.getByTestId('view-mode-room'))
    expect(screen.getByTestId('room-user-line')).toHaveTextContent('hello')
    expect(subscribes).toBe(1)
    expect(useExecutionStore.getState().executions[KEY].historyLoaded).toBe(true)
  })

  // F2: spec §3.2 — fold memory is per pane and must survive the transcript
  // remounting, which is exactly what a view switch does.
  it('F2: a block expanded in the room is still expanded after a round trip through chat', () => {
    const longBody = Array.from({ length: 100 }, (_, i) => `line ${i + 1}`).join('\n')
    patchExec({
      messages: [
        { type: 'assistant', message: { id: 'm1', role: 'assistant', content: [{ type: 'tool_use', id: 'tu1', name: 'Bash', input: { command: 'ls' } }], stop_reason: null } },
        { type: 'user', message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 'tu1', content: longBody, is_error: false }], stop_reason: null } },
      ] as Exec['messages'],
      turnStarts: [0],
    })
    render(<Switchable {...base} isActive />)
    fireEvent.click(screen.getByTestId('fold-more'))
    expect(screen.getByTestId('fold-less')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('view-mode'))
    fireEvent.click(screen.getByTestId('view-mode-chat'))
    expect(screen.queryByTestId('operation-block')).toBeNull()
    fireEvent.click(screen.getByTestId('header-overflow'))
    fireEvent.click(screen.getByTestId('view-mode-room'))
    expect(screen.getByTestId('fold-less')).toBeInTheDocument()
    expect(screen.queryByTestId('fold-more')).toBeNull()
  })

  it('chat has no dock', () => {
    render(<ExecutionView {...base} mode="chat" isActive />)
    expect(screen.queryByTestId('worker-dock')).toBeNull()
    expect(screen.getByRole('textbox')).toBeInTheDocument()
  })

  it("chat's header shows state and cost and folds the actions", () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'idle', session_id: 'sid' }) as never)
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    render(<ExecutionView {...base} {...headlessTab()} mode="chat" isActive />)
    expect(screen.getByTestId('execution-state')).toHaveTextContent('idle')
    expect(screen.getByTestId('execution-cost')).toBeInTheDocument()
    expect(screen.queryByTestId('worker-name')).toBeNull()
    expect(screen.queryByTestId('header-wide-actions')).toBeNull()
    expect(screen.queryByTestId('view-mode')).toBeNull()
    fireEvent.click(screen.getByTestId('header-overflow'))
    const panel = screen.getByTestId('header-overflow-panel')
    for (const id of ['overflow-interrupt', 'overflow-exit', 'view-mode-room', 'view-mode-chat', 'view-mode-terminal']) {
      expect(within(panel).getByTestId(id)).toBeInTheDocument()
    }
    expect(within(panel).getByTestId('view-mode-chat')).toHaveAttribute('aria-pressed', 'true')
  })

  it("chat's pending line is a dimmed user bubble carrying the queued tag", () => {
    patchExec({ pendingSend: true, pendingLocal: { text: 'second', delivery: 'queued' } as Exec['pendingLocal'] })
    render(<ExecutionView {...base} mode="chat" isActive />)
    const bubble = screen.getByTestId('chat-bubble-user')
    expect(bubble).toHaveTextContent('second')
    expect(within(bubble).getByText(/queued/i)).toBeInTheDocument()
    expect(bubble.className).toContain('opacity-60')
    expect(screen.queryByTestId('room-user-line')).toBeNull()
  })

  // F8: the pending bubble follows the durable one's rules — your text as
  // written (line breaks kept), and a slash command in mono.
  it('F8: a multi-line pending line keeps its line breaks', () => {
    patchExec({ pendingSend: true, pendingLocal: { text: 'line one\nline two', delivery: 'delivered' } as Exec['pendingLocal'] })
    render(<ExecutionView {...base} mode="chat" isActive />)
    const bubble = screen.getByTestId('chat-bubble-user')
    const p = bubble.querySelector('p')
    expect(p).not.toBeNull()
    expect(p!.className).toContain('whitespace-pre-wrap')
    expect(p!.className).toContain('break-words')
    expect(p!.textContent).toContain('line one\nline two')
    expect(bubble.className).toContain('text-sm')
  })

  it('F8: a pending slash command gets the mono face, like a durable one', () => {
    patchExec({ pendingSend: true, pendingLocal: { text: '/compact', delivery: 'queued' } as Exec['pendingLocal'] })
    render(<ExecutionView {...base} mode="chat" isActive />)
    const bubble = screen.getByTestId('chat-bubble-user')
    expect(bubble.className).toContain('font-mono')
    expect(bubble.className).toContain('opacity-60')
    expect(within(bubble).getByText(/queued/i)).toBeInTheDocument()
  })

  it('chat keeps the dots on while a thought streams', () => {
    patchExec({ turnLive: true, partial: thinkingPartial('weighing options') })
    render(<ExecutionView {...base} mode="chat" isActive />)
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
    expect(screen.queryByTestId('room-thinking')).toBeNull()
    expect(screen.queryByText('weighing options')).toBeNull()
    // Prose starts: the typewriter takes over and the dots go.
    act(() => { patchExec({ partial: textPartial('here it is') }) })
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
    expect(screen.getByTestId('chat-partial-group')).toHaveTextContent('here it is')
  })

  // F1, revisited in R2-B: chat now says "Using N tools…" while a tool runs,
  // so that line carries the activity and the dots go off, as in the room —
  // two signals for one state is what spec §4.4 R3 rules out.
  it('F1: chat turns the dots off while a tool runs; its tools line says so', () => {
    patchExec({ turnLive: true })
    render(<ExecutionView {...base} mode="chat" isActive />)
    expect(screen.getByTestId('thinking-indicator')).toBeInTheDocument()
    act(() => { useExecutionStore.getState().applyEvents(H, E, [toolUseFrame(1, 5_000)]) })
    expect(Object.values(useExecutionStore.getState().executions[KEY].tools).some((x) => x.status === 'running')).toBe(true)
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
    expect(screen.getByTestId('chat-tools-line')).toHaveTextContent('Using 1 tool…')
  })

  it('chat turns the dots off while a tool_use streams its input; the tools line counts it', () => {
    patchExec({
      turnLive: true,
      partial: { messageId: 'm', finalized: 0, blocks: { 0: { index: 0, type: 'tool_use', text: '', thinking: '', partialJson: '{"com', toolId: 's', toolName: 'Bash' } } },
    })
    render(<ExecutionView {...base} mode="chat" isActive />)
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
    expect(screen.getByTestId('chat-tools-line')).toHaveTextContent('Using 1 tool…')
  })

  it('F1: the room still turns the dots off while a tool runs', () => {
    patchExec({ turnLive: true })
    render(<ExecutionView {...base} isActive />)
    act(() => { useExecutionStore.getState().applyEvents(H, E, [toolUseFrame(1, 5_000)]) })
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
  })

  it('room still hands a streaming thought to RoomThinking', () => {
    patchExec({ turnLive: true, partial: thinkingPartial('weighing options') })
    render(<ExecutionView {...base} isActive />)
    expect(screen.queryByTestId('thinking-indicator')).not.toBeInTheDocument()
    expect(screen.getByTestId('room-thinking')).toBeInTheDocument()
  })
})

// ---- worker pane R3 T2.1: the quick-reply dock above the input -------------

describe('ExecutionView — quick replies (R3 T2.1)', () => {
  const seed = (items: { id: string; text: string }[]) => {
    const e = emptyHostConfigEntry('ready')
    useHostConfigStore.setState({ byHost: { [H]: { ...e, quickReplies: items, quickRepliesSupported: true, revisions: { ...e.revisions, quickReplies: 1 } } } })
  }
  const reply = (text: string) => screen.getAllByTestId('quick-reply').find((b) => b.textContent === text)!

  beforeEach(() => seed([{ id: 'go', text: 'go on' }, { id: 'tests', text: 'run the tests' }]))

  it('shows above the input in the room', () => {
    render(<ExecutionView {...base} isActive />)
    const first = screen.getAllByTestId('quick-reply')[0]
    expect(screen.getAllByTestId('quick-reply').map((b) => b.textContent)).toEqual(['go on', 'run the tests'])
    expect(first.compareDocumentPosition(screen.getByRole('textbox')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('shows in chat as well as room', () => {
    render(<ExecutionView {...base} mode="chat" isActive />)
    expect(screen.getAllByTestId('quick-reply')).toHaveLength(2)
    expect(screen.getAllByTestId('quick-reply')[0].compareDocumentPosition(screen.getByRole('textbox')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('an emptied list shows no dock', () => {
    seed([])
    render(<ExecutionView {...base} isActive />)
    expect(screen.queryByTestId('quick-reply')).toBeNull()
  })

  it('a tap sends the reply at once', async () => {
    render(<ExecutionView {...base} isActive />)
    fireEvent.click(reply('run the tests'))
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', 'run the tests'))
  })

  it('is disabled while a send is pending / the worker ended', async () => {
    const { rerender } = render(<ExecutionView {...base} isActive />)
    act(() => useExecutionStore.getState().setPendingSend(H, E, true))
    for (const b of screen.getAllByTestId('quick-reply')) expect(b).toBeDisabled()
    act(() => useExecutionStore.getState().setPendingSend(H, E, false))
    for (const b of screen.getAllByTestId('quick-reply')) expect(b).not.toBeDisabled()
    act(() => useExecutionStore.getState().setSummary(H, E, summary({ state: 'terminated' }) as never))
    rerender(<ExecutionView {...base} isActive />)
    for (const b of screen.getAllByTestId('quick-reply')) expect(b).toBeDisabled()
  })

  it('does not clear what is typed in the input', async () => {
    // PR #1493 R1 (P3): start from a draft an earlier failed typed send left
    // behind. A quick reply that cleared it (the default `setDraft(null)`)
    // would change WorkerInput's key and remount it over the new text; with
    // a null draft to begin with, that mistake was invisible.
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(400, 'invalid_text', 'too long'))
    render(<ExecutionView {...base} isActive />)
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'first' } })
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' })
    await waitFor(() => expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('first'))
    await waitFor(() => expect((screen.getByRole('textbox') as HTMLTextAreaElement).disabled).toBe(false))
    const box = screen.getByRole('textbox') as HTMLTextAreaElement
    fireEvent.change(box, { target: { value: 'half-typ' } })
    fireEvent.click(reply('go on'))
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', 'go on'))
    await waitFor(() => expect(useExecutionStore.getState().executions[KEY].lastTurn?.turnId).toBe('t1'))
    const after = screen.getByRole('textbox') as HTMLTextAreaElement
    expect(after).toBe(box)
    expect(after.value).toBe('half-typ')
  })

  it('a failed quick reply leaves the half-typed input alone and shows the error', async () => {
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(400, 'invalid_text', 'too long'))
    render(<ExecutionView {...base} isActive />)
    const box = screen.getByRole('textbox') as HTMLTextAreaElement
    fireEvent.change(box, { target: { value: 'half-typ' } })
    fireEvent.click(reply('go on'))
    await waitFor(() => expect(screen.getByTestId('send-error')).toBeInTheDocument())
    const after = screen.getByRole('textbox') as HTMLTextAreaElement
    // Not remounted (the same node) and not overwritten by the reply's text.
    expect(after).toBe(box)
    expect(after.value).toBe('half-typ')
    expect(after.disabled).toBe(false)
    expect(useExecutionStore.getState().executions[KEY].pendingLocal).toBeNull()
  })
})

// R3 T3.3: Mod+F opens the pane's search bar. jsdom is not a Mac, so Mod is Ctrl.
describe('ExecutionView — search (R3 T3.3)', () => {
  const said = (text: string) =>
    ({ type: 'user', message: { role: 'user', content: [{ type: 'text', text }], stop_reason: null } }) as Exec['messages'][number]
  const modF = (target: Element) => fireEvent.keyDown(target, { key: 'f', ctrlKey: true })
  const bar = () => screen.queryByTestId('transcript-search')
  /** A pointer press inside the pane: it becomes the last pane interacted with (R1-2). */
  const touch = () => fireEvent.pointerDown(screen.getAllByRole('textbox').at(-1)!)
  const openSearch = () => { touch(); return modF(document.body) }

  class FakeHighlight {
    ranges: Range[] = []
    add(range: Range) { this.ranges.push(range); return this }
  }
  const g = globalThis as unknown as { CSS?: unknown; Highlight?: unknown }
  let highlights: Map<string, FakeHighlight>
  let saved: [unknown, unknown]
  beforeEach(() => {
    saved = [g.CSS, g.Highlight]
    highlights = new Map()
    g.CSS = { highlights }
    g.Highlight = FakeHighlight
    patchExec({ messages: [said('find the needle'), said('and another needle')], turnStarts: [0] })
  })
  afterEach(() => {
    ;[g.CSS, g.Highlight] = saved
    delete (Element.prototype as { scrollTo?: unknown }).scrollTo
  })

  it('Mod+F opens the bar and focuses the input', () => {
    render(<ExecutionView {...base} isActive />)
    expect(bar()).toBeNull()
    touch()
    // Not cancelled = the browser's own find would open (the web build).
    expect(modF(document.body)).toBe(false)
    expect(bar()).toBeInTheDocument()
    expect(screen.getByTestId('transcript-search-input')).toHaveFocus()
  })

  it('shows the match count', () => {
    render(<ExecutionView {...base} isActive />)
    openSearch()
    fireEvent.change(screen.getByTestId('transcript-search-input'), { target: { value: 'needle' } })
    expect(screen.getByTestId('transcript-search-count')).toHaveTextContent('1 / 2')
    expect(highlights.get('search-current')?.ranges.map(String)).toEqual(['needle'])
  })

  // R1-2: isActive is the tab's; with a split, both worker panes listen.
  // Only the pane the reader last pressed or focused in takes a body Mod+F.
  it('in a split, Mod+F on the body opens only the pane last interacted with', () => {
    render(
      <>
        <div data-testid="pane-a"><ExecutionView {...base} paneId="pa" isActive /></div>
        <div data-testid="pane-b"><ExecutionView {...base} paneId="pb" isActive /></div>
      </>,
    )
    const [inA, inB] = screen.getAllByRole('textbox')
    fireEvent.pointerDown(inA)
    fireEvent.focusIn(inB)
    expect(modF(document.body)).toBe(false)
    expect(screen.getAllByTestId('transcript-search')).toHaveLength(1)
    expect(within(screen.getByTestId('pane-b')).getByTestId('transcript-search')).toBeInTheDocument()
  })

  it('with no pane interacted with, Mod+F on the body opens none', () => {
    render(
      <>
        <ExecutionView {...base} paneId="pa" isActive />
        <ExecutionView {...base} paneId="pb" isActive />
      </>,
    )
    expect(modF(document.body)).toBe(true)
    expect(bar()).toBeNull()
  })

  // PR #1495 re-review P2-1: the interaction record is only for telling
  // split panes apart. A lone pane takes a body Mod+F without one — an ended
  // worker's input is disabled and never takes focus, so nothing would
  // record it, and searching an old transcript is what it is for.
  it('a lone pane opens on a body Mod+F with no interaction recorded', () => {
    render(<ExecutionView {...base} isActive />)
    expect(modF(document.body)).toBe(false)
    expect(bar()).toBeInTheDocument()
  })

  it('an ended execution, alone, opens on a body Mod+F', () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'terminated' }) as never)
    render(<ExecutionView {...base} isActive />)
    expect(screen.getByRole('textbox')).toBeDisabled()
    expect(modF(document.body)).toBe(false)
    expect(bar()).toBeInTheDocument()
  })

  it('in a split, once the pane interacted with unmounts, the one left opens on a body Mod+F', () => {
    const { rerender } = render(
      <>
        <div data-testid="pane-a"><ExecutionView {...base} paneId="pa" isActive /></div>
        <div data-testid="pane-b"><ExecutionView {...base} paneId="pb" isActive /></div>
      </>,
    )
    fireEvent.pointerDown(screen.getAllByRole('textbox')[0])
    rerender(
      <>
        <div data-testid="pane-b"><ExecutionView {...base} paneId="pb" isActive /></div>
      </>,
    )
    expect(modF(document.body)).toBe(false)
    expect(within(screen.getByTestId('pane-b')).getByTestId('transcript-search')).toBeInTheDocument()
  })

  it('a target inside the pane opens it', () => {
    render(<ExecutionView {...base} isActive />)
    const box = screen.getByRole('textbox')
    expect(modF(box)).toBe(false)
    expect(bar()).toBeInTheDocument()
  })

  it('an inactive pane ignores it', () => {
    render(<ExecutionView {...base} isActive={false} />)
    touch()
    expect(modF(document.body)).toBe(true)
    expect(bar()).toBeNull()
  })

  it('a target inside another pane ignores it', () => {
    render(<ExecutionView {...base} isActive />)
    const other = document.createElement('textarea')
    document.body.appendChild(other)
    // Another pane, a dialog, Monaco: its own Mod+F, not prevented.
    expect(modF(other)).toBe(true)
    expect(bar()).toBeNull()
    other.remove()
  })

  it('the other modifier, or Mod+Shift+F, does not open it', () => {
    render(<ExecutionView {...base} isActive />)
    fireEvent.keyDown(document.body, { key: 'f', metaKey: true })
    fireEvent.keyDown(document.body, { key: 'f', ctrlKey: true, shiftKey: true })
    fireEvent.keyDown(document.body, { key: 'g', ctrlKey: true })
    expect(bar()).toBeNull()
  })

  it('escape closes and clears; focus returns to where it was', () => {
    render(<ExecutionView {...base} isActive />)
    const box = screen.getByRole('textbox')
    box.focus()
    modF(box)
    const input = screen.getByTestId('transcript-search-input')
    expect(input).toHaveFocus()
    fireEvent.change(input, { target: { value: 'needle' } })
    expect(highlights.has('search-current')).toBe(true)
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(bar()).toBeNull()
    expect(highlights.has('search-current')).toBe(false)
    expect(highlights.has('search-match')).toBe(false)
    expect(box).toHaveFocus()
  })

  it('unmounting the pane clears its marks', () => {
    const { unmount } = render(<ExecutionView {...base} isActive />)
    openSearch()
    fireEvent.change(screen.getByTestId('transcript-search-input'), { target: { value: 'needle' } })
    expect(highlights.has('search-current')).toBe(true)
    unmount()
    expect(highlights.has('search-current')).toBe(false)
  })

  it('opens in chat too', () => {
    render(<ExecutionView {...base} mode="chat" isActive />)
    openSearch()
    fireEvent.change(screen.getByTestId('transcript-search-input'), { target: { value: 'needle' } })
    expect(screen.getByTestId('transcript-search-count')).toHaveTextContent('1 / 2')
  })

  // R1-3 / A F6: the take-back confirm renders inside the pane's root.
  it('Mod+F inside a dialog is the dialog\'s', () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'running' }) as never)
    render(<ExecutionView {...base} from={from} isActive />)
    fireEvent.click(takeBackBtn())
    const cancel = screen.getByTestId('takeback-cancel')
    cancel.focus()
    expect(modF(cancel)).toBe(true)
    expect(bar()).toBeNull()
  })

  it('one Escape closes the dialog, not the bar too', () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'running' }) as never)
    render(<ExecutionView {...base} from={from} isActive />)
    openSearch()
    expect(bar()).toBeInTheDocument()
    fireEvent.click(takeBackBtn())
    expect(screen.getByTestId('takeback-dialog')).toBeInTheDocument()
    fireEvent.keyDown(screen.getByTestId('transcript-search-input'), { key: 'Escape' })
    expect(screen.queryByTestId('takeback-dialog')).toBeNull()
    expect(bar()).toBeInTheDocument()
  })

  // R1-1 / A F2: room ⇄ chat with the bar open.
  it('switching view with the bar open keeps the marks and the place', () => {
    const scrollTo = vi.fn()
    Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
    const intoView = vi.fn()
    Element.prototype.scrollIntoView = intoView
    try {
      const { rerender } = render(<ExecutionView {...base} isActive />)
      openSearch()
      const input = screen.getByTestId('transcript-search-input')
      fireEvent.change(input, { target: { value: 'needle' } })
      fireEvent.keyDown(input, { key: 'Enter' })
      expect(screen.getByTestId('transcript-search-count')).toHaveTextContent('2 / 2')
      const jumps = intoView.mock.calls.length
      scrollTo.mockClear()
      rerender(<ExecutionView {...base} mode="chat" isActive />)
      const cur = highlights.get('search-current')?.ranges ?? []
      expect(cur.map(String)).toEqual(['needle'])
      expect(cur[0].collapsed).toBe(false)
      expect(cur[0].startContainer.isConnected).toBe(true)
      expect(screen.getByTestId('transcript-search-count')).toHaveTextContent('2 / 2')
      expect(intoView.mock.calls.length).toBe(jumps + 1)
      expect(scrollTo).not.toHaveBeenCalled()
    } finally {
      delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView
    }
  })

  it('switching view with the bar open and no match lands at the bottom', () => {
    const scrollTo = vi.fn()
    Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
    const { rerender } = render(<ExecutionView {...base} isActive />)
    openSearch()
    // jsdom has no layout: every box is 1000 high, and scrollTop writes are recorded.
    const proto = Element.prototype
    const saved = [Object.getOwnPropertyDescriptor(proto, 'scrollTop'), Object.getOwnPropertyDescriptor(proto, 'scrollHeight')]
    const setTop = vi.fn()
    Object.defineProperty(proto, 'scrollTop', { configurable: true, get: () => 0, set: setTop })
    Object.defineProperty(proto, 'scrollHeight', { configurable: true, get: () => 1000 })
    try {
      scrollTo.mockClear()
      rerender(<ExecutionView {...base} mode="chat" isActive />)
      // The transcript's own first jump is held; the bar puts the reader at the bottom.
      expect(scrollTo).not.toHaveBeenCalled()
      expect(setTop).toHaveBeenCalledWith(1000)
      expect(bar()).toBeInTheDocument()
    } finally {
      for (const [name, d] of [['scrollTop', saved[0]], ['scrollHeight', saved[1]]] as const) {
        if (d) Object.defineProperty(proto, name, d)
        else delete (proto as unknown as Record<string, unknown>)[name]
      }
    }
  })

  // A F4: the pane wires the bar's jumps to the transcript's release().
  it('a jump into the last screen stops the bottom-follow', () => {
    const scrollTo = vi.fn()
    Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
    render(<ExecutionView {...base} isActive />)
    const scroller = document.querySelector('.overflow-y-auto') as HTMLElement
    const geometry = (scrollHeight: number, scrollTop: number) => {
      Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: scrollHeight })
      Object.defineProperty(scroller, 'clientHeight', { configurable: true, value: 200 })
      Object.defineProperty(scroller, 'scrollTop', { configurable: true, writable: true, value: scrollTop })
    }
    geometry(1000, 800)
    fireEvent.scroll(scroller)
    const intoView = vi.fn(() => { scroller.scrollTop = 790 })
    Element.prototype.scrollIntoView = intoView
    try {
      openSearch()
      fireEvent.change(screen.getByTestId('transcript-search-input'), { target: { value: 'needle' } })
      expect(intoView).toHaveBeenCalled()
      fireEvent.scroll(scroller)
      scrollTo.mockClear()
      geometry(1100, 790)
      act(() => {
        const s = useExecutionStore.getState().executions[KEY]
        patchExec({ messages: [...s.messages, said('a new line')] })
      })
      expect(scrollTo).not.toHaveBeenCalled()
    } finally {
      delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView
    }
  })

  it('holds the bottom-follow only while the bar is open', () => {
    const scrollTo = vi.fn()
    Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
    render(<ExecutionView {...base} isActive />)
    const scroller = document.querySelector('.overflow-y-auto') as HTMLElement
    Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: 1000 })
    Object.defineProperty(scroller, 'clientHeight', { configurable: true, value: 200 })
    Object.defineProperty(scroller, 'scrollTop', { configurable: true, writable: true, value: 800 })
    fireEvent.scroll(scroller)
    // The reader scrolls up to read.
    scroller.scrollTop = 100
    fireEvent.scroll(scroller)
    const land = (text: string) => act(() => {
      const s = useExecutionStore.getState().executions[KEY]
      patchExec({ messages: [...s.messages, said(text)] })
    })
    openSearch()
    scrollTo.mockClear()
    land('while open')
    expect(scrollTo).not.toHaveBeenCalled()
    fireEvent.keyDown(screen.getByTestId('transcript-search-input'), { key: 'Escape' })
    land('after closing')
    expect(scrollTo).toHaveBeenCalledTimes(1)
  })
})

// Fix round 1, finding 1: the pane's scroll memo is keyed by pane AND
// execution, not just the pane — a handoff / take-back swaps a pane's
// content to a different execution while keeping its paneId (lib/nex/handoff
// swaps `PaneContent`, but `paneId` never changes).
describe('ExecutionView — scroll memory is keyed per execution (spec §6 fix)', () => {
  afterEach(() => {
    delete (Element.prototype as { scrollTo?: unknown }).scrollTo
  })

  it('a different execution swapped into the same pane does not inherit the old scroll position', () => {
    const scrollTo = vi.fn()
    Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
    const E2 = 'exc_2'
    useExecutionStore.getState().setSummary(H, E2, summary({ id: E2 }) as never)
    useExecutionStore.getState().setHistoryLoaded(H, E2, true)

    const { unmount } = render(<ExecutionView {...base} paneId="p1" isActive />)
    const scroller = document.querySelector('.overflow-y-auto') as HTMLElement
    Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: 1000 })
    Object.defineProperty(scroller, 'clientHeight', { configurable: true, value: 200 })
    Object.defineProperty(scroller, 'scrollTop', { configurable: true, writable: true, value: 300 })
    fireEvent.scroll(scroller)
    unmount()

    // A different execution takes the same pane (as a handoff / take-back
    // would): its first `follow()` must jump to the bottom, not restore the
    // previous execution's mid-transcript position.
    scrollTo.mockClear()
    render(<ExecutionView {...base} paneId="p1" executionId={E2} isActive />)
    expect(scrollTo).toHaveBeenCalledTimes(1)
    expect(scrollTo).not.toHaveBeenCalledWith(expect.objectContaining({ top: 300 }))
  })
})

// R4 T3.2: the dock lists running tasks; "inspect" goes where search would.
describe('ExecutionView — dock tasks (R4 T3.2)', () => {
  type Msg = Exec['messages'][number]
  const said = (text: string) => ({ type: 'user', message: { role: 'user', content: [{ type: 'text', text }], stop_reason: null } }) as Msg
  const call = (id: string, name: string, input: Record<string, unknown>, parent: string | null = null) =>
    ({ type: 'assistant', parent_tool_use_id: parent, message: { id: `m_${id}`, role: 'assistant', content: [{ type: 'tool_use', id, name, input }], stop_reason: null } }) as Msg
  const running = (id: string, toolUseId: string | null, extra: Record<string, unknown> = {}) => ({
    task_id: id, turn_id: 't1', kind: 'shell' as const, task_type: 'local_bash', tool_use_id: toolUseId, parent_tool_use_id: null,
    description: '', backgrounded: true, status: 'running' as const, provider_status: null, closed_by: null,
    started_at: Date.now() - 120_000, ended_at: null, startSeq: 1, ...extra,
  })
  afterEach(() => {
    delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView
    delete (Element.prototype as { scrollTo?: unknown }).scrollTo
  })

  it('shows running tasks in the dock, oldest first; closed ones are not listed', () => {
    patchExec({ tasks: {
      b: running('b', 'tu_b', { command: 'tail -f log', started_at: Date.now() - 60_000 }),
      a: running('a', 'tu_a', { command: 'pnpm dev' }),
      c: { ...running('c', 'tu_c', { command: 'done' }), status: 'completed' },
    } })
    render(<ExecutionView {...base} isActive />)
    expect(screen.getByTestId('worker-dock-tasks')).toHaveTextContent('2 running')
    expect(screen.getAllByTestId('worker-dock-task').map((el) => el.textContent)).toEqual(['pnpm dev (2m)', 'tail -f log (1m)'])
  })

  it('inspect opens the subagent that hides the call and scrolls to it, once', () => {
    const intoView = vi.fn()
    Element.prototype.scrollIntoView = intoView
    const scrollTo = vi.fn()
    Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
    patchExec({
      messages: [said('go'), call('task', 'Task', { description: 'explore' }), call('c1', 'Bash', { command: 'tail -f log' }, 'task')],
      turnStarts: [0],
      tasks: { a: running('a', 'c1', { command: 'tail -f log' }) },
    })
    render(<ExecutionView {...base} isActive />)
    expect(document.querySelector('[data-search-unit="2:0:arg"]')).toBeNull()
    fireEvent.click(screen.getByTestId('worker-dock-toggle'))
    fireEvent.click(screen.getByTestId('worker-dock-inspect'))
    const target = document.querySelector('[data-search-unit="2:0:arg"]')
    expect(target).not.toBeNull()
    expect(intoView).toHaveBeenCalledTimes(1)
    expect(intoView.mock.contexts[0]).toBe(target)
    // A re-render (new content) does not replay the jump.
    act(() => { const s = useExecutionStore.getState().executions[KEY]; patchExec({ messages: [...s.messages, said('later')] }) })
    expect(intoView).toHaveBeenCalledTimes(1)
  })

  it('inspect with the search bar closed survives the next streamed line', () => {
    const scrollTo = vi.fn()
    Element.prototype.scrollTo = scrollTo as unknown as Element['scrollTo']
    patchExec({
      messages: [said('go'), call('c1', 'Bash', { command: 'tail -f log' })],
      turnStarts: [0],
      tasks: { a: running('a', 'c1', { command: 'tail -f log' }) },
    })
    render(<ExecutionView {...base} isActive />)
    const scroller = document.querySelector('.overflow-y-auto') as HTMLElement
    const geometry = (scrollHeight: number, scrollTop: number) => {
      Object.defineProperty(scroller, 'scrollHeight', { configurable: true, value: scrollHeight })
      Object.defineProperty(scroller, 'clientHeight', { configurable: true, value: 200 })
      Object.defineProperty(scroller, 'scrollTop', { configurable: true, writable: true, value: scrollTop })
    }
    geometry(1000, 800)
    fireEvent.scroll(scroller)
    const intoView = vi.fn(() => { scroller.scrollTop = 300 })
    Element.prototype.scrollIntoView = intoView
    fireEvent.click(screen.getByTestId('worker-dock-toggle'))
    fireEvent.click(screen.getByTestId('worker-dock-inspect'))
    expect(intoView).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('transcript-search-input')).toBeNull()
    fireEvent.scroll(scroller)
    scrollTo.mockClear()
    geometry(1100, 300)
    act(() => { const s = useExecutionStore.getState().executions[KEY]; patchExec({ messages: [...s.messages, said('a new line')] }) })
    expect(scrollTo).not.toHaveBeenCalled()
  })

  it('inspect for a call not in the transcript does nothing', () => {
    const intoView = vi.fn()
    Element.prototype.scrollIntoView = intoView
    patchExec({ messages: [said('go')], turnStarts: [0], tasks: { a: running('a', 'missing', { command: 'x' }) } })
    render(<ExecutionView {...base} isActive />)
    fireEvent.click(screen.getByTestId('worker-dock-toggle'))
    fireEvent.click(screen.getByTestId('worker-dock-inspect'))
    expect(intoView).not.toHaveBeenCalled()
  })
})

// Worker pane theme spec §9.1: files attached by path. The chips live here,
// not in WorkerInput (re-keyed on the draft), and the whole pane is the drop target.
describe('ExecutionView — attachments', () => {
  const txt = (name: string) => new File(['x'], name, { type: 'text/plain' })
  const saved = (name: string) => `/Users/w/repo/.purdex-uploads/${E}/${name}`
  const dropFiles = (files: File[]) => {
    const root = screen.getByTestId('execution-view')
    fireEvent.dragEnter(root, { dataTransfer: { types: ['Files'], files } })
    expect(screen.getByTestId('drop-overlay')).toHaveTextContent('Drop files to upload')
    fireEvent.drop(root, { dataTransfer: { types: ['Files'], files } })
  }
  beforeEach(() => {
    vi.mocked(api.uploadWorkerFile).mockReset().mockImplementation(async (_h, _e, f) => ({ path: saved(f.name), name: f.name, size: 1 }))
  })

  it('a drop on the pane root (not the input) shows the overlay and uploads each file in order', async () => {
    render(<ExecutionView {...base} isActive />)
    dropFiles([txt('a.txt'), txt('b.txt')])
    expect(screen.queryByTestId('drop-overlay')).toBeNull()
    await waitFor(() => expect(screen.getAllByTestId('upload-chip').map((c) => c.dataset.status)).toEqual(['done', 'done']))
    expect(vi.mocked(api.uploadWorkerFile).mock.calls.map((c) => [c[0], c[1], c[2].name])).toEqual([[H, E, 'a.txt'], [H, E, 'b.txt']])
  })

  it('a drag without files shows no overlay', () => {
    render(<ExecutionView {...base} isActive />)
    fireEvent.dragEnter(screen.getByTestId('execution-view'), { dataTransfer: { types: ['text/plain'], files: [] } })
    expect(screen.queryByTestId('drop-overlay')).toBeNull()
  })

  it('a successful send composes the text exactly and clears the chips', async () => {
    render(<ExecutionView {...base} isActive />)
    dropFiles([txt('a.txt'), txt('b.txt')])
    await waitFor(() => expect(screen.getAllByTestId('upload-chip').every((c) => c.dataset.status === 'done')).toBe(true))
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'read these' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1',
      `read these\n\n[file: ${saved('a.txt')}]\n[file: ${saved('b.txt')}]`))
    await waitFor(() => expect(screen.queryAllByTestId('upload-chip')).toHaveLength(0))
  })

  it('a failed send keeps the chips and restores only the typed text', async () => {
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(400, 'invalid_text', 'too long'))
    render(<ExecutionView {...base} isActive />)
    dropFiles([txt('a.txt')])
    await waitFor(() => expect(screen.getByTestId('upload-chip').dataset.status).toBe('done'))
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'typed' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await waitFor(() => expect(screen.getByTestId('send-error')).toBeInTheDocument())
    expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toBe('typed')
    expect(screen.getAllByTestId('upload-chip')).toHaveLength(1)
  })

  it('send is blocked while a file is still uploading', async () => {
    vi.mocked(api.uploadWorkerFile).mockReset().mockReturnValue(new Promise(() => {}))
    render(<ExecutionView {...base} isActive />)
    dropFiles([txt('a.txt')])
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'hi' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await act(async () => {})
    expect(api.sendMessage).not.toHaveBeenCalled()
    expect(screen.getByTestId('upload-block')).toHaveTextContent('Waiting for uploads to finish…')
  })

  it('an ended execution takes no drop', () => {
    useExecutionStore.getState().setSummary(H, E, summary({ state: 'terminated' }) as never)
    render(<ExecutionView {...base} isActive />)
    fireEvent.dragEnter(screen.getByTestId('execution-view'), { dataTransfer: { types: ['Files'], files: [txt('a.txt')] } })
    expect(screen.queryByTestId('drop-overlay')).toBeNull()
  })

  // PR #1522 A1: a quick reply goes through the same attachment-aware send as
  // a typed message — blocked by an uploading / failed chip, and carrying the
  // done chips (then clearing them) otherwise.
  describe('quick replies share the attachment gate', () => {
    beforeEach(() => {
      const e = emptyHostConfigEntry('ready')
      useHostConfigStore.setState({ byHost: { [H]: { ...e, quickReplies: [{ id: 'go', text: 'go on' }], quickRepliesSupported: true, revisions: { ...e.revisions, quickReplies: 1 } } } })
    })

    it('an uploading chip blocks a quick reply (disabled, reason shown, nothing sent)', async () => {
      vi.mocked(api.uploadWorkerFile).mockReset().mockReturnValue(new Promise(() => {}))
      render(<ExecutionView {...base} isActive />)
      dropFiles([txt('a.txt')])
      const btn = screen.getByTestId('quick-reply')
      expect(btn).toBeDisabled()
      fireEvent.click(btn)
      await act(async () => {})
      expect(api.sendMessage).not.toHaveBeenCalled()
      expect(screen.getByTestId('upload-block')).toHaveTextContent('Waiting for uploads to finish…')
    })

    it('a failed chip blocks a quick reply', async () => {
      vi.mocked(api.uploadWorkerFile).mockReset().mockRejectedValue(new NexApiError(413, 'file_too_large', 'too big'))
      render(<ExecutionView {...base} isActive />)
      dropFiles([txt('a.txt')])
      await waitFor(() => expect(screen.getByTestId('upload-chip').dataset.status).toBe('failed'))
      const btn = screen.getByTestId('quick-reply')
      expect(btn).toBeDisabled()
      fireEvent.click(btn)
      await act(async () => {})
      expect(api.sendMessage).not.toHaveBeenCalled()
      expect(screen.getByTestId('upload-block')).toHaveTextContent('An upload failed — remove it to send')
    })

    it('with done chips, a quick reply carries their [file:] lines and clears them', async () => {
      render(<ExecutionView {...base} isActive />)
      dropFiles([txt('a.txt')])
      await waitFor(() => expect(screen.getByTestId('upload-chip').dataset.status).toBe('done'))
      fireEvent.click(screen.getByTestId('quick-reply'))
      await waitFor(() => expect(api.sendMessage).toHaveBeenCalledWith(H, E, 'ls_1', `go on\n\n[file: ${saved('a.txt')}]`))
      await waitFor(() => expect(screen.queryAllByTestId('upload-chip')).toHaveLength(0))
    })
  })

  // Matches the disabled `+` button: while a send is in flight the input is
  // disabled, so a drop must not open the overlay or upload either.
  it('takes no drop while the input is disabled (a send in flight)', () => {
    act(() => useExecutionStore.getState().setPendingSend(H, E, true))
    render(<ExecutionView {...base} isActive />)
    const root = screen.getByTestId('execution-view')
    fireEvent.dragEnter(root, { dataTransfer: { types: ['Files'], files: [txt('a.txt')] } })
    expect(screen.queryByTestId('drop-overlay')).toBeNull()
    fireEvent.drop(root, { dataTransfer: { types: ['Files'], files: [txt('a.txt')] } })
    expect(screen.queryAllByTestId('upload-chip')).toHaveLength(0)
    expect(api.uploadWorkerFile).not.toHaveBeenCalled()
  })
})

describe('ExecutionView — worker prelude', () => {
  const said = (text: string) =>
    ({ type: 'user', message: { role: 'user', content: [{ type: 'text', text }], stop_reason: null } }) as Exec['messages'][number]
  const CAP = { route: { method: 'GET', path: '/x' }, page_max_items: 500, page_max_bytes: 1, max_block_bytes: 1 }
  const seed = (resume: boolean) => {
    useNexHostStore.setState({ byHost: { [H]: { phase: 'ready', capabilities: { transcript_prelude: CAP } } } } as never)
    useExecutionStore.getState().setSummary(H, E, summary(resume ? { resume_session_id: 'sid' } : {}) as never)
    patchExec({ messages: [said('the brief')], turnStarts: [0] })
    vi.mocked(api.fetchExecutionPrelude).mockResolvedValue({
      state: 'ok', prevCursor: null, totalBytes: null,
      items: [{ pos: '2', at: 1, kind: 'user', msg: said('earlier') }],
    } as never)
  }
  let offsetHeightDesc: PropertyDescriptor | undefined
  beforeEach(() => {
    offsetHeightDesc = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
  })
  afterEach(() => {
    if (offsetHeightDesc) Object.defineProperty(HTMLElement.prototype, 'offsetHeight', offsetHeightDesc)
    delete (Element.prototype as { scrollTo?: unknown }).scrollTo
    useNexHostStore.setState({ byHost: {} })
    vi.mocked(api.fetchExecutionPrelude).mockReset()
  })

  it('draws the prelude above the brief line (turn 1)', async () => {
    seed(true)
    render(<ExecutionView {...base} isActive />)
    const earlier = await screen.findByText('earlier')
    const brief = screen.getByText('the brief')
    expect(earlier.compareDocumentPosition(brief) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('a worker without a resume session id gets no prelude markup and no request (D4)', () => {
    seed(false)
    render(<ExecutionView {...base} isActive />)
    expect(screen.queryByTestId('prelude-anchor')).toBeNull()
    expect(api.fetchExecutionPrelude).not.toHaveBeenCalled()
  })

  it('the composed preludeVersion drives the scroll correction when the first page lands', async () => {
    seed(true)
    Element.prototype.scrollTo = vi.fn() as unknown as Element['scrollTo']
    // jsdom has no layout: the anchor is as tall as its text.
    Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
      configurable: true,
      get(this: HTMLElement) { return this.dataset?.testid === 'prelude-anchor' ? (this.textContent ?? '').length : 0 },
    })
    render(<ExecutionView {...base} isActive />)
    const box = screen.getByTestId('prelude-anchor').parentElement as HTMLElement
    Object.defineProperty(box, 'scrollHeight', { configurable: true, value: 100000 })
    Object.defineProperty(box, 'clientHeight', { configurable: true, value: 400 })
    Object.defineProperty(box, 'scrollTop', { configurable: true, writable: true, value: 300 })
    fireEvent.scroll(box)
    await screen.findByText('earlier')
    expect(box.scrollTop).not.toBe(300)
  })
})

// Shell cleanup spec §8.2 (P5, T5.4): ExecutionView hands `isActive` and
// `isFocusTarget` to the reply box, which focuses itself only at activation
// and after a send comes back, and only as its tab's focus target.
describe('ExecutionView — reply box focus (shell cleanup §8.2)', () => {
  const nextFrame = () => act(() => new Promise<void>((r) => requestAnimationFrame(() => r())))
  const setSending = (v: boolean) => act(() => { useExecutionStore.getState().setPendingSend(H, E, v) })

  it('mounting active as the focus target focuses the reply box', async () => {
    render(<ExecutionView {...base} isActive isFocusTarget />)
    await nextFrame()
    expect(screen.getByRole('textbox')).toHaveFocus()
  })

  it('mounting active but not the focus target leaves focus alone', async () => {
    render(<ExecutionView {...base} isActive isFocusTarget={false} />)
    await nextFrame()
    expect(screen.getByRole('textbox')).not.toHaveFocus()
  })

  it('a send coming back refocuses the reply box of the focus target', async () => {
    render(<ExecutionView {...base} isActive isFocusTarget />)
    // Let the mount's activation frame pass, so what follows tests the send coming back alone.
    await nextFrame()
    // The reader's focus leaves the box (blurred before it is disabled: jsdom
    // keeps a disabled element focused internally).
    screen.getByRole('textbox').blur()
    setSending(true)
    expect(screen.getByRole('textbox')).toBeDisabled()
    setSending(false)
    expect(screen.getByRole('textbox')).not.toHaveFocus()
    await nextFrame()
    expect(screen.getByRole('textbox')).toHaveFocus()
  })

  it('a send that comes back after the reader moved to another pane does not take focus', async () => {
    const { rerender } = render(<ExecutionView {...base} isActive isFocusTarget />)
    await nextFrame()
    screen.getByRole('textbox').blur()
    setSending(true)
    rerender(<ExecutionView {...base} isActive isFocusTarget={false} />)
    setSending(false)
    await nextFrame()
    expect(screen.getByRole('textbox')).not.toHaveFocus()
  })

  // P5 review A2: the input's `disabled` also covers stream loss; the box
  // enabling again later in the session is not a send coming back.
  it('the live stream lost and back (input enabled again) does not take focus', async () => {
    render(<ExecutionView {...base} isActive isFocusTarget />)
    await nextFrame() // the activation lands
    screen.getByRole('textbox').blur()
    act(() => { useExecutionStore.getState().setSse(H, E, 'closed', 'forbidden') })
    expect(screen.getByRole('textbox')).toBeDisabled()
    await nextFrame()
    act(() => { useExecutionStore.getState().setSse(H, E, 'open', null) })
    expect(screen.getByRole('textbox')).not.toBeDisabled()
    await nextFrame()
    expect(screen.getByRole('textbox')).not.toHaveFocus()
  })

  // P5 review follow-up: opening a worker whose history is still loading —
  // the activation finds the box disabled, and keeps its focus pending until
  // the box is usable.
  it('opened while its history loads: the reply box takes focus once the history has loaded', async () => {
    useExecutionStore.getState().setHistoryLoaded(H, E, false)
    render(<ExecutionView {...base} isActive isFocusTarget />)
    expect(screen.getByRole('textbox')).toBeDisabled()
    await nextFrame()
    expect(screen.getByRole('textbox')).not.toHaveFocus()
    act(() => { useExecutionStore.getState().setHistoryLoaded(H, E, true) })
    expect(screen.getByRole('textbox')).not.toBeDisabled()
    await nextFrame()
    expect(screen.getByRole('textbox')).toHaveFocus()
  })

  it('opened while its history loads, the reader moved to another pane meanwhile: no focus when it loads', async () => {
    useExecutionStore.getState().setHistoryLoaded(H, E, false)
    const { rerender } = render(<ExecutionView {...base} isActive isFocusTarget />)
    await nextFrame()
    rerender(<ExecutionView {...base} isActive isFocusTarget={false} />)
    act(() => { useExecutionStore.getState().setHistoryLoaded(H, E, true) })
    await nextFrame()
    expect(screen.getByRole('textbox')).not.toHaveFocus()
  })

  it('a send that comes back as the worker ends (input stays disabled) does not take focus', async () => {
    render(<ExecutionView {...base} isActive isFocusTarget />)
    await nextFrame()
    screen.getByRole('textbox').blur()
    setSending(true)
    act(() => {
      useExecutionStore.getState().setSummary(H, E, summary({ state: 'terminated' }) as never)
      useExecutionStore.getState().setPendingSend(H, E, false)
    })
    expect(screen.getByRole('textbox')).toBeDisabled()
    const focus = vi.spyOn(HTMLTextAreaElement.prototype, 'focus')
    try {
      await nextFrame()
      expect(focus).not.toHaveBeenCalled()
    } finally {
      focus.mockRestore()
    }
  })
})
