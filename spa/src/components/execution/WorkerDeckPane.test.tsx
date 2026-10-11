// U4-1b: a worker pane in its 指揮台 view — the deck on the execution's own Claude Code conversation (bound to the row's session id), the lifecycle
// before that id exists, no way to a terminal, the worker's own input and send path in the footer, model and cost in the status row, and the way
// back to the room.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { UNDO_MS, clearAllSendQueues } from '../../lib/conversations/send-queue'
import { clearAllAdapters } from '../../lib/conversations/deck-adapter'
import { clearAllWorkerLeases } from '../../lib/conversations/worker-adapter'
import { clearAllUploads } from '../../lib/conversations/attachment-upload'
import { clearAllAttachments } from '../../lib/conversations/attachment-memory'
import { clearAllDrafts } from '../../lib/conversations/draft-memory'
import { clearAllWorkerDrafts, readWorkerDraft } from '../../lib/nex/worker-draft-memory'
import { clearAllPermissionCards } from '../../lib/nex/permission-card-memory'
import WorkerDeckPane from './WorkerDeckPane'
import { emptyDoc } from '../../lib/conversations/model'
import { clearAllPanels } from '../../lib/conversations/panel-memory'
import type { ConversationItem, Turn } from '../../lib/conversations/types'
import { useConversationStore, type ConversationEntry } from '../../stores/useConversationStore'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import * as api from '../../lib/nex/nex-api'
import * as lease from '../../hooks/useExecutionLease'
import { NexApiError } from '../../lib/nex/types'
import { takeBack, takeToTerminal } from '../../lib/nex/handoff'
import { exitWorker } from '../../lib/nex/exit-worker'
import { getTakeToTerminal } from '../../lib/nex/take-to-terminal-registry'
import { getNexClientId } from '../../lib/nex/client-id'
import { closeTab } from '../../lib/tab-lifecycle'

vi.mock('../../lib/nex/nex-api', () => ({ sendMessage: vi.fn(), interruptExecution: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), uploadWorkerFile: vi.fn(), fetchExecutionPrelude: vi.fn(), listExecutions: vi.fn(), answerPermission: vi.fn() }))
vi.mock('../../hooks/useExecutionSubscription', () => ({ useExecutionSubscription: vi.fn(() => ({ problem: null, paused: false })) }))
vi.mock('../../hooks/useExecutionLease', () => ({ useExecutionLease: vi.fn() }))

const H = 'h', E = 'exc_1', SID = '7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b'
const summary = (over: Record<string, unknown> = {}) => ({
  id: E, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w/purdex', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 1, updated_at: 1,
  duration_ms: null, event_count: 0, observers: 1, archived: false, effective_profile: 'standard', turn_count: 1, session_id: SID, cost_usd: 1.5, ...over,
})
const item = { type: 'user', id: 'u1', at: 1, index: 0, text: 'hello worker', source: 'user' } as ConversationItem
const turn: Turn = { id: 't0', index: 0, started_at: 1, outcome: 'done', items: [item] }
const entry = (): ConversationEntry => ({ doc: { ...emptyDoc(), turns: [turn], header: { status: 'running', title: '', backend: 'execution', usage: { model: 'claude-haiku-5-5', effort: 'medium' } } as never }, status: 'live', reason: '', paging: false, subagents: {} })

vi.mock('../../lib/nex/handoff', async (orig) => ({ ...(await orig<typeof import('../../lib/nex/handoff')>()), takeToTerminal: vi.fn(async () => ({ result: {}, swapped: true })), takeBack: vi.fn(async () => ({ result: {}, swapped: true })) }))
vi.mock('../../lib/nex/exit-worker', async (orig) => ({ ...(await orig<typeof import('../../lib/nex/exit-worker')>()), exitWorker: vi.fn() }))
vi.mock('../../lib/tab-lifecycle', async (orig) => ({ ...(await orig<typeof import('../../lib/tab-lifecycle')>()), closeTab: vi.fn() }))

const acquire = vi.fn(() => vi.fn())
const ui = (over: Partial<Parameters<typeof WorkerDeckPane>[0]> = {}) => (
  <WorkerDeckPane hostId={H} executionId={E} isActive isFocusTarget tabId="t" paneId="p-w" onModeChange={() => {}} {...over} />
)
function seed(sum: Record<string, unknown>, loaded = true) {
  useExecutionStore.getState().setSummary(H, E, sum as never)
  useExecutionStore.getState().setHistoryLoaded(H, E, loaded)
}
const realEnsure = useNexHostStore.getState().ensure

