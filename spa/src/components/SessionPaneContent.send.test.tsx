// U3 mount, wiring check: the REAL SessionPaneContent -> DeckPane / ChatPane -> SessionInput chain against a fake daemon. What the
// input gets (pane key, host, the conversation's session id, live capabilities, the transcript's items, the idle flag) decides
// whether a send is paired with its echo, a busy answer is resent once, and a missing mod is shown.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { TabContent } from './TabContent'
import { SessionPaneContent } from './SessionPaneContent'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { clearAllDrafts, draftKey, readDraft } from '../lib/conversations/draft-memory'
import { emptyDoc } from '../lib/conversations/model'
import { clearAllAdapters } from '../lib/conversations/deck-adapter'
import { UNDO_MS, clearAllSendQueues } from '../lib/conversations/send-queue'
import type { Capabilities, ConversationItem } from '../lib/conversations/types'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useSessionViewStore, sessionBinding } from '../stores/useSessionViewStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
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
const enter = () => fireEvent.keyDown(box(), { key: 'Enter' })
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
})
afterEach(() => { cleanup(); clearAllDrafts(); clearAllSendQueues(); vi.useRealTimers() })

describe.each(['deck', 'chat'] as const)('the input wired into the %s', (view) => {
  it('sends through the daemon\'s typed route: the SESSION CODE on the pane\'s host, with the pane\'s tmux generation, then the transcript echo (client_msg_id) settles the queued message', async () => {
    setView(view)
    const ui = mount()
    type('hello agent'); enter()
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'undo')
    await tick(UNDO_MS)
    expect(submits()).toHaveLength(1)
    expect(submits()[0]).toMatchObject({ host: H, text: 'hello agent', path: `/api/sessions/${CODE}/submit`, expected_tmux_instance: 'i' })
    expect(submits()[0].client_msg_id).toBeTruthy()
    expect(fetchMock.mock.calls.some((c) => String(c[1]).includes('/api/conversations/claude/') && String(c[1]).endsWith('/submit'))).toBe(false) // the old mod route is not used
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'sent')
    // the transcript brings the user item for it, carrying the id the App chose
    conv.value = ready({ items: [user('u0', 'earlier'), user('u1', 'hello agent', { client_msg_id: submits()[0].client_msg_id })] })
    remount(ui)
    await tick(0)
    expect(screen.queryByTestId('queued-message')).toBeNull()
    expect(submits()).toHaveLength(1)
  })

  it('a refusal (the terminal input has text) fails the message with its own text; nothing is resent by itself', async () => {
    fetchMock.mockImplementationOnce(() => answer(409, { outcome: 'refused', reason: 'input_not_empty', error: 'input_not_empty' }))
    setView(view)
    mount()
    type('blocked'); enter()
    await tick(UNDO_MS)
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'failed')
    await tick(30_000)
    expect(submits()).toHaveLength(1)
    expect(screen.getByRole('textbox')).toBeInTheDocument()
  })

  it('a conversation with neither a mod nor keys shows the disabled input with the way to the terminal; no request is made', async () => {
    conv.value = ready({ capabilities: {} })
    setView(view)
    mount()
    expect(screen.getByTestId('session-input-disabled')).toBeInTheDocument()
    expect(screen.queryByRole('textbox')).toBeNull()
    fireEvent.click(screen.getByText('Switch to terminal'))
    expect(screen.queryByTestId('session-input')).toBeNull() // switched to the terminal view
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('the `keys` capability (a live terminal conversation with no mod) enables the input, and a send goes through the same route', async () => {
    conv.value = ready({ capabilities: { send: 'keys', interrupt: 'prompt' } })
    setView(view)
    mount()
    expect(screen.queryByTestId('session-input-disabled')).toBeNull()
    type('typed by keys'); enter()
    await tick(UNDO_MS)
    expect(submits()).toHaveLength(1)
    expect(submits()[0]).toMatchObject({ path: `/api/sessions/${CODE}/submit`, text: 'typed by keys' })
  })

  it('the tmux generation stamped AFTER the queue exists is the one sent (the adapter reads its target at each send)', async () => {
    const legacy: Tab = { ...createTab({ kind: 'tmux-session', hostId: H, sessionCode: CODE, mode: 'terminal', cachedName: CODE, tmuxInstance: '' }), id: 't-legacy' }
    const legacyPane = (legacy.layout as { pane: { id: string } }).pane.id
    useTabStore.setState({ tabs: { [legacy.id]: legacy }, tabOrder: [legacy.id], activeTabId: legacy.id, visitHistory: [] })
    act(() => useSessionViewStore.getState().setView(legacy.id, legacyPane, sessionBinding(H, CODE), view))
    render(<TabContent activeTab={legacy} allTabs={[legacy]} />)
    type('late stamp'); enter() // the queue now exists, the instance is still unknown
    act(() => useTabStore.getState().adoptTmuxInstance(H, CODE, 'g-late'))
    await tick(UNDO_MS)
    expect(submits()).toHaveLength(1)
    expect(submits()[0].expected_tmux_instance).toBe('g-late')
  })

  it('no generation anywhere: nothing is sent (`no_instance`), the message fails with a reason, no request is made', async () => {
    const legacy: Tab = { ...createTab({ kind: 'tmux-session', hostId: H, sessionCode: CODE, mode: 'terminal', cachedName: CODE, tmuxInstance: '' }), id: 't-legacy' }
    const legacyPane = (legacy.layout as { pane: { id: string } }).pane.id
    useTabStore.setState({ tabs: { [legacy.id]: legacy }, tabOrder: [legacy.id], activeTabId: legacy.id, visitHistory: [] })
    act(() => useSessionViewStore.getState().setView(legacy.id, legacyPane, sessionBinding(H, CODE), view))
    render(<TabContent activeTab={legacy} allTabs={[legacy]} />)
    type('who am i'); enter()
    await tick(UNDO_MS)
    expect(fetchMock).not.toHaveBeenCalled()
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'failed')
  })

  it('the session list\'s generation is the fallback when the pane has none', async () => {
    const legacy: Tab = { ...createTab({ kind: 'tmux-session', hostId: H, sessionCode: CODE, mode: 'terminal', cachedName: CODE, tmuxInstance: '' }), id: 't-legacy' }
    const legacyPane = (legacy.layout as { pane: { id: string } }).pane.id
    useTabStore.setState({ tabs: { [legacy.id]: legacy }, tabOrder: [legacy.id], activeTabId: legacy.id, visitHistory: [] })
    useSessionStore.setState({ sessions: { [H]: [{ code: CODE, name: 'x', cwd: '/', mode: 'terminal', tmux_instance: 'g-list' }] } })
    act(() => useSessionViewStore.getState().setView(legacy.id, legacyPane, sessionBinding(H, CODE), view))
    render(<TabContent activeTab={legacy} allTabs={[legacy]} />)
    type('from the list'); enter()
    await tick(UNDO_MS)
    expect(submits()[0].expected_tmux_instance).toBe('g-list')
  })

  it('the draft and the queue are the pane\'s AND the session\'s: another session of the same pane starts empty', async () => {
    setView(view)
    const ui = mount()
    type('half a thought')
    expect(readDraft(draftKey(paneId, H, SID))).toBe('half a thought')
    conv.value = ready({ sessionId: 'bbbbbbbb-1111-4222-8333-444444444444' })
    remount(ui)
    expect(box().value).toBe('') // the input is mounted afresh for the new session
    type('new thought'); enter()
    await tick(UNDO_MS)
    expect(submits()[0].path).toBe(`/api/sessions/${CODE}/submit`) // the route names the tmux session, whichever conversation it shows
    expect(submits()[0].text).toBe('new thought')
  })
})

