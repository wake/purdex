// Regression (CLAUDE.md tab-hosted checklist): a worker pane in its deck view — what the reader typed in the worker's reply box, and the deck itself —
// survives switching to another tab and back. The pane really unmounts (keepAliveCount 0, an execution pane is not a "light" kind), so this mounts the
// real TabContent, not WorkerDeckPane alone.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
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
import { forgetWorkerDraft } from '../../lib/nex/worker-draft-memory'
import * as lease from '../../hooks/useExecutionLease'

vi.mock('../../lib/nex/nex-api', () => ({ sendMessage: vi.fn(), interruptExecution: vi.fn(), terminateExecution: vi.fn(), releaseLease: vi.fn(), uploadWorkerFile: vi.fn(), fetchExecutionPrelude: vi.fn(), listExecutions: vi.fn() }))
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
  cleanup(); clearModuleRegistry(); clearAllPanels()
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
  vi.mocked(lease.useExecutionLease).mockReturnValue({ ensureLease: vi.fn(), release: vi.fn(), forget: vi.fn(), touch: vi.fn() } as never)
})
afterEach(() => { forgetWorkerDraft(`${H}:${E}`); useNexHostStore.setState({ byHost: {}, ensure: realEnsure }) })

describe('the worker deck across tab switches', () => {
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
  })
})