beforeEach(() => {
  cleanup(); clearAllPanels(); clearAllSendQueues(); clearAllAdapters(); clearAllWorkerLeases(); clearAllUploads(); clearAllAttachments(); clearAllDrafts(); clearAllWorkerDrafts(); clearAllPermissionCards()
  useExecutionStore.setState({ executions: {} })
  useNexHostStore.setState({ ensure: async () => {}, byHost: { [H]: { phase: 'ready', capabilities: {} } } } as never)
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  vi.mocked(lease.useExecutionLease).mockReturnValue({ ensureLease: vi.fn(async () => 'lease-1'), release: vi.fn(), forget: vi.fn(), touch: vi.fn() } as never)
  vi.mocked(api.sendMessage).mockReset().mockResolvedValue({} as never)
  vi.mocked(api.answerPermission).mockReset()
  vi.mocked(takeToTerminal).mockClear(); vi.mocked(takeBack).mockClear(); vi.mocked(exitWorker).mockClear(); vi.mocked(closeTab).mockClear()
  vi.mocked(api.interruptExecution).mockReset().mockResolvedValue({} as never)
  vi.mocked(api.uploadWorkerFile).mockReset().mockImplementation(async (_h, _e, f) => ({ path: `/w/purdex/${f.name}`, name: f.name, size: 1 }) as never)
  acquire.mockClear()
  useConversationStore.setState({ byKey: { [`${H}\0${SID}`]: entry() }, acquire } as never)
})
afterEach(() => { cleanup(); vi.useRealTimers(); useNexHostStore.setState({ byHost: {}, ensure: realEnsure }) })