describe('the queue is driven by the pane, not by the input (every view)', () => {
  it('the transcript echo is matched while the terminal view is up (nothing stays queued behind it)', async () => {
    conv.value = ready()
    setView('deck')
    const ui = mount()
    type('hello'); enter()
    setView('terminal')
    await tick(UNDO_MS)
    conv.value = ready({ items: [user('u0', 'earlier'), user('u1', 'hello', { client_msg_id: submits()[0].client_msg_id })] })
    remount(ui)
    await tick(0)
    setView('deck')
    expect(screen.queryByTestId('queued-message')).toBeNull()
  })
})

describe('the remount race: the pane is away while a request is in flight', () => {
  const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
  const both = [sessionTab, dashTab]

  it('sent, tab switched away, back, THEN the answer lands: the message is sent (settled by its echo), and nothing is sent twice', async () => {
    let answerLate: (r: Response) => void = () => {}
    fetchMock.mockImplementationOnce(() => new Promise<Response>((r) => { answerLate = r }))
    useTabStore.setState({ tabs: { [sessionTab.id]: sessionTab, [dashTab.id]: dashTab }, tabOrder: [sessionTab.id, dashTab.id], activeTabId: sessionTab.id, visitHistory: [] })
    registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: () => <div data-testid="other-tab" /> }] })
    setView('deck')
    const ui = render(<TabContent activeTab={sessionTab} allTabs={both} />)
    type('slow one'); enter()
    await tick(UNDO_MS)
    expect(submits()).toHaveLength(1) // in flight
    ui.rerender(<TabContent activeTab={dashTab} allTabs={both} />)
    expect(screen.queryByTestId('session-footer')).toBeNull() // really unmounted
    ui.rerender(<TabContent activeTab={sessionTab} allTabs={both} />)
    await tick(0)
    await act(async () => { answerLate(new Response(JSON.stringify({ outcome: 'handed', mode: 'fill' }), { status: 200 })) })
    await tick(5000)
    expect(submits()).toHaveLength(1)
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'sent')
  })
})

