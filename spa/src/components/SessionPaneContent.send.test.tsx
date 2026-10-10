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
import { clearAllSendQueues } from '../lib/conversations/send-queue'
import type { Capabilities, ConversationItem } from '../lib/conversations/types'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
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
const submits = () => fetchMock.mock.calls.filter((c) => String(c[1]).endsWith('/submit')).map((c) => ({ path: String(c[1]), host: c[0] as string, ...(JSON.parse(c[2].body) as { text: string; client_msg_id: string }) }))
const tick = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms))

const user = (id: string, text: string, extra: object = {}): ConversationItem => ({ type: 'user', id, at: Date.now(), index: 0, text, source: 'user', ...extra }) as ConversationItem
function ready(over: { status?: string; capabilities?: Capabilities | null; items?: ConversationItem[]; sessionId?: string } = {}): PaneConversation {
  const items = over.items ?? [user('u0', 'earlier')]
  return {
    state: 'ready', hostId: H, sessionId: over.sessionId ?? SID,
    entry: {
      doc: {
        ...emptyDoc(),
        turns: [{ id: 't0', index: 0, started_at: 1, outcome: 'done', items }],
        header: { title: 'x', status: over.status ?? 'idle', backend: 'terminal', live: true },
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
  fetchMock.mockImplementation(() => answer(200, { status: 'accepted' }))
  clearAllDrafts(); clearAllSendQueues()
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
  it('sends to the CONVERSATION\'s session on the pane\'s host, then the transcript echo (client_msg_id) settles the queued message', async () => {
    setView(view)
    const ui = mount()
    type('hello agent'); enter()
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'undo')
    await tick(3000)
    expect(submits()).toHaveLength(1)
    expect(submits()[0]).toMatchObject({ host: H, text: 'hello agent', path: `/api/conversations/claude/${SID}/submit` })
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'sent')
    // the transcript brings the user item for it, carrying the id the App chose
    conv.value = ready({ items: [user('u0', 'earlier'), user('u1', 'hello agent', { client_msg_id: submits()[0].client_msg_id })] })
    remount(ui)
    await tick(0)
    expect(screen.queryByTestId('queued-message')).toBeNull()
    expect(submits()).toHaveLength(1)
  })

  it('busy: waits while the header is not idle, and is resent ONCE (same id) when the header turns idle', async () => {
    fetchMock.mockImplementationOnce(() => answer(200, { status: 'busy' }))
    conv.value = ready({ status: 'running' })
    setView(view)
    const ui = mount()
    type('after you'); enter()
    await tick(3000)
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'waiting')
    expect(submits()).toHaveLength(1)
    conv.value = ready({ status: 'idle' })
    remount(ui)
    await tick(3000)
    expect(submits()).toHaveLength(2)
    expect(submits()[1]).toEqual(submits()[0])
    // later renders (still idle) do not send it again
    remount(ui)
    await tick(5000)
    expect(submits()).toHaveLength(2)
  })

  it('a conversation without a send mod shows the disabled input with the way to the terminal; no request is made', async () => {
    conv.value = ready({ capabilities: {} })
    setView(view)
    mount()
    expect(screen.getByTestId('session-input-disabled')).toBeInTheDocument()
    expect(screen.queryByRole('textbox')).toBeNull()
    fireEvent.click(screen.getByText('Switch to terminal'))
    expect(screen.queryByTestId('session-input')).toBeNull() // switched to the terminal view
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('409 no_mod fails the message (it stays visible as failed) instead of disabling the box by itself', async () => {
    fetchMock.mockImplementationOnce(() => answer(409, { error: 'no_mod' }))
    setView(view)
    mount()
    type('nobody home'); enter()
    await tick(3000)
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'failed')
    expect(screen.getByRole('textbox')).toBeInTheDocument()
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
    await tick(3000)
    expect(submits()[0].path).toBe('/api/conversations/claude/bbbbbbbb-1111-4222-8333-444444444444/submit')
  })
})

describe('the queue is driven by the pane, not by the input (every view)', () => {
  it('sent from the deck, then the terminal view: busy waits, and the idle header resends once with the same id although no input is mounted', async () => {
    fetchMock.mockImplementationOnce(() => answer(200, { status: 'busy' }))
    conv.value = ready({ status: 'running' })
    setView('deck')
    const ui = mount()
    type('after you'); enter()
    setView('terminal')
    expect(screen.queryByRole('textbox')).toBeNull() // no SessionInput under the terminal
    await tick(3000)
    expect(submits()).toHaveLength(1)
    conv.value = ready({ status: 'idle' })
    remount(ui)
    await tick(3000)
    expect(submits()).toHaveLength(2)
    expect(submits()[1]).toEqual(submits()[0])
    remount(ui)
    await tick(5000)
    expect(submits()).toHaveLength(2)
    // back in the deck the message is shown as sent, not stuck
    setView('deck')
    expect(screen.getByTestId('queued-message')).toHaveAttribute('data-state', 'sent')
  })

  it('the transcript echo is matched while the terminal view is up (nothing stays queued behind it)', async () => {
    conv.value = ready()
    setView('deck')
    const ui = mount()
    type('hello'); enter()
    setView('terminal')
    await tick(3000)
    conv.value = ready({ items: [user('u0', 'earlier'), user('u1', 'hello', { client_msg_id: submits()[0].client_msg_id })] })
    remount(ui)
    await tick(0)
    setView('deck')
    expect(screen.queryByTestId('queued-message')).toBeNull()
  })
})
