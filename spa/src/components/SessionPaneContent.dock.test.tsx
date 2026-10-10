// U3-4: the dock inside the session pane. The footer is [dock card] → input → status row; the terminal view draws no dock but a thin
// 「● 等你回答」 strip; and an open card — what was picked in it — survives the tab being switched away and back (the real TabContent
// unmounts a session pane: keepAliveCount 0, as in SessionPaneContent.deck.test.tsx).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup, act, fireEvent } from '@testing-library/react'
import { TabContent } from './TabContent'
import { SessionPaneContent } from './SessionPaneContent'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { clearAllDrafts } from '../lib/conversations/draft-memory'
import { clearAllSendQueues } from '../lib/conversations/send-queue'
import { clearAllPanels } from '../lib/conversations/panel-memory'
import { forgetDockDraftsOfPane } from '../lib/conversations/dock-memory'
import { emptyDoc } from '../lib/conversations/model'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useSessionViewStore, sessionBinding } from '../stores/useSessionViewStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { createTab } from '../types/tab'
import type { Tab } from '../types/tab'
import type { PaneConversation } from '../hooks/useConversationOfPane'
import type { ConversationApproval } from '../lib/conversations/types'

const conv = vi.hoisted(() => ({ value: { state: 'off' } as unknown }))
vi.mock('../hooks/useConversationOfPane', () => ({ useConversationOfPane: () => conv.value }))
vi.mock('../hooks/useConversationViewGate', () => ({ useConversationViewGate: () => ({ ok: true, reason: null }) }))
vi.mock('./TerminalView', () => ({ default: () => <div data-testid="terminal-view" /> }))
vi.mock('../lib/host-api', async (orig) => ({ ...(await orig<typeof import('../lib/host-api')>()), fetchWsTicket: vi.fn(async () => 't') }))
vi.mock('../lib/rebuild/cwd-probe', () => ({ probeSessionCwd: vi.fn() }))
vi.mock('../lib/rebuild/provenance-probe', () => ({ probeSessionProvenance: vi.fn() }))
vi.mock('./deck/SessionInput', () => ({ SessionInput: (p: { asking?: boolean }) => <div data-testid="input-footer" data-asking={String(p.asking)} /> }))
vi.mock('./deck/SessionStatusRow', () => ({ SessionStatusRow: () => <div data-testid="status-row" /> }))

const H = 'host-1'
const CODE = 'dev001'
const sessionTab: Tab = {
  ...createTab({ kind: 'tmux-session', hostId: H, sessionCode: CODE, mode: 'terminal', cachedName: CODE, tmuxInstance: 'i' }),
  id: 't-session',
}
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const paneIdOf = (tab: Tab) => (tab.layout as { pane: { id: string } }).pane.id
const all = [sessionTab, dashTab]

const ask = (id: string): ConversationApproval => ({
  id, kind: 'hook_ask', state: 'open',
  payload: { tool_use_id: `toolu_${id}`, questions: [{ question: '先做哪個方案？', multiSelect: false, options: [{ label: '甲案' }, { label: '乙案' }] }] },
})
const ready = (approvals: ConversationApproval[]): PaneConversation => ({
  state: 'ready', hostId: H, sessionId: 's1',
  entry: {
    doc: { ...emptyDoc(), approvals, turns: [{ id: 't0', index: 0, started_at: 1, outcome: 'running', items: [{ type: 'user', id: 'u', at: 1, index: 0, text: 'go', source: 'user' }] }] },
    status: 'live', reason: '', paging: false, subagents: {},
  },
})

afterEach(() => { vi.restoreAllMocks() })
beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(() => ({ width: 1000, height: 600, top: 0, left: 0, right: 1000, bottom: 600, x: 0, y: 0, toJSON: () => ({}) }))
  clearAllPanels()
  cleanup()
  clearAllDrafts(); clearAllSendQueues()
  forgetDockDraftsOfPane(paneIdOf(sessionTab))
  conv.value = ready([ask('a1')])
  clearModuleRegistry()
  registerModule({ id: 'terminal', name: 'Terminal', panes: [{ kind: 'tmux-session', component: SessionPaneContent }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: () => <div data-testid="other-tab" /> }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 } },
    hostOrder: [H], activeHostId: H, runtime: { [H]: { status: 'connected' as const, attachReady: true } },
  })
  useTabStore.setState({ tabs: { [sessionTab.id]: sessionTab, [dashTab.id]: dashTab }, tabOrder: [sessionTab.id, dashTab.id], activeTabId: sessionTab.id, visitHistory: [] })
  useSessionViewStore.setState({ byPane: {} })
})

const setView = (v: 'terminal' | 'deck' | 'chat') =>
  act(() => useSessionViewStore.getState().setView(sessionTab.id, paneIdOf(sessionTab), sessionBinding(H, CODE), v))

describe('the dock in the footer', () => {
  it('stacks [dock card] → input → status row, and the input knows a question waits', () => {
    setView('deck')
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    const footer = screen.getByTestId('session-footer')
    expect([...footer.children].map((c) => c.getAttribute('data-testid'))).toEqual(['question-dock', 'input-footer', 'status-row'])
    expect(screen.getByTestId('input-footer')).toHaveAttribute('data-asking', 'true')
  })

  it('with no open question the footer is just the input and the status row', () => {
    conv.value = ready([])
    setView('deck')
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect([...screen.getByTestId('session-footer').children].map((c) => c.getAttribute('data-testid'))).toEqual(['input-footer', 'status-row'])
    expect(screen.getByTestId('input-footer')).toHaveAttribute('data-asking', 'false')
  })

  it('the chat view stacks the same footer', () => {
    setView('chat')
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.getByTestId('question-dock')).toBeInTheDocument()
  })
})

describe('the terminal view', () => {
  it('draws no dock but a strip while a question is open, and drops it when the question closes', () => {
    const view = render(<TabContent activeTab={sessionTab} allTabs={all} />) // the terminal is the default view
    expect(screen.queryByTestId('question-dock')).toBeNull()
    expect(screen.getByTestId('ask-strip')).toHaveTextContent('Waiting for your answer')
    conv.value = ready([])
    view.rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.queryByTestId('ask-strip')).toBeNull()
  })

  it('shows no strip in the deck (the card is there) or without a question', () => {
    setView('deck')
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.queryByTestId('ask-strip')).toBeNull()
    cleanup()
    conv.value = ready([])
    setView('terminal')
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.queryByTestId('ask-strip')).toBeNull()
  })

  it('a question read from a hook_permission is no strip (P8b)', () => {
    conv.value = ready([{ id: 'p', kind: 'hook_permission', state: 'open', payload: {} }])
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.queryByTestId('ask-strip')).toBeNull()
  })
})

describe('the open card across the tab being switched away and back (CLAUDE.md tab-hosted rule)', () => {
  it('the card comes back with what was picked in it', () => {
    setView('deck')
    const view = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.click(screen.getAllByTestId('dock-option')[1])
    fireEvent.change(screen.getByTestId('dock-other'), { target: { value: '' } })
    view.rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('question-dock')).toBeNull()
    expect(screen.getByTestId('other-tab')).toBeInTheDocument()
    view.rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.getByTestId('dock-card')).toBeInTheDocument()
    expect(screen.getAllByTestId('dock-option')[1]).toHaveAttribute('data-chosen', 'true')
  })

  it('the strip is still there on the terminal view after the switch', () => {
    const view = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    view.rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    view.rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.getByTestId('ask-strip')).toBeInTheDocument()
  })
})