describe('across a tab switch (the real TabContent)', () => {
  const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash2' }
  const both = [sessionTab, dashTab]
  const away = (ui: ReturnType<typeof render>) => { ui.rerender(<TabContent activeTab={dashTab} allTabs={both} />); expect(screen.queryByTestId('session-footer')).toBeNull() }
  const back = (ui: ReturnType<typeof render>) => ui.rerender(<TabContent activeTab={sessionTab} allTabs={both} />)
  beforeEach(() => {
    useTabStore.setState({ tabs: { [sessionTab.id]: sessionTab, [dashTab.id]: dashTab }, tabOrder: [sessionTab.id, dashTab.id], activeTabId: sessionTab.id, visitHistory: [] })
    registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: () => <div data-testid="other-tab" /> }] })
  })

  it('a message still in its 1.5 s buffer is sent although the tab is away, and is shown as sent on return', async () => {
    conv.value = ready({ status: 'running' })
    setView('deck')
    const ui = render(<TabContent activeTab={sessionTab} allTabs={both} />)
    type('mid-turn'); enter()
    await tick(500)
    away(ui)
    await tick(1000)
    expect(submits()).toHaveLength(1)
    expect(submits()[0].text).toBe('mid-turn')
    back(ui)
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'sent')
  })

  it('a handed message survives the switch and still de-duplicates on return: Claude Code\'s line is shown, the entry is not', async () => {
    conv.value = ready({ status: 'running' })
    setView('deck')
    const ui = render(<TabContent activeTab={sessionTab} allTabs={both} />)
    type('queued one'); enter()
    await tick(UNDO_MS)
    expect(submits()).toHaveLength(1)
    away(ui)
    conv.value = ready({ status: 'running', queued: [{ text: 'queued one', at: Date.now(), source: 'user' }] }) // the header changed while the pane was away
    back(ui)
    await tick(0)
    expect(screen.getAllByTestId('header-queued')).toHaveLength(1)
    expect(screen.queryByTestId('queued-message')).toBeNull()
    // the transcript echo (any id of the entry) settles it for good
    conv.value = ready({ status: 'running', items: [user('u0', 'earlier'), user('u1', 'queued one', { client_msg_id: submits()[0].client_msg_id })] })
    back(ui)
    await tick(0)
    expect(screen.queryByTestId('queued-message')).toBeNull()
  })

  it('a handed message with no line and no echo is shown again on return (not hidden by a stale match)', async () => {
    conv.value = ready({ status: 'running', queued: [] })
    setView('deck')
    const ui = render(<TabContent activeTab={sessionTab} allTabs={both} />)
    type('queued one'); enter()
    await tick(UNDO_MS)
    conv.value = ready({ status: 'running', queued: [{ text: 'queued one', at: Date.now(), source: 'user' }] })
    back(ui)
    await tick(0)
    expect(screen.queryByTestId('queued-message')).toBeNull()
    away(ui)
    conv.value = ready({ status: 'running', queued: [] })
    back(ui)
    await tick(0)
    expect(screen.getByTestId('queued-message')).toBeInTheDocument()
  })
})
