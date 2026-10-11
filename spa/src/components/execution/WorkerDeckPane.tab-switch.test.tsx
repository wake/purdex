// Regression (CLAUDE.md tab-hosted checklist): a worker pane in its deck view — what the reader typed in the worker's reply box, and the deck itself —
// survives switching to another tab and back. The pane really unmounts (keepAliveCount 0, an execution pane is not a "light" kind), so this mounts the
// real TabContent, not WorkerDeckPane alone.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, render, screen, fireEvent, cleanup, within } from '@testing-library/react'
import { TabContent } from '../TabContent'
import WorkerDeckPane from './WorkerDeckPane'
import { registerModule, clearModuleRegistry, type PaneRendererProps } from '../../lib/module-registry'
import { emptyDoc } from '../../lib/conversations/model'
import { clearAllPanels } from '../../lib/conversations/panel-memory'
import type { ConversationItem, Turn } from '../../lib/conversations/types'
import { useConversationStore, type ConversationEntry } from '../../stores/useConversationStore'
import { useExecutionStore } from '../../stores/useExecutionStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useHostConfigStore } from '../../stores/useHostConfigStore'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { clearAllWorkerDrafts, forgetWorkerDraft, readWorkerDraft } from '../../lib/nex/worker-draft-memory'
import { clearAllPermissionCards, permissionCardCount } from '../../lib/nex/permission-card-memory'
import { UNDO_MS, clearAllSendQueues } from '../../lib/conversations/send-queue'
import { clearAllAdapters } from '../../lib/conversations/deck-adapter'
import { clearAllWorkerLeases } from '../../lib/conversations/worker-adapter'
import { clearAllDrafts } from '../../lib/conversations/draft-memory'
import * as api from '../../lib/nex/nex-api'
import * as lease from '../../hooks/useExecutionLease'

vi.mock('../../lib/nex/nex-api', () => ({ sendMessage: vi.fn(), interruptExecution: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), uploadWorkerFile: vi.fn(), fetchExecutionPrelude: vi.fn(), listExecutions: vi.fn(), answerPermission: vi.fn(), fetchNexHost: vi.fn(() => new Promise(() => {})) }))
vi.mock('../../hooks/useExecutionSubscription', () => ({ useExecutionSubscription: vi.fn(() => ({ problem: null, paused: false })) }))
vi.mock('../../hooks/useExecutionLease', () => ({ useExecutionLease: vi.fn() }))

const H = 'h', E = 'exc_1', SID = '7f3e9c1a-2b4d-4e6f-8a0b-1c2d3e4f5a6b'
const summary = { id: E, state: 'idle', provider: 'claude', principal_id: 'p', cwd: '/w', mount_kind: 'dev', brief: 'b', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 1, archived: false, effective_profile: 'standard', turn_count: 1, session_id: SID }
const item = { type: 'user', id: 'u1', at: 1, index: 0, text: 'hello worker', source: 'user' } as ConversationItem
const turn: Turn = { id: 't0', index: 0, started_at: 1, outcome: 'done', items: [item] }
const entry: ConversationEntry = { doc: { ...emptyDoc(), turns: [turn] }, status: 'live', reason: '', paging: false, subagents: {} }

// The real wrapper keeps the mode on the pane content (it persists with the tab); here the renderer is fixed on the deck.
function DeckRenderer({ pane, isActive, isFocusTarget = false }: PaneRendererProps) {
  if (pane.content.kind !== 'execution') return null
  return <WorkerDeckPane hostId={H} executionId={pane.content.executionId} isActive={isActive} isFocusTarget={isFocusTarget}
    tabId="t-exec" paneId={pane.id} onModeChange={() => {}} />
}
const Other = () => <div data-testid="other-tab" />
const execTab: Tab = { ...createTab({ kind: 'execution', executionId: E, host: H, mode: 'deck' }), id: 't-exec' }
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const all = [execTab, dashTab]
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const realEnsure = useNexHostStore.getState().ensure

beforeEach(() => {
  cleanup(); clearModuleRegistry(); clearAllPanels(); clearAllSendQueues(); clearAllAdapters(); clearAllWorkerLeases(); clearAllDrafts(); clearAllWorkerDrafts(); clearAllPermissionCards()
  vi.mocked(api.sendMessage).mockReset().mockResolvedValue({} as never)
  registerModule({ id: 'nex', name: 'Nex', panes: [{ kind: 'execution', component: DeckRenderer }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: Other }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: async () => {} })
  useNexHostStore.setState({ ensure: async () => {}, byHost: { [H]: { phase: 'ready', capabilities: {} } } } as never)
  useExecutionStore.setState({ executions: {} })
  useExecutionStore.getState().setSummary(H, E, summary as never)
  useExecutionStore.getState().setHistoryLoaded(H, E, true)
  useConversationStore.setState({ byKey: { [`${H}\0${SID}`]: entry }, acquire: () => () => {} } as never)
  vi.mocked(lease.useExecutionLease).mockReturnValue({ ensureLease: vi.fn(async () => 'lease-1'), release: vi.fn(), forget: vi.fn(), touch: vi.fn() } as never)
})
afterEach(() => { forgetWorkerDraft(`${H}:${E}`); vi.useRealTimers(); useNexHostStore.setState({ byHost: {}, ensure: realEnsure }) })

