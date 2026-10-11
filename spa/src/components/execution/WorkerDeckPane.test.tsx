// U4-1b: a worker pane in its 指揮台 view — the deck on the execution's own Claude Code conversation (bound to the row's session id), the lifecycle
// before that id exists, no way to a terminal, the worker's own input and send path in the footer, model and cost in the status row, and the way
// back to the room.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
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

vi.mock('../../lib/nex/nex-api', () => ({ sendMessage: vi.fn(), interruptExecution: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), uploadWorkerFile: vi.fn(), fetchExecutionPrelude: vi.fn(), listExecutions: vi.fn() }))
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
  cleanup(); clearAllPanels()
  useExecutionStore.setState({ executions: {} })
  useNexHostStore.setState({ ensure: async () => {}, byHost: { [H]: { phase: 'ready', capabilities: {} } } } as never)
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  vi.mocked(lease.useExecutionLease).mockReturnValue({ ensureLease: vi.fn(async () => 'lease-1'), release: vi.fn(), forget: vi.fn(), touch: vi.fn() } as never)
  vi.mocked(api.sendMessage).mockReset().mockResolvedValue({} as never)
  acquire.mockClear()
  useConversationStore.setState({ byKey: { [`${H}\0${SID}`]: entry() }, acquire } as never)
})
afterEach(() => { cleanup(); useNexHostStore.setState({ byHost: {}, ensure: realEnsure }) })

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

  it('the footer is the worker\'s own input: a send goes through the worker path (lease, sendMessage), not the terminal\'s', async () => {
    seed(summary())
    render(ui())
    const box = screen.getByRole('textbox')
    fireEvent.change(box, { target: { value: 'run the tests' } })
    fireEvent.keyDown(box, { key: 'Enter' })
    await vi.waitFor(() => expect(api.sendMessage).toHaveBeenCalled())
    expect(vi.mocked(api.sendMessage).mock.calls[0].slice(0, 4)).toEqual([H, E, 'lease-1', 'run the tests'])
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

  it('an exited worker has its own screen in the deck view too', () => {
    seed(summary({ state: 'terminated' }))
    render(ui())
    expect(screen.queryByTestId('deck-scroll')).toBeNull()
  })
})