describe('WorkerDeckPane', () => {
  it('draws the deck on the conversation of the row\'s own session id, and holds that conversation', () => {
    seed(summary())
    render(ui())
    expect(acquire).toHaveBeenCalledWith(H, SID)
    expect(screen.getByTestId('deck-scroll')).toBeInTheDocument()
    expect(screen.getByText('hello worker')).toBeInTheDocument()
  })

  it('a row with no session id of its own reads the session it resumes', () => {
    seed(summary({ session_id: undefined, resume_session_id: SID }))
    render(ui())
    expect(acquire).toHaveBeenCalledWith(H, SID)
    expect(screen.getByTestId('deck-scroll')).toBeInTheDocument()
  })

  it('before the session id exists: the lifecycle, no deck; once it arrives the deck takes over', () => {
    seed(summary({ session_id: undefined, state: 'queued' }))
    render(ui())
    expect(screen.getByTestId('worker-deck-placeholder')).toHaveTextContent('Queued')
    expect(screen.queryByTestId('deck-scroll')).toBeNull()
    expect(acquire).not.toHaveBeenCalled()
    act(() => { useExecutionStore.getState().setSummary(H, E, summary() as never) })
    expect(screen.queryByTestId('worker-deck-placeholder')).toBeNull()
    expect(screen.getByTestId('deck-scroll')).toBeInTheDocument()
  })

  it('a worker has no terminal view: nothing offers 「切到終端機」', () => {
    seed(summary())
    useConversationStore.setState({ byKey: { [`${H}\0${SID}`]: { ...entry(), status: 'error', doc: { ...emptyDoc(), turns: [] } } } } as never)
    render(ui())
    expect(screen.getByTestId('deck-unreadable')).toBeInTheDocument()
    expect(screen.queryByTestId('deck-to-terminal')).toBeNull()
  })

  it('the status row says the model (from the conversation\'s header) and the cost (from the row)', () => {
    seed(summary())
    render(ui())
    const row = screen.getByTestId('status-row')
    expect(row).toHaveTextContent('claude-haiku-5-5')
    expect(screen.getByTestId('item-cost')).toHaveTextContent('$1.50')
  })

  it('the small view menu goes back to the room', () => {
    seed(summary())
    const onModeChange = vi.fn()
    render(ui({ onModeChange }))
    fireEvent.click(screen.getByTestId('worker-deck-view-menu'))
    expect(screen.getByTestId('view-mode-deck')).toHaveAttribute('aria-checked', 'true')
    fireEvent.click(screen.getByTestId('view-mode-room'))
    expect(onModeChange).toHaveBeenCalledWith('room')
  })

  // --- U4-3: the permission request is a dock card, in the place a question's is (above the input), answered through Nexen's permission channel ---
  const ask = (id = 'req_a', seq = 1) => act(() => {
    useExecutionStore.getState().applyEvents(H, E, [{ seq, execution_id: E, kind: 'permission.requested', payload: { request_id: id, turn_id: 'trn_1', tool_name: 'Bash', input: { command: 'rm -rf build' } }, created_at: seq }])
  })

  it('no request, no card (and no dock)', () => {
    seed(summary({ state: 'running' }))
    render(ui())
    expect(screen.queryByTestId('permission-card')).toBeNull()
    expect(screen.queryByTestId('permission-dock')).toBeNull()
  })

  it('a request the worker waits on is a card in the dock: above the input, with the tool and what it wants to run; the input says to answer above', () => {
    seed(summary({ state: 'running' }))
    render(ui())
    ask()
    const dock = screen.getByTestId('permission-dock')
    expect(within(dock).getByTestId('permission-card')).toBeInTheDocument()
    expect(within(dock).getByTestId('permission-tool')).toHaveTextContent('Bash')
    expect(within(dock).getByTestId('permission-input')).toHaveTextContent('rm -rf build')
    expect(dock.compareDocumentPosition(screen.getByTestId('session-input')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy() // the input is below it
    expect(screen.getByRole('textbox')).toHaveAttribute('placeholder', expect.stringContaining('answer above'))
  })

  it('allow answers through Nexen\'s permission channel under the lease, and the card goes', async () => {
    vi.mocked(api.answerPermission).mockReset().mockResolvedValue({ request_id: 'req_a', outcome: 'allowed' } as never)
    seed(summary({ state: 'running' }))
    render(ui())
    ask()
    fireEvent.click(screen.getByTestId('permission-allow'))
    await vi.waitFor(() => expect(api.answerPermission).toHaveBeenCalledTimes(1))
    expect(vi.mocked(api.answerPermission).mock.calls[0].slice(0, 4)).toEqual([H, E, 'req_a', { decision: 'allow', leaseId: 'lease-1' }])
    await vi.waitFor(() => expect(screen.queryByTestId('permission-card')).toBeNull())
  })

  it('deny opens a note first, then sends it as the reason; the card goes', async () => {
    vi.mocked(api.answerPermission).mockReset().mockResolvedValue({ request_id: 'req_a', outcome: 'denied' } as never)
    seed(summary({ state: 'running' }))
    render(ui())
    ask()
    fireEvent.click(screen.getByTestId('permission-deny'))
    fireEvent.change(screen.getByTestId('permission-deny-note'), { target: { value: 'not in prod' } })
    fireEvent.click(screen.getByTestId('permission-deny'))
    await vi.waitFor(() => expect(api.answerPermission).toHaveBeenCalledTimes(1))
    expect(vi.mocked(api.answerPermission).mock.calls[0].slice(0, 4)).toEqual([H, E, 'req_a', { decision: 'deny', message: 'not in prod', leaseId: 'lease-1' }])
    await vi.waitFor(() => expect(screen.queryByTestId('permission-card')).toBeNull())
  })

  it('another client answered first: the resolution closes the card here too (and no answer is sent from here)', () => {
    seed(summary({ state: 'running' }))
    render(ui())
    ask()
    expect(screen.getByTestId('permission-card')).toBeInTheDocument()
    act(() => {
      useExecutionStore.getState().applyEvents(H, E, [{ seq: 2, execution_id: E, kind: 'permission.resolved', payload: { request_id: 'req_a', turn_id: 'trn_1', outcome: 'allowed' }, created_at: 2 }])
    })
    expect(screen.queryByTestId('permission-card')).toBeNull()
    expect(api.answerPermission).not.toHaveBeenCalled()
  })

  it('a request that expired leaves the muted line the room leaves', () => {
    seed(summary({ state: 'running' }))
    render(ui())
    ask()
    act(() => {
      useExecutionStore.getState().applyEvents(H, E, [{ seq: 2, execution_id: E, kind: 'permission.resolved', payload: { request_id: 'req_a', turn_id: 'trn_1', outcome: 'expired', timeout_s: 300 }, created_at: 2 }])
    })
    expect(screen.queryByTestId('permission-card')).toBeNull()
    expect(screen.getByTestId('permission-expired')).toBeInTheDocument()
  })

  it('the next request waiting shows after the first is answered (one card at a time, the earliest first)', async () => {
    vi.mocked(api.answerPermission).mockReset().mockResolvedValue({ request_id: 'req_a', outcome: 'allowed' } as never)
    seed(summary({ state: 'running' }))
    render(ui())
    ask('req_a', 1)
    ask('req_b', 2)
    expect(screen.getAllByTestId('permission-card')).toHaveLength(1)
    fireEvent.click(screen.getByTestId('permission-allow'))
    await vi.waitFor(() => expect(api.answerPermission).toHaveBeenCalledTimes(1))
    await vi.waitFor(() => expect(screen.getByTestId('permission-card')).toBeInTheDocument()) // req_b
    expect(vi.mocked(api.answerPermission).mock.calls[0][2]).toBe('req_a')
  })

  // --- U4-4a: the live line speaks from the worker's own Nexen events (fresher than the transcript), in the terminal deck's words ---
  const setSlice = (patch: Record<string, unknown>) => act(() => {
    useExecutionStore.setState((st) => ({ executions: { ...st.executions, [`${H}:${E}`]: { ...st.executions[`${H}:${E}`], ...patch } } }) as never)
  })
  const live = () => screen.getByTestId('turn-live').textContent ?? ''

  it('the live line thinks while the events name no running tool, and names the tool once they do (and thinks again when it ends)', () => {
    seed(summary({ state: 'running' }))
    render(ui())
    expect(live()).toContain('Thinking…')
    setSlice({ tools: { t1: { name: 'Bash', startedAt: 5, endedAt: null, status: 'running' } } })
    expect(live()).toContain('Running · Bash')
    setSlice({ tools: { t1: { name: 'Bash', startedAt: 5, endedAt: 9, status: 'done' } } })
    expect(live()).toContain('Thinking…')
  })

  it('the background work the events count (task_start / task_end) is on the live line, in the terminal deck\'s words', () => {
    seed(summary({ state: 'running' }))
    render(ui())
    const task = (id: string, kind: string, status = 'running') => ({ task_id: id, turn_id: 't', kind, task_type: 'x', tool_use_id: null, parent_tool_use_id: null, description: '', backgrounded: true, status, provider_status: null, closed_by: null, started_at: 1, ended_at: null, startSeq: 1 })
    setSlice({ tasks: { a: task('a', 'shell'), b: task('b', 'shell'), c: task('c', 'subagent'), d: task('d', 'other', 'completed') } })
    expect(live()).toContain('2 background command(s), 1 background subagent(s)')
    setSlice({ tasks: {} })
    expect(live()).not.toContain('background')
  })

  // --- U4-4b: the worker's current facts (stream state, observers, lease) are small chips on the right of the status row ---
  it('the status row\'s right side says the stream state, the observers and the lease holder, small', () => {
    seed(summary({ state: 'running', observers: 2, lease: { principal_id: 'someone-else', expires_at: Date.now() + 60_000 } }))
    setSlice({ sse: 'open' })
    render(ui())
    const chips = screen.getByTestId('worker-chips')
    expect(chips).toHaveTextContent('live · 2 observers · lease: someone-else')
    expect(within(chips).getByTestId('worker-chips-dot').className).toContain('bg-status-success')
  })

  it('a lease this client holds says "you"; none says so; a closed stream shows the error dot', () => {
    seed(summary({ state: 'running', observers: 1, lease: { principal_id: `h/${getNexClientId()}`, expires_at: Date.now() + 60_000 } }))
    setSlice({ sse: 'closed' })
    render(ui())
    const chips = screen.getByTestId('worker-chips')
    expect(chips).toHaveTextContent('disconnected · 1 observers · lease: you')
    expect(within(chips).getByTestId('worker-chips-dot').className).toContain('bg-status-error')
    act(() => { useExecutionStore.getState().setSummary(H, E, summary({ state: 'running', observers: 0 }) as never) })
    expect(screen.getByTestId('worker-chips')).toHaveTextContent('no lease')
  })

  // --- U4-4b: the hand-over controls (take to terminal, exit): the same flow as the room's, from the deck's own small menu and the status bar's terminal button ---
  const openMenu = () => fireEvent.click(screen.getByTestId('worker-deck-view-menu'))

  it('the view menu offers 終端機 (take to terminal) for a session-bound claude worker: an idle one goes at once', async () => {
    seed(summary({ state: 'idle' }))
    render(ui())
    openMenu()
    fireEvent.click(screen.getByTestId('view-mode-terminal'))
    await vi.waitFor(() => expect(takeToTerminal).toHaveBeenCalledTimes(1))
    expect(vi.mocked(takeToTerminal).mock.calls[0][0]).toMatchObject({ hostId: H, executionId: E, cwd: '/w/purdex' })
  })

  it('a running turn asks first: the same confirm as the room\'s, then takes it', async () => {
    seed(summary({ state: 'running' }))
    render(ui())
    openMenu()
    fireEvent.click(screen.getByTestId('view-mode-terminal'))
    expect(takeToTerminal).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('takeback-confirm'))
    await vi.waitFor(() => expect(takeToTerminal).toHaveBeenCalledTimes(1))
  })

  it('a worker handed over from a terminal session goes back to it (take-back), not to a new one', async () => {
    seed(summary({ state: 'idle' }))
    const from = { sessionCode: 'zk16vd', tmuxInstance: 'inst-1', cachedName: 'purdex' }
    render(ui({ from }))
    openMenu()
    fireEvent.click(screen.getByTestId('view-mode-terminal'))
    await vi.waitFor(() => expect(takeBack).toHaveBeenCalledTimes(1))
    expect(vi.mocked(takeBack).mock.calls[0][0]).toMatchObject({ hostId: H, executionId: E, from })
    expect(takeToTerminal).not.toHaveBeenCalled()
  })

  it('not offered where the room does not offer it: a queued worker, a non-claude provider', () => {
    seed(summary({ state: 'queued', session_id: undefined }))
    render(ui())
    openMenu() // the placeholder keeps the small menu (the way back to the room), but offers no terminal
    expect(screen.queryByTestId('view-mode-terminal')).toBeNull()
    cleanup()
    seed(summary({ state: 'idle', provider: 'codex' }))
    render(ui())
    openMenu()
    expect(screen.queryByTestId('view-mode-terminal')).toBeNull()
  })

  it('the status bar\'s terminal button reaches the deck too: the pane is registered while it is mounted, and taken back when it goes', () => {
    seed(summary({ state: 'idle' }))
    const { unmount } = render(ui())
    expect(getTakeToTerminal('p-w')).toMatchObject({ canTake: true, busy: false })
    unmount()
    expect(getTakeToTerminal('p-w')).toBeNull()
  })

  it('退出 is a small button on the status row: an idle worker exits at once, a running one asks first', async () => {
    vi.mocked(exitWorker).mockReset().mockResolvedValue({ state: 'terminated', archived: false } as never)
    seed(summary({ state: 'idle' }))
    render(ui())
    fireEvent.click(screen.getByTestId('worker-deck-exit'))
    await vi.waitFor(() => expect(exitWorker).toHaveBeenCalledTimes(1))
    expect(vi.mocked(exitWorker).mock.calls[0][0]).toMatchObject({ hostId: H, executionId: E })
    cleanup()
    vi.mocked(exitWorker).mockClear()
    useExecutionStore.setState({ executions: {} })
    seed(summary({ state: 'running' }))
    render(ui())
    fireEvent.click(screen.getByTestId('worker-deck-exit'))
    expect(exitWorker).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('exit-confirm'))
    await vi.waitFor(() => expect(exitWorker).toHaveBeenCalledTimes(1))
  })

  it('a lease another principal holds is said, not swallowed', () => {
    seed(summary())
    useExecutionStore.setState((st) => ({ executions: { ...st.executions, [`${H}:${E}`]: { ...st.executions[`${H}:${E}`], leaseError: { code: 'lease_held', heldBy: 'someone-else' } } } }) as never)
    render(ui())
    expect(screen.getByTestId('lease-held')).toHaveTextContent('someone-else')
  })

  it('an unreadable conversation offers no retry (a worker has nothing to ask again)', () => {
    seed(summary())
    useConversationStore.setState({ byKey: { [`${H}\0${SID}`]: { ...entry(), status: 'unreadable', reason: 'not_found' } } } as never)
    render(ui())
    expect(screen.queryByTestId('deck-retry')).toBeNull()
  })

  // --- U4-2: the footer is the deck's own input on the worker's adapter ---
  const typeAndSend = (text: string) => {
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: text } })
    fireEvent.keyDown(box, { key: 'Enter' })
  }
  const withUser = (text: string, at: number) => {
    const echoed = { type: 'user', id: `u-${text}`, at, index: 1, text, source: 'user' } as ConversationItem
    const e = entry()
    useConversationStore.setState({ byKey: { [`${H}\0${SID}`]: { ...e, doc: { ...e.doc, turns: [{ ...turn, items: [item, echoed] }] } } } } as never)
  }

  it('the footer is the deck\'s input (not the room\'s WorkerInput), and a worker has no "switch to terminal" in it', () => {
    seed(summary())
    render(ui())
    expect(screen.getByTestId('session-input')).toBeInTheDocument()
    expect(screen.queryByTestId('session-input-disabled')).toBeNull() // no mod to wait for: the worker answers for itself, whatever the capabilities say
  })

  it('a send goes through Nexen under the lease; until the transcript has it, it stays in the not-accepted frame, then it is gone', async () => {
    vi.useFakeTimers()
    seed(summary())
    render(ui())
    typeAndSend('run the tests')
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'undo') // the App holds it for the undo window
    expect(screen.getByTestId('queued-message').className).toContain('border-accent')
    expect(api.sendMessage).not.toHaveBeenCalled()
    await act(async () => { await vi.advanceTimersByTimeAsync(UNDO_MS) })
    expect(vi.mocked(api.sendMessage).mock.calls[0].slice(0, 4)).toEqual([H, E, 'lease-1', 'run the tests'])
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'sent') // handed to Nexen (which queues it itself while a turn runs)
    expect(screen.getByTestId('queued-message').className).toContain('border-accent') // still not accepted: the transcript has not got it
    act(() => { withUser('run the tests', Date.now()) })
    expect(screen.queryByTestId('queued-message')).toBeNull() // the transcript's own row takes over
  })

  it('a message handed over that is never echoed can be dismissed (it does not stay in the frame for good)', async () => {
    vi.useFakeTimers()
    seed(summary())
    render(ui())
    typeAndSend('never echoed')
    await act(async () => { await vi.advanceTimersByTimeAsync(UNDO_MS) })
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'sent')
    fireEvent.click(within(screen.getByTestId('queued-message')).getByRole('button', { name: 'Dismiss' }))
    expect(screen.queryByTestId('queued-message')).toBeNull()
  })

  it('the typed text is the worker\'s own draft (the room view shares it)', () => {
    seed(summary())
    render(ui())
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'half a thought' } })
    expect(readWorkerDraft(`${H}:${E}`)).toBe('half a thought')
  })

  it('the interrupt button interrupts the worker through Nexen', async () => {
    seed(summary())
    render(ui())
    fireEvent.click(screen.getByRole('button', { name: /interrupt/i }))
    await vi.waitFor(() => expect(api.interruptExecution).toHaveBeenCalledWith(H, E, 'lease-1'))
  })

  it('a file picked saves through the worker upload route and its path lands in the draft', async () => {
    seed(summary())
    render(ui())
    const input = screen.getByTestId('attach-file-input') as HTMLInputElement
    fireEvent.change(input, { target: { files: [new File(['x'], 'notes.txt', { type: 'text/plain' })] } })
    await vi.waitFor(() => expect(api.uploadWorkerFile).toHaveBeenCalled())
    await vi.waitFor(() => expect((screen.getByRole('textbox') as HTMLTextAreaElement).value).toContain('/w/purdex/notes.txt'))
  })

  it('a file dropped anywhere on the pane (not only the input) is saved the same way', async () => {
    seed(summary())
    render(ui())
    fireEvent.drop(screen.getByTestId('deck-scroll'), { dataTransfer: { files: [new File(['x'], 'drop.txt', { type: 'text/plain' })], types: ['Files'] } })
    await vi.waitFor(() => expect(api.uploadWorkerFile).toHaveBeenCalled())
  })

  it('a refused send (the lease is held by another) is shown as failed with a retry, not swallowed', async () => {
    vi.useFakeTimers()
    seed(summary())
    vi.mocked(api.sendMessage).mockRejectedValueOnce(new NexApiError(409, 'lease_held', 'held'))
    render(ui())
    typeAndSend('hello')
    await act(async () => { await vi.advanceTimersByTimeAsync(UNDO_MS) })
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'failed')
  })

  // --- U4-4a: the lifecycle is system rows in the stream, and an ended worker has an ended footer where the input was (WorkerEndedPane's content) ---
  it('an exited worker keeps its deck: a system row says it exited, and the ended footer takes the input\'s place', () => {
    seed(summary({ state: 'terminated', terminal_reason: 'idle timeout' }))
    render(ui())
    expect(screen.getByTestId('deck-scroll')).toBeInTheDocument()
    expect(screen.getByTestId('worker-lifecycle')).toHaveTextContent('This worker has exited')
    expect(screen.getByTestId('worker-ended-footer')).toBeInTheDocument()
    expect(screen.queryByRole('textbox')).toBeNull()
    expect(screen.queryByTestId('worker-ended')).toBeNull() // not the full screen
  })

  it('a worker that failed to start with a session says why on a system row, and offers exit besides the rebuild', () => {
    seed(summary({ state: 'failed', reject_reason: 'no capacity' }))
    render(ui())
    expect(screen.getByTestId('worker-lifecycle')).toHaveTextContent('Failed to start · Reason: no capacity')
    expect(within(screen.getByTestId('worker-ended-footer')).getByTestId('worker-ended-exit')).toBeInTheDocument()
    expect(within(screen.getByTestId('worker-ended-footer')).getByTestId('worker-rebuild')).toBeInTheDocument()
  })

  it('a worker that failed before it had a session: no deck to draw, the system row and the ended footer on their own', () => {
    seed(summary({ state: 'rejected', session_id: undefined, reject_reason: 'bad profile' }))
    render(ui())
    expect(screen.queryByTestId('deck-scroll')).toBeNull()
    expect(screen.getByTestId('worker-lifecycle')).toHaveTextContent('Failed to start · Reason: bad profile')
    expect(screen.getByTestId('worker-ended-footer')).toBeInTheDocument()
  })

  it('an ended worker whose conversation cannot be read still has its ended footer (the way to rebuild, exit or close is never lost)', () => {
    seed(summary({ state: 'terminated' }))
    useConversationStore.setState({ byKey: { [`${H}\0${SID}`]: { ...entry(), status: 'unreadable', reason: 'not_found' } } } as never)
    render(ui())
    expect(screen.getByTestId('deck-unreadable')).toBeInTheDocument()
    expect(screen.getByTestId('worker-ended-footer')).toBeInTheDocument()
  })

  it('an ended worker draws no live line, though its header still says running, and no stale background work', () => {
    seed(summary({ state: 'terminated' }))
    const task = { task_id: 'a', turn_id: 't', kind: 'shell', task_type: 'x', tool_use_id: null, parent_tool_use_id: null, description: '', backgrounded: true, status: 'running', provider_status: null, closed_by: null, started_at: 1, ended_at: null, startSeq: 1 }
    useExecutionStore.setState((st) => ({ executions: { ...st.executions, [`${H}:${E}`]: { ...st.executions[`${H}:${E}`], tasks: { a: task } } } }) as never)
    render(ui())
    expect(screen.queryByTestId('turn-live')).toBeNull()
    expect(screen.getByTestId('worker-lifecycle')).toBeInTheDocument()
  })

  it('the lifecycle row is the last thing in the stream: after a message still waiting to be read', () => {
    vi.useFakeTimers()
    seed(summary({ state: 'idle' }))
    render(ui())
    typeAndSend('last words')
    act(() => { useExecutionStore.getState().setSummary(H, E, summary({ state: 'terminated' }) as never) })
    const msg = screen.getByTestId('queued-message')
    const row = screen.getByTestId('worker-lifecycle')
    expect(msg.compareDocumentPosition(row) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('until the event history is loaded the transcript speaks for the live line (empty events are not "nothing runs")', () => {
    seed(summary({ state: 'running' }), false)
    const e = entry()
    const running = { type: 'step', id: 's1', at: 1, index: 1, kind: 'execute', tool: 'Bash', status: 'running', summary: 'ls', started_at: 1, input: {} } as ConversationItem
    useConversationStore.setState({ byKey: { [`${H}\0${SID}`]: { ...e, doc: { ...e.doc, turns: [{ ...turn, outcome: 'running', items: [item, running] }] } } } } as never)
    render(ui())
    expect(live()).toContain('Running · Bash')
    act(() => { useExecutionStore.getState().setHistoryLoaded(H, E, true) })
    expect(live()).toContain('Thinking…') // now the events are in, and name no tool
  })

  it('a worker still queued is a lifecycle line, not a deck (and has no ended footer)', () => {
    seed(summary({ state: 'queued', session_id: undefined }))
    render(ui())
    expect(screen.getByTestId('worker-deck-placeholder')).toHaveTextContent('Queued')
    expect(screen.queryByTestId('worker-ended-footer')).toBeNull()
  })

  it('the ended footer rebuilds through the same flow as the ended screen, and closes the tab', async () => {
    seed(summary({ state: 'terminated' }))
    render(ui())
    fireEvent.click(within(screen.getByTestId('worker-ended-footer')).getByTestId('worker-rebuild'))
    await vi.waitFor(() => expect(takeToTerminal).toHaveBeenCalledTimes(1)) // terminal: the only mode this test host offers
    expect(vi.mocked(takeToTerminal).mock.calls[0][0]).toMatchObject({ hostId: H, executionId: E })
    fireEvent.click(within(screen.getByTestId('worker-ended-footer')).getByRole('button', { name: 'Close tab' }))
    expect(closeTab).toHaveBeenCalledWith('t')
  })
  // --- the controls outlive the conversation's state, and wait for the pane's own unsent messages ---
  it('a worker with no session id yet (placeholder) can still be exited, with the running-turn confirm', async () => {
    vi.mocked(exitWorker).mockReset().mockResolvedValue({ state: 'terminated', archived: false } as never)
    seed(summary({ state: 'running', session_id: undefined }))
    render(ui())
    expect(screen.getByTestId('worker-deck-placeholder')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('worker-deck-exit'))
    expect(exitWorker).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('exit-confirm'))
    await vi.waitFor(() => expect(exitWorker).toHaveBeenCalledTimes(1))
  })

  it('an unreadable conversation keeps 退出 and the status bar\'s terminal button answerable (the confirm is not inside the deck\'s footer)', () => {
    seed(summary({ state: 'running' }))
    useConversationStore.setState({ byKey: { [`${H}\0${SID}`]: { ...entry(), status: 'error', doc: { ...emptyDoc(), turns: [] } } } } as never)
    render(ui())
    expect(screen.getByTestId('deck-unreadable')).toBeInTheDocument()
    act(() => { getTakeToTerminal('p-w')!.takeToTerminal() })
    expect(screen.getByTestId('takeback-confirm')).toBeInTheDocument()
  })

  it('a message the pane still holds (undo window) freezes 退出 and 帶回終端機 until it has gone out', async () => {
    vi.useFakeTimers()
    seed(summary({ state: 'idle' }))
    render(ui())
    typeAndSend('run the tests')
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'undo')
    expect(screen.getByTestId('worker-deck-exit')).toBeDisabled()
    expect(getTakeToTerminal('p-w')).toMatchObject({ canTake: true, busy: true })
    getTakeToTerminal('p-w')!.takeToTerminal()
    expect(takeToTerminal).not.toHaveBeenCalled()
    await act(async () => { await vi.advanceTimersByTimeAsync(UNDO_MS) }) // handed to Nexen: nothing of the pane's is on its way any more
    expect(screen.getByTestId('worker-deck-exit')).not.toBeDisabled()
    expect(getTakeToTerminal('p-w')).toMatchObject({ busy: false })
  })

  it('a confirm opened before a message was typed does not run once the message is on its way', async () => {
    vi.useFakeTimers()
    vi.mocked(exitWorker).mockReset().mockResolvedValue({ state: 'terminated', archived: false } as never)
    seed(summary({ state: 'running' }))
    render(ui())
    fireEvent.click(screen.getByTestId('worker-deck-exit'))
    typeAndSend('one more thing')
    fireEvent.click(screen.getByTestId('exit-confirm'))
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(exitWorker).not.toHaveBeenCalled()
  })
  it('a send still in flight when the session moves on keeps 退出 and 帶回終端機 held until it has answered (#2659)', async () => {
    vi.useFakeTimers()
    let answer: (v: unknown) => void = () => {}
    vi.mocked(api.sendMessage).mockReset().mockImplementation(() => new Promise((r) => { answer = r }) as never)
    seed(summary({ state: 'idle' }))
    render(ui())
    typeAndSend('run the tests')
    await act(async () => { await vi.advanceTimersByTimeAsync(UNDO_MS) })
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'sending')
    const SID2 = '11111111-2222-4333-8444-555555555555'
    useConversationStore.setState({ byKey: { [`${H}\0${SID}`]: entry(), [`${H}\0${SID2}`]: entry() } } as never)
    act(() => { useExecutionStore.getState().setSummary(H, E, summary({ state: 'idle', session_id: SID2 }) as never) })
    expect(acquire).toHaveBeenCalledWith(H, SID2)
    expect(screen.getByTestId('worker-deck-exit')).toBeDisabled() // the new session's queue is empty, the old request still runs
    expect(getTakeToTerminal('p-w')).toMatchObject({ busy: true })
    await act(async () => { answer({}); await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByTestId('worker-deck-exit')).not.toBeDisabled()
    expect(getTakeToTerminal('p-w')).toMatchObject({ busy: false })
  })

  it('the chips sit on the status row where the deck draws one, and with the controls where it does not', () => {
    seed(summary())
    render(ui())
    expect(within(screen.getByTestId('worker-deck-footer')).getByTestId('worker-chips')).toBeInTheDocument()
    expect(within(screen.getByTestId('worker-deck-handoff')).queryByTestId('worker-chips')).toBeNull()
    cleanup()
    seed(summary({ state: 'queued', session_id: undefined }))
    render(ui())
    expect(within(screen.getByTestId('worker-deck-handoff')).getByTestId('worker-chips')).toBeInTheDocument()
  })
})