describe('the worker deck across tab switches', () => {
  it('an exited worker\'s deck (stream, system row, ended footer) is drawn again after a switch away and back', () => {
    useExecutionStore.getState().setSummary(H, E, { ...summary, state: 'terminated', terminal_reason: 'idle timeout' } as never)
    const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
    expect(screen.getByTestId('worker-lifecycle')).toHaveTextContent('This worker has exited · Reason: idle timeout')
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('worker-ended-footer')).toBeNull()
    rerender(<TabContent activeTab={execTab} allTabs={all} />)
    expect(screen.getByTestId('deck-scroll')).toBeInTheDocument()
    expect(screen.getByTestId('worker-lifecycle')).toHaveTextContent('This worker has exited')
    expect(screen.getByTestId('worker-ended-footer')).toBeInTheDocument()
  })

  it('keeps the typed reply, and draws the deck again, when the reader switches to another tab and back', () => {
    const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
    expect(screen.getByTestId('deck-scroll')).toBeInTheDocument()
    fireEvent.change(box(), { target: { value: 'half a thought' } })

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('deck-scroll')).toBeNull() // really unmounted, not hidden
    expect(screen.getByTestId('other-tab')).toBeInTheDocument()

    rerender(<TabContent activeTab={execTab} allTabs={all} />)
    expect(screen.getByTestId('deck-scroll')).toBeInTheDocument()
    expect(box().value).toBe('half a thought')
    expect(readWorkerDraft(`${H}:${E}`)).toBe('half a thought') // the worker's own memory (the room view reads the same)
  })

  // The undo window ends after the pane is gone: the lease belongs to the mounted view, so the message is refused, said so, and can be sent again.
  it('a message whose undo window ends while another tab is shown is not lost: it comes back as failed, and a resend after returning goes through', async () => {
    vi.useFakeTimers()
    const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
    fireEvent.change(box(), { target: { value: 'send me' } })
    fireEvent.keyDown(box(), { key: 'Enter' })
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'undo')

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('deck-scroll')).toBeNull()
    await act(async () => { await vi.advanceTimersByTimeAsync(UNDO_MS + 10) })
    expect(api.sendMessage).not.toHaveBeenCalled() // nothing went out under a lease nobody holds

    rerender(<TabContent activeTab={execTab} allTabs={all} />)
    const msg = screen.getByTestId('queued-message')
    expect(msg).toHaveAttribute('data-state', 'failed')
    expect(msg).toHaveTextContent('send me')
    expect(msg).toHaveTextContent('Left this view')

    fireEvent.click(within(msg).getByRole('button', { name: 'Try again' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(50) })
    expect(vi.mocked(api.sendMessage).mock.calls[0].slice(0, 4)).toEqual([H, E, 'lease-1', 'send me'])
  })
})

// U4-3: the permission request's card in the worker deck. Its note, open note field and expanded preview, and the mark of an answer sent just before the
// switch (its resolution may not have arrived yet), live outside the card (permission-card-memory), so the switch that unmounts the pane brings them back.
describe('the worker deck\'s permission card across tab switches', () => {
  const LONG = `python3 -c "print(1)" ${'x'.repeat(500)} THE-TAIL`
  let seq = 0
  const apply = (kind: string, payload: Record<string, unknown>) => act(() => {
    seq += 1
    useExecutionStore.getState().applyEvents(H, E, [{ seq, execution_id: E, kind, payload, created_at: Date.now() }])
  })
  const ask = (requestId = 'req_a') => apply('permission.requested', { request_id: requestId, turn_id: 'trn_1', tool_name: 'Bash', input: { command: LONG } })
  const note = () => screen.getByTestId('permission-deny-note') as HTMLInputElement

  beforeEach(() => {
    seq = 0
    useExecutionStore.getState().setSummary(H, E, { ...summary, state: 'running' } as never)
  })

  it('keeps the typed note, the open note field and the expanded input when the reader switches away and back', () => {
    const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
    ask()
    fireEvent.click(screen.getByTestId('permission-expand'))
    fireEvent.click(screen.getByTestId('permission-deny'))
    fireEvent.change(note(), { target: { value: 'not in prod' } })

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('permission-card')).toBeNull() // really unmounted, not hidden

    rerender(<TabContent activeTab={execTab} allTabs={all} />)
    expect(within(screen.getByTestId('permission-dock')).getByTestId('permission-card')).toBeInTheDocument()
    expect(note().value).toBe('not in prod')
    expect(screen.getByTestId('permission-input')).toHaveTextContent('THE-TAIL')
    expect(screen.getByTestId('permission-expand')).toHaveTextContent('Show less')
  })

  it('an answer sent just before the switch stays answered on return (its resolution may not have come yet): no card again', async () => {
    vi.mocked(api.answerPermission).mockReset().mockResolvedValue({ request_id: 'req_a', outcome: 'allowed' } as never)
    const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
    ask()
    fireEvent.click(screen.getByTestId('permission-allow'))
    await vi.waitFor(() => expect(api.answerPermission).toHaveBeenCalledTimes(1))
    await vi.waitFor(() => expect(screen.queryByTestId('permission-card')).toBeNull())

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    rerender(<TabContent activeTab={execTab} allTabs={all} />)
    expect(screen.queryByTestId('permission-card')).toBeNull() // the store still reads pending; the mark keeps it closed
    expect(api.answerPermission).toHaveBeenCalledTimes(1)
  })

  it('a request another client answered while the reader was away is not drawn on return, and its memory goes', () => {
    const { rerender } = render(<TabContent activeTab={execTab} allTabs={all} />)
    ask()
    fireEvent.click(screen.getByTestId('permission-deny'))
    fireEvent.change(note(), { target: { value: 'for req_a' } })
    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    apply('permission.resolved', { request_id: 'req_a', turn_id: 'trn_1', outcome: 'allowed' })
    rerender(<TabContent activeTab={execTab} allTabs={all} />)
    expect(screen.queryByTestId('permission-card')).toBeNull()
    expect(permissionCardCount()).toBe(0)
  })
})

