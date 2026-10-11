// #2664: the quick replies on a terminal session, wired in the REAL SessionPaneContent (deck and chat) against a fake daemon: a tap goes through the
// pane's send queue (the typed /submit after the App's buffer), leaves the draft alone, steps aside for a question card, is hidden where nothing can be
// sent, and its fold choice and its held message survive a tab switch.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { TabContent } from './TabContent'
import { SessionPaneContent } from './SessionPaneContent'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { clearAllDrafts, draftKey, readDraft } from '../lib/conversations/draft-memory'
import { emptyDoc } from '../lib/conversations/model'
import { clearAllAdapters } from '../lib/conversations/deck-adapter'
import { UNDO_MS, clearAllSendQueues } from '../lib/conversations/send-queue'
import type { Capabilities, ConversationApproval, ConversationItem } from '../lib/conversations/types'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useSessionViewStore, sessionBinding } from '../stores/useSessionViewStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { useHostConfigStore } from '../stores/useHostConfigStore'
import { useWorkerSettingsStore } from '../stores/useWorkerSettingsStore'
import { createTab } from '../types/tab'
import type { Tab } from '../types/tab'
import type { PaneConversation } from '../hooks/useConversationOfPane'

const fetchMock = vi.hoisted(() => vi.fn())
const conv = vi.hoisted(() => ({ value: { state: 'off' } as unknown }))
vi.mock('../hooks/useConversationOfPane', () => ({ useConversationOfPane: () => conv.value }))
vi.mock('../hooks/useConversationViewGate', () => ({ useConversationViewGate: () => ({ ok: true, reason: null }) }))
vi.mock('./TerminalView', () => ({ default: () => <div data-testid="terminal-view" /> }))
vi.mock('../lib/host-api', async (orig) => ({ ...(await orig<typeof import('../lib/host-api')>()), fetchWsTicket: vi.fn(async () => 't'), pinnedHostFetch: fetchMock }))
vi.mock('../lib/rebuild/cwd-probe', () => ({ probeSessionCwd: vi.fn() }))
vi.mock('../lib/rebuild/provenance-probe', () => ({ probeSessionProvenance: vi.fn() }))

const H = 'host-1'
const CODE = 'dev001'
const SID = 'aaaaaaaa-1111-4222-8333-444444444444'
const sessionTab: Tab = { ...createTab({ kind: 'tmux-session', hostId: H, sessionCode: CODE, mode: 'terminal', cachedName: CODE, tmuxInstance: 'i' }), id: 't-session' }
const paneId = (sessionTab.layout as { pane: { id: string } }).pane.id
const PROMPT: Capabilities = { send: 'prompt', interrupt: 'prompt' }

const answer = (status: number, body: unknown) => Promise.resolve(new Response(JSON.stringify(body), { status }))
const submits = () => fetchMock.mock.calls.filter((c) => String(c[1]).endsWith('/submit')).map((c) => ({ path: String(c[1]), host: c[0] as string, ...(JSON.parse(c[2].body) as { text: string; client_msg_id: string; expected_tmux_instance: string }) }))
const tick = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms))

const user = (id: string, text: string, extra: object = {}): ConversationItem => ({ type: 'user', id, at: Date.now(), index: 0, text, source: 'user', ...extra }) as ConversationItem
function ready(over: { status?: string; queued?: Array<{ text: string; at: number; source: string }>; capabilities?: Capabilities | null; items?: ConversationItem[]; sessionId?: string } = {}): PaneConversation {
  const items = over.items ?? [user('u0', 'earlier')]
  return {
    state: 'ready', hostId: H, sessionId: over.sessionId ?? SID,
    entry: {
      doc: {
        ...emptyDoc(),
        turns: [{ id: 't0', index: 0, started_at: 1, outcome: 'done', items }],
        header: { title: 'x', status: over.status ?? 'idle', backend: 'terminal', live: true, ...(over.queued ? { queued: over.queued } : {}) },
        capabilities: over.capabilities === undefined ? PROMPT : over.capabilities,
      },
      status: 'live', reason: '', paging: false, subagents: {},
    },
  }
}

const setView = (v: 'deck' | 'chat' | 'terminal') =>
  act(() => useSessionViewStore.getState().setView(sessionTab.id, paneId, sessionBinding(H, CODE), v))
const box = () => screen.getByRole('textbox') as HTMLTextAreaElement
const type = (v: string) => fireEvent.change(box(), { target: { value: v } })
const mount = () => render(<TabContent activeTab={sessionTab} allTabs={[sessionTab]} />)
const remount = (view: ReturnType<typeof mount>) => view.rerender(<TabContent activeTab={sessionTab} allTabs={[sessionTab]} />)

beforeEach(() => {
  cleanup()
  vi.useFakeTimers()
  fetchMock.mockReset()
  fetchMock.mockImplementation(() => answer(200, { outcome: 'handed', mode: 'fill' }))
  clearAllDrafts(); clearAllSendQueues(); clearAllAdapters()
  useSessionStore.setState({ sessions: {} })
  conv.value = ready()
  clearModuleRegistry()
  registerModule({ id: 'terminal', name: 'Terminal', panes: [{ kind: 'tmux-session', component: SessionPaneContent }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 } },
    hostOrder: [H], activeHostId: H, runtime: { [H]: { status: 'connected' as const, attachReady: true } },
  })
  useTabStore.setState({ tabs: { [sessionTab.id]: sessionTab }, tabOrder: [sessionTab.id], activeTabId: sessionTab.id, visitHistory: [] })
  useSessionViewStore.setState({ byPane: {} })
  useHostConfigStore.setState({
    byHost: { [H]: { status: 'ready', quickRepliesSupported: true, quickReplies: [{ id: 'a', title: 'continue', content: 'continue' }, { id: 'b', title: 'run the tests', content: 'run the tests' }], revisions: { quickReplies: 1 }, problems: {} } },
    ensureLoaded: async () => {},
  } as never)
  useWorkerSettingsStore.setState({ quickRepliesCollapsed: false })
})
afterEach(() => { cleanup(); clearAllDrafts(); clearAllSendQueues(); vi.useRealTimers() })


