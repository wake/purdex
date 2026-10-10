// U3-1c b: the deck is mounted inside the session pane. The pane holds its conversation in EVERY view (plan D4), and what
// the reader did in the deck (an opened output, the scroll place) survives the tab being switched away and back — the
// real TabContent unmounts a session pane (keepAliveCount 0), as in SessionPaneContent.view-swap.test.tsx.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup, act, fireEvent } from '@testing-library/react'
import { TabContent } from './TabContent'
import { SessionPaneContent } from './SessionPaneContent'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { forgetFolds } from '../lib/conversations/fold-memory'
import { clearAllPanels, readPanel } from '../lib/conversations/panel-memory'
import { emptyDoc } from '../lib/conversations/model'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useSessionViewStore, sessionBinding } from '../stores/useSessionViewStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { createTab } from '../types/tab'
import type { Tab } from '../types/tab'
import type { PaneConversation } from '../hooks/useConversationOfPane'
import type { ConversationItem } from '../lib/conversations/types'

const conv = vi.hoisted(() => ({ value: { state: 'off' } as unknown, calls: [] as Array<{ enabled: boolean }> }))
vi.mock('../hooks/useConversationOfPane', () => ({
  useConversationOfPane: (_c: unknown, enabled: boolean) => { conv.calls.push({ enabled }); return conv.value },
}))
vi.mock('../hooks/useConversationViewGate', () => ({ useConversationViewGate: () => ({ ok: true, reason: null }) }))
vi.mock('./TerminalView', () => ({ default: () => <div data-testid="terminal-view" /> }))
vi.mock('../lib/host-api', async (orig) => ({ ...(await orig<typeof import('../lib/host-api')>()), fetchWsTicket: vi.fn(async () => 't') }))
vi.mock('../lib/rebuild/cwd-probe', () => ({ probeSessionCwd: vi.fn() }))
vi.mock('../lib/rebuild/provenance-probe', () => ({ probeSessionProvenance: vi.fn() }))
vi.mock('./deck/SessionInput', () => ({ SessionInput: () => <div data-testid="input-footer" /> }))

const H = 'host-1'
const CODE = 'dev001'
const sessionTab: Tab = {
  ...createTab({ kind: 'tmux-session', hostId: H, sessionCode: CODE, mode: 'terminal', cachedName: CODE, tmuxInstance: 'i' }),
  id: 't-session',
}
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const paneIdOf = (tab: Tab) => (tab.layout as { pane: { id: string } }).pane.id
const all = [sessionTab, dashTab]

const exec = {
  type: 'step', id: 'x1', at: 1, index: 0, kind: 'execute', tool: 'Bash', status: 'done', summary: 'ls', started_at: 1, input: null,
  command: { text: 'ls' }, output: { text: Array.from({ length: 30 }, (_, i) => `r${i}`).join('\n'), total_lines: 30, total_bytes: 1, truncated: false },
} as ConversationItem
const ready = (): PaneConversation => ({
  state: 'ready', hostId: H, sessionId: 's1',
  entry: { doc: { ...emptyDoc(), turns: [{ id: 't0', index: 0, started_at: 1, outcome: 'done', items: [exec] }] }, status: 'live', reason: '', paging: false, subagents: {} },
})

afterEach(() => { vi.restoreAllMocks() })
beforeEach(() => {
  // jsdom lays nothing out; the pane split needs a width (1000 docks the panel).
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(() => ({ width: 1000, height: 600, top: 0, left: 0, right: 1000, bottom: 600, x: 0, y: 0, toJSON: () => ({}) }))
  clearAllPanels()
  cleanup()
  conv.calls = []
  conv.value = ready()
  forgetFolds(`${paneIdOf(sessionTab)}\0s1`)
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

describe('the deck inside the session pane', () => {
  it('holds the conversation in every view, not only the deck', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(conv.calls.length).toBeGreaterThan(0)
    expect(conv.calls.every((c) => c.enabled)).toBe(true) // terminal view: the stream is held for the approvals
    setView('deck')
    expect(screen.getByTestId('deck-turn')).toBeInTheDocument()
    setView('chat')
    expect(conv.calls.every((c) => c.enabled)).toBe(true)
    expect(screen.queryByTestId('deck-turn')).toBeNull()
  })

  it('mounts the footer under the stream', () => {
    setView('deck')
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.getByTestId('input-footer')).toBeInTheDocument()
  })

  it('the chat view mounts the chat with the same footer, and the terminal layer stays mounted under it', () => {
    setView('chat')
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.getByTestId('chat-view')).toBeInTheDocument()
    expect(screen.getByTestId('input-footer')).toBeInTheDocument()
    expect(screen.getByTestId('terminal-view')).toBeInTheDocument()
    expect(screen.queryByTestId('deck-turn')).toBeNull()
  })

  it('an unreadable conversation in the chat says so in the chat\'s words and offers the terminal', () => {
    conv.value = { state: 'unreadable', reason: 'no_session', retry: () => {} }
    setView('chat')
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.getByTestId('unreadable')).toHaveAttribute('data-reason', 'no_session')
    fireEvent.click(screen.getByTestId('unreadable-terminal'))
    expect(screen.queryByTestId('unreadable')).toBeNull()
  })

  it('an opened output survives switching to another tab and back (the pane really unmounts)', () => {
    setView('deck')
    const view = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.click(screen.getByTestId('output-toggle'))
    expect(screen.getByTestId('output-body')).toBeInTheDocument()
    view.rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('deck-turn')).toBeNull()
    expect(screen.getByTestId('other-tab')).toBeInTheDocument()
    view.rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.getByTestId('output-body')).toBeInTheDocument()
  })

  it('an open right panel survives switching to another tab and back, in the deck and in the chat', () => {
    setView('deck')
    const view = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    fireEvent.click(screen.getByTestId('output-toggle'))
    fireEvent.click(screen.getByTestId('output-show-all'))
    expect(screen.getByTestId('session-right-panel')).toBeInTheDocument()
    view.rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    expect(screen.queryByTestId('session-right-panel')).toBeNull() // really unmounted
    view.rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.getByTestId('session-right-panel')).toBeInTheDocument()
    expect(readPanel(paneIdOf(sessionTab))?.content).toMatchObject({ kind: 'output', stepId: 'x1' })
    // the same pane in the chat: its work row opens a chain, and that survives too
    clearAllPanels()
    setView('chat')
    fireEvent.click(screen.getAllByTestId('chat-work')[0])
    expect(screen.getByTestId('session-right-panel')).toBeInTheDocument()
    view.rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    view.rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.getByTestId('session-right-panel')).toBeInTheDocument()
  })

  it('an unreadable conversation offers the terminal and switching goes there', () => {
    conv.value = { state: 'unreadable', reason: 'no_session', retry: () => {} }
    setView('deck')
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.getByTestId('deck-unreadable')).toBeInTheDocument()
    expect(useSessionViewStore.getState().byPane).not.toEqual({}) // it did not switch by itself
    fireEvent.click(screen.getByTestId('deck-to-terminal'))
    expect(screen.queryByTestId('deck-unreadable')).toBeNull()
    expect(screen.getByTestId('session-terminal-layer').style.visibility).toBe('')
  })
})