const replies = () => screen.queryAllByTestId('quick-reply')

describe.each(['deck', 'chat'] as const)('the quick replies in the %s', (view) => {
  it('a row above the input; a tap queues that text, the typed route sends it after the buffer, and the draft stays', async () => {
    setView(view)
    const ui = mount()
    expect(replies().map((b) => b.textContent)).toEqual(['continue', 'run the tests'])
    // the row sits between the dock and the input
    const footer = screen.getByTestId('session-footer')
    const order = [...footer.querySelectorAll('[data-testid="quick-replies"],[data-testid="session-input"]')].map((e) => e.getAttribute('data-testid'))
    expect(order).toEqual(['quick-replies', 'session-input'])
    type('half a thought')
    fireEvent.click(replies()[1])
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'undo')
    expect(box().value).toBe('half a thought')
    expect(readDraft(draftKey(paneId, H, SID))).toBe('half a thought')
    expect(submits()).toHaveLength(0)
    await tick(UNDO_MS)
    expect(submits()).toHaveLength(1)
    expect(submits()[0]).toMatchObject({ host: H, text: 'run the tests', path: `/api/sessions/${CODE}/submit`, expected_tmux_instance: 'i' })
    conv.value = ready({ items: [user('u0', 'earlier'), user('u1', 'run the tests', { client_msg_id: submits()[0].client_msg_id })] })
    remount(ui)
    await tick(0)
    expect(screen.queryByTestId('queued-message')).toBeNull()
  })

  it('a reply still inside the buffer is taken back with Undo: its text goes in front of the draft, nothing is sent', async () => {
    setView(view)
    mount()
    type('typed')
    fireEvent.click(replies()[0])
    fireEvent.click(screen.getByText('Undo'))
    expect(screen.queryByTestId('queued-message')).toBeNull()
    expect(box().value).toBe('continue\ntyped')
    await tick(UNDO_MS)
    expect(submits()).toHaveLength(0)
  })

  it('steps aside while a question card waits above the input, and is back when it is answered', () => {
    const ask: ConversationApproval = {
      id: 'a1', kind: 'hook_ask', state: 'open',
      payload: { tool_use_id: 'toolu_a1', questions: [{ question: '先做哪個方案？', multiSelect: false, options: [{ label: '甲案' }, { label: '乙案' }] }] },
    }
    const open = ready() as Extract<PaneConversation, { state: 'ready' }>
    open.entry!.doc.approvals = [ask]
    conv.value = open
    setView(view)
    const ui = mount()
    expect(screen.getByTestId('question-dock')).toBeInTheDocument()
    expect(screen.queryByTestId('quick-replies')).toBeNull()
    conv.value = ready() // answered elsewhere: no open question (the dock may still show its closing note for a moment)
    remount(ui)
    expect(replies()).toHaveLength(2)
  })

  it('a conversation with neither a mod nor keys has no row (nothing could be sent)', () => {
    conv.value = ready({ capabilities: { send: 'none' } as never })
    setView(view)
    mount()
    expect(replies()).toHaveLength(0)
    expect(screen.queryByTestId('quick-replies')).toBeNull()
  })

  it('a worker-owned conversation without its adapter bound has no row either', () => {
    conv.value = ready({ capabilities: { send: 'execution' } as never })
    setView(view)
    mount()
    expect(replies()).toHaveLength(0)
  })
})

describe('across a tab switch (the real TabContent)', () => {
  const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash-qr' }
  const both = [sessionTab, dashTab]
  beforeEach(() => {
    useTabStore.setState({ tabs: { [sessionTab.id]: sessionTab, [dashTab.id]: dashTab }, tabOrder: [sessionTab.id, dashTab.id], activeTabId: sessionTab.id, visitHistory: [] })
    registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: () => <div data-testid="other-tab" /> }] })
  })

  it.each(['deck', 'chat'] as const)('%s: the fold choice and a held reply outlive the switch; the reply is sent although the tab is away', async (view) => {
    setView(view)
    const ui = render(<TabContent activeTab={sessionTab} allTabs={both} />)
    fireEvent.click(replies()[0])
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'undo')
    fireEvent.click(screen.getByTestId('quick-reply-toggle'))
    expect(replies()).toHaveLength(0)
    ui.rerender(<TabContent activeTab={dashTab} allTabs={both} />)
    expect(screen.queryByTestId('session-footer')).toBeNull()
    await tick(UNDO_MS)
    expect(submits()).toHaveLength(1)
    expect(submits()[0].text).toBe('continue')
    ui.rerender(<TabContent activeTab={sessionTab} allTabs={both} />)
    expect(replies()).toHaveLength(0) // still folded
    expect(screen.getByTestId('quick-reply-toggle')).toHaveAttribute('aria-expanded', 'false')
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'sent')
  })
})
