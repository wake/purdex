// spa/src/components/HandoffConfirmDialog.test.tsx — P-C.3b task 3: the
// confirm step in front of `handToNex`: busy state, one call per confirm,
// success / open-execution / error toasts, retry after an error.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act, waitFor } from '@testing-library/react'
import { HandoffConfirmDialog } from './HandoffConfirmDialog'
import { handToNex } from '../lib/nex/handoff'
import { HandoffApiError } from '../lib/nex/handoff-api'
import { useUndoToast } from '../stores/useUndoToast'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { createTab, type PaneRebuildRecord, type TmuxSessionContent } from '../types/tab'
import { collectLeaves, getPrimaryPane } from '../lib/pane-tree'
import { useHostStore } from '../stores/useHostStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { setHostShown } from '../lib/shown-hosts'

vi.mock('../lib/nex/handoff', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/nex/handoff')>()),
  handToNex: vi.fn(),
}))

const mockedHandToNex = vi.mocked(handToNex)

const args = { hostId: 'h1', sessionCode: 'zk16vd', tmuxInstance: 'inst-1', cachedName: 'purdex', tabId: 't1', paneId: 'p1' }
// Claude Code by the pane's rebuild record, so `agentTypes` can stay unclassified (the fromTitle tests need that).
const CC_RECORD: PaneRebuildRecord = { sessionName: 'purdex', tmuxInstance: 'inst-1', agent: { type: 'cc', updatedAt: 0 }, capturedAt: 0 }
/** The session `args` names, as a pane the handoff gate is open on. */
const liveSession = (over: Partial<TmuxSessionContent> = {}): TmuxSessionContent => ({
  kind: 'tmux-session', hostId: 'h1', sessionCode: 'zk16vd', mode: 'terminal', cachedName: 'purdex', tmuxInstance: 'inst-1', rebuild: CC_RECORD, ...over,
})
const readyNex = () => useNexHostStore.setState({
  byHost: {
    h1: {
      info: null,
      capabilities: { delegate: { resume_session_id: true }, sandbox_profiles: ['default', 'handoff'] } as never,
      phase: 'ready', error: null, fetchedAt: 0, generation: 1, fingerprint: 'f',
    },
  },
} as never)
const ok = { execution_id: 'exc_1', state: 'running', effective_profile: 'handoff', session_id: 'sid-1', cwd: '/w', session_kept: true }

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

function renderDialog() {
  const onClose = vi.fn()
  render(<HandoffConfirmDialog {...args} onClose={onClose} />)
  return { onClose }
}

const confirmBtn = () => screen.getByTestId('handoff-confirm') as HTMLButtonElement
const cancelBtn = () => screen.getByTestId('handoff-cancel') as HTMLButtonElement
const keepBox = () => screen.getByTestId('handoff-keep-session') as HTMLInputElement
const toast = () => useUndoToast.getState().toast

/** A tab whose primary pane shows the given session (or a different one). */
function sessionTab(hostId: string, sessionCode: string): string {
  const tab = createTab({ kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: 'x', tmuxInstance: 'inst-1' })
  useTabStore.getState().addTab(tab)
  return tab.id
}

beforeEach(() => {
  cleanup()
  mockedHandToNex.mockReset()
  useUndoToast.setState({ toast: null })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  // Confirm re-checks the live gate (P6 review A1): `args`' pane exists, holds the session, runs CC, and Nex is ready.
  useTabStore.getState().addTab({ id: 't1', pinned: false, locked: false, createdAt: 0, layout: { type: 'leaf', pane: { id: 'p1', content: liveSession() } } })
  useAgentStore.setState({ agentTypes: {} })
  readyNex()
  useShownHostsStore.setState({ ids: ['h1'] }) // the host is shown (H2d-3 re-checks it)
})
afterEach(() => vi.restoreAllMocks())

describe('HandoffConfirmDialog — rendering', () => {
  it('shows the localized title, body, Cancel and Confirm', () => {
    renderDialog()
    expect(screen.getByTestId('handoff-dialog')).toBeInTheDocument()
    expect(screen.getByText('Hand this session to nex?')).toBeInTheDocument()
    expect(screen.getByText(/continues headless under nex/)).toBeInTheDocument()
    expect(cancelBtn().textContent).toBe('Cancel')
    expect(confirmBtn().textContent).toContain('Hand to nex')
  })

  it('Cancel closes without calling handToNex', () => {
    const { onClose } = renderDialog()
    fireEvent.click(cancelBtn())
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(mockedHandToNex).not.toHaveBeenCalled()
  })

  it('Escape closes when idle', () => {
    const { onClose } = renderDialog()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})

describe('HandoffConfirmDialog — keep the tmux session (exec-to-terminal spec §4.3 / G4)', () => {
  it('shows the checkbox, labelled and checked by default', () => {
    renderDialog()
    expect(keepBox().checked).toBe(true)
    expect(screen.getByLabelText('Keep the tmux session')).toBe(keepBox())
  })

  it('unchecked → handToNex gets keepSession:false', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: { ...ok, session_kept: false }, swapped: true })
    renderDialog()
    fireEvent.click(keepBox())
    expect(keepBox().checked).toBe(false)
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(mockedHandToNex).toHaveBeenCalledWith({ ...args, keepSession: false })
  })

  it('is checked again on every open — never remembered (user ruling 2026-09-19)', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: { ...ok, session_kept: false }, swapped: true })
    const { onClose } = renderDialog()
    fireEvent.click(keepBox())
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(onClose).toHaveBeenCalledTimes(1)
    cleanup()
    renderDialog()
    expect(keepBox().checked).toBe(true)

    // Also after a plain cancel.
    fireEvent.click(keepBox())
    fireEvent.click(cancelBtn())
    cleanup()
    renderDialog()
    expect(keepBox().checked).toBe(true)
  })

  it('the checkbox is inert while busy', async () => {
    const d = deferred<{ result: typeof ok; swapped: boolean }>()
    mockedHandToNex.mockReturnValueOnce(d.promise)
    renderDialog()
    fireEvent.click(confirmBtn())
    expect(keepBox().disabled).toBe(true)
    await act(async () => { d.resolve({ result: ok, swapped: true }) })
  })

  it('names the other panes on this session (same host + code, this pane excluded); silent when there are none', () => {
    // This pane's own tab + two more tabs on the same session, one on another
    // session and one on the same code but another host.
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    const own = sessionTab(args.hostId, args.sessionCode)
    const ownPane = getPrimaryPane(useTabStore.getState().tabs[own].layout).id
    sessionTab(args.hostId, args.sessionCode)
    sessionTab(args.hostId, args.sessionCode)
    sessionTab(args.hostId, 'other1')
    sessionTab('h2', args.sessionCode)
    const onClose = vi.fn()
    render(<HandoffConfirmDialog {...args} tabId={own} paneId={ownPane} onClose={onClose} />)
    expect(screen.getByTestId('handoff-other-panes')).toHaveTextContent('2 other panes use this session')
    cleanup()

    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    const alone = sessionTab(args.hostId, args.sessionCode)
    const alonePane = getPrimaryPane(useTabStore.getState().tabs[alone].layout).id
    sessionTab(args.hostId, 'other1')
    render(<HandoffConfirmDialog {...args} tabId={alone} paneId={alonePane} onClose={onClose} />)
    expect(screen.queryByTestId('handoff-other-panes')).toBeNull()
  })

  it('swapped:false with session_kept:false → the "Open execution" action opens the execution WITHOUT `from`', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: { ...ok, session_kept: false }, swapped: false })
    const open = vi.spyOn(useTabStore.getState(), 'openSingletonTab').mockReturnValue('tab-x')
    renderDialog()
    fireEvent.click(keepBox())
    await act(async () => { fireEvent.click(confirmBtn()) })
    toast()!.action!()
    expect(open).toHaveBeenCalledWith({ kind: 'execution', executionId: 'exc_1', host: 'h1' })
    expect(open.mock.calls[0][0]).not.toHaveProperty('from')
  })
})

describe('HandoffConfirmDialog — confirm', () => {
  it('rapid double-click on Confirm → exactly one handToNex call, both buttons disabled while busy', async () => {
    const d = deferred<{ result: typeof ok; swapped: boolean }>()
    mockedHandToNex.mockReturnValueOnce(d.promise)
    const { onClose } = renderDialog()

    fireEvent.click(confirmBtn())
    fireEvent.click(confirmBtn())
    expect(mockedHandToNex).toHaveBeenCalledTimes(1)
    expect(mockedHandToNex).toHaveBeenCalledWith({ ...args, keepSession: true })
    expect(confirmBtn().disabled).toBe(true)
    expect(cancelBtn().disabled).toBe(true)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).not.toHaveBeenCalled()

    await act(async () => { d.resolve({ result: ok, swapped: true }) })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('success closes the dialog and toasts handoff.success with no action', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: ok, swapped: true })
    const { onClose } = renderDialog()
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(toast()?.message).toBe('Handed to nex.')
    expect(toast()?.action).toBeUndefined()
  })

  it('swapped:false closes and toasts with an "Open execution" action that opens the from-bearing execution', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: ok, swapped: false })
    const open = vi.spyOn(useTabStore.getState(), 'openSingletonTab').mockReturnValue('tab-x')
    const { onClose } = renderDialog()
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(toast()?.message).toBe('Handed to nex.')
    expect(toast()?.actionLabel).toBe('Open execution')
    expect(toast()?.action).toBeTypeOf('function')
    toast()!.action!()
    expect(open).toHaveBeenCalledWith({
      kind: 'execution',
      executionId: 'exc_1',
      host: 'h1',
      from: { sessionCode: 'zk16vd', tmuxInstance: 'inst-1', cachedName: 'purdex' },
    })
  })
})

describe('HandoffConfirmDialog — the initial view mode (shell cleanup spec §9.4)', () => {
  it('passes mode to handToNex', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: ok, swapped: true })
    render(<HandoffConfirmDialog {...args} mode="chat" onClose={vi.fn()} />)
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(mockedHandToNex).toHaveBeenCalledWith({ ...args, mode: 'chat', keepSession: true })
  })

  it('swapped:false with mode chat → "Open execution" opens the execution in chat', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: ok, swapped: false })
    const open = vi.spyOn(useTabStore.getState(), 'openSingletonTab').mockReturnValue('tab-x')
    render(<HandoffConfirmDialog {...args} mode="chat" onClose={vi.fn()} />)
    await act(async () => { fireEvent.click(confirmBtn()) })
    toast()!.action!()
    expect(open).toHaveBeenCalledWith({
      kind: 'execution',
      executionId: 'exc_1',
      host: 'h1',
      from: { sessionCode: 'zk16vd', tmuxInstance: 'inst-1', cachedName: 'purdex' },
      mode: 'chat',
    })
  })

  it('no mode → the recovery content has none (reads as room)', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: ok, swapped: false })
    const open = vi.spyOn(useTabStore.getState(), 'openSingletonTab').mockReturnValue('tab-x')
    renderDialog()
    await act(async () => { fireEvent.click(confirmBtn()) })
    toast()!.action!()
    expect(open.mock.calls[0][0]).not.toHaveProperty('mode')
  })
})

describe('HandoffConfirmDialog — records the session pane title (worker theme spec §8.4)', () => {
  function titledSessionTab(paneTitle: string) {
    const tab = createTab(liveSession())
    useTabStore.getState().addTab(tab)
    useSessionStore.setState({ sessions: { h1: [{ code: 'zk16vd', name: 'fix-login', pane_title: paneTitle }] as never } })
    return { tabId: tab.id, paneId: getPrimaryPane(tab.layout).id }
  }
  afterEach(() => {
    useSessionStore.setState({ sessions: {} })
    useAgentStore.setState({ agentTypes: {} })
    useUISettingsStore.setState({ dynamicTabName: false, stripAgentTitleMarker: true })
  })

  it('passes the session pane title to handToNex as fromTitle', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: ok, swapped: true })
    const ids = titledSessionTab('fix-login')
    render(<HandoffConfirmDialog {...args} {...ids} onClose={vi.fn()} />)
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(mockedHandToNex).toHaveBeenCalledWith({ ...args, ...ids, keepSession: true, fromTitle: 'fix-login' })
  })

  it('strips the agent title marker regardless of the dynamicTabName / stripAgentTitleMarker display settings', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: ok, swapped: true })
    const ids = titledSessionTab('✳ fix-login')
    useAgentStore.setState({ agentTypes: { 'h1:zk16vd': 'cc' } })
    useUISettingsStore.setState({ dynamicTabName: false, stripAgentTitleMarker: false })
    render(<HandoffConfirmDialog {...args} {...ids} onClose={vi.fn()} />)
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(mockedHandToNex.mock.calls[0][0].fromTitle).toBe('fix-login')
  })

  // (A gone tab no longer reaches handToNex at all: Confirm re-checks the live pane — P6 review A1.)
  it('no fromTitle when the session has no pane title', async () => {
    mockedHandToNex.mockResolvedValue({ result: ok, swapped: true })
    const tab = createTab(liveSession())
    useTabStore.getState().addTab(tab)
    useSessionStore.setState({ sessions: { h1: [{ code: 'zk16vd', name: 'fix-login' }] as never } })
    render(<HandoffConfirmDialog {...args} tabId={tab.id} paneId={getPrimaryPane(tab.layout).id} onClose={vi.fn()} />)
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(mockedHandToNex.mock.calls[0][0].fromTitle).toBeUndefined()
  })

  it('strips a known marker even when agentType is unclassified (review finding A2)', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: ok, swapped: true })
    const ids = titledSessionTab('✳ fix-login')
    // agentTypes stays {} (unclassified) — the typed strip would be a no-op.
    render(<HandoffConfirmDialog {...args} {...ids} onClose={vi.fn()} />)
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(mockedHandToNex.mock.calls[0][0].fromTitle).toBe('fix-login')
  })

  it('an ordinary title with no marker is unchanged when agentType is unclassified', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: ok, swapped: true })
    const ids = titledSessionTab('fix-login')
    render(<HandoffConfirmDialog {...args} {...ids} onClose={vi.fn()} />)
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(mockedHandToNex.mock.calls[0][0].fromTitle).toBe('fix-login')
  })

  it('records the pane title of the handed-off session even on a secondary pane of a split', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: ok, swapped: true })
    // The tab is unrelated to the handed-off session (a split whose primary
    // pane is something else); the lookup is by (hostId, sessionCode), not by tab.
    const tab = createTab({ kind: 'tmux-session', hostId: 'h1', sessionCode: 'other-code', mode: 'terminal', cachedName: 'other', tmuxInstance: 'inst-2' })
    useTabStore.getState().addTab(tab)
    const primaryId = getPrimaryPane(tab.layout).id
    useTabStore.getState().splitPane(tab.id, primaryId, 'h', liveSession())
    const secondId = collectLeaves(useTabStore.getState().tabs[tab.id].layout).find((p) => p.id !== primaryId)!.id
    useSessionStore.setState({ sessions: { h1: [{ code: 'zk16vd', name: 'fix-login', pane_title: 'fix-login' }] as never } })
    render(<HandoffConfirmDialog {...args} tabId={tab.id} paneId={secondId} onClose={vi.fn()} />)
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(mockedHandToNex.mock.calls[0][0].fromTitle).toBe('fix-login')
  })

  it('the swapped:false "Open execution" content carries it too', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: ok, swapped: false })
    const ids = titledSessionTab('fix-login')
    render(<HandoffConfirmDialog {...args} {...ids} onClose={vi.fn()} />)
    await act(async () => { fireEvent.click(confirmBtn()) })
    act(() => { toast()!.action!() })
    const opened = Object.values(useTabStore.getState().tabs)
      .map((tab) => getPrimaryPane(tab.layout).content)
      .find((c) => c.kind === 'execution')
    expect(opened).toMatchObject({ kind: 'execution', executionId: 'exc_1', fromTitle: 'fix-login' })
  })
})

// Host ownership H2d-3 T5 — the dialog re-checks the shown state at click, at completion and in the toast's opener.
describe('HandoffConfirmDialog — a host hidden in the workbench (H2d-3)', () => {
  beforeEach(() => {
    useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '1', port: 7860, order: 0 } }, hostOrder: ['h1'], activeHostId: null })
    useShownHostsStore.setState({ ids: ['h1'] })
  })

  it('hidden at click → the dialog closes, handToNex is not called', async () => {
    setHostShown('h1', false)
    const { onClose } = renderDialog()
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(mockedHandToNex).not.toHaveBeenCalled()
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('hidden during the flight → the toast has no "Open execution" action', async () => {
    const d = deferred<{ result: typeof ok; swapped: boolean }>()
    mockedHandToNex.mockReturnValueOnce(d.promise)
    const { onClose } = renderDialog()
    fireEvent.click(confirmBtn())
    setHostShown('h1', false)
    await act(async () => { d.resolve({ result: ok, swapped: false }) })
    expect(onClose).toHaveBeenCalledTimes(1)
    expect(toast()?.message).toBe('Handed to nex.')
    expect(toast()?.action).toBeUndefined()
  })

  it('shown at completion, hidden before the action is clicked → the click opens the Hosts page on that host, no execution tab', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: ok, swapped: false })
    renderDialog()
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(toast()?.action).toBeTypeOf('function')
    const open = vi.spyOn(useTabStore.getState(), 'openSingletonTab')
    setHostShown('h1', false)
    toast()!.action!()
    expect(open.mock.calls.map((c) => c[0].kind)).toEqual(['hosts'])
    expect(useHostStore.getState().activeHostId).toBe('h1')
  })
})

// P6 review A1 — the gate is re-read from the stores at the click, not taken from the last render: the host's close
// is an effect after a render, so a click can land between the store change and that close. Blocked → the dialog
// closes and nothing is sent. Most of these stores are ones the dialog does not even subscribe to (no re-render).
describe('HandoffConfirmDialog — re-checks the live gate at Confirm (P6 review A1)', () => {
  async function confirmAfter(change: () => void) {
    const r = renderDialog()
    change()
    await act(async () => { fireEvent.click(confirmBtn()) })
    return r
  }
  const setPane = (content: Parameters<ReturnType<typeof useTabStore.getState>['setPaneContent']>[2]) =>
    useTabStore.getState().setPaneContent('t1', 'p1', content)

  it('open gate → handToNex is called (control)', async () => {
    mockedHandToNex.mockResolvedValueOnce({ result: ok, swapped: true })
    const { onClose } = await confirmAfter(() => {})
    expect(mockedHandToNex).toHaveBeenCalledTimes(1)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('another agent now runs in the session (the live type outranks the record) → not sent, closed', async () => {
    const { onClose } = await confirmAfter(() => { useAgentStore.setState({ agentTypes: { 'h1:zk16vd': 'codex' } }) })
    expect(mockedHandToNex).not.toHaveBeenCalled()
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('Claude Code exited and nothing else says it runs (no record) → not sent, closed', async () => {
    useAgentStore.setState({ agentTypes: { 'h1:zk16vd': 'cc' } })
    setPane(liveSession({ rebuild: undefined }))
    const { onClose } = await confirmAfter(() => { useAgentStore.getState().clearSession('h1', 'zk16vd') })
    expect(mockedHandToNex).not.toHaveBeenCalled()
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  // P6 re-review: the pane's rebuild record still says cc after the live type is cleared; the real SessionEnd also
  // marks it exited, and an exited record no longer opens the gate.
  it('Claude Code exited through the real SessionEnd path while the record says cc → not sent, closed', async () => {
    useAgentStore.setState({ agentTypes: { 'h1:zk16vd': 'cc' } })
    setPane(liveSession({ rebuild: { ...CC_RECORD, agent: { type: 'cc', sessionId: 'S1', frameId: 'F1', updatedAt: 0 } } }))
    const { onClose } = await confirmAfter(() => {
      useAgentStore.getState().handleNormalizedEvent('h1', 'zk16vd', {
        agent_type: 'cc', status: 'clear', raw_event_name: 'PdxSessionEnd', broadcast_ts: 1, subagents: [],
        detail: { pdx_exit: { agent_type: 'cc', session_id: 'S1', tmux_pane_id: '%1', tmux_instance: 'inst-1', frame_id: 'F1', reason: 'session-end', at: 7_000 } },
      })
    })
    expect(mockedHandToNex).not.toHaveBeenCalled()
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('the pane\'s session is terminated → not sent, closed', async () => {
    const { onClose } = await confirmAfter(() => { setPane(liveSession({ terminated: 'session-closed' })) })
    expect(mockedHandToNex).not.toHaveBeenCalled()
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('Nex is no longer ready on the host → not sent, closed', async () => {
    const { onClose } = await confirmAfter(() => {
      useNexHostStore.setState((s) => ({ byHost: { ...s.byHost, h1: { ...s.byHost.h1, phase: 'unavailable' } } }) as never)
    })
    expect(mockedHandToNex).not.toHaveBeenCalled()
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('the pane now holds another tmux process of the session → not sent, closed', async () => {
    const { onClose } = await confirmAfter(() => { setPane(liveSession({ tmuxInstance: 'inst-2' })) })
    expect(mockedHandToNex).not.toHaveBeenCalled()
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('the pane is gone → not sent, closed', async () => {
    const { onClose } = await confirmAfter(() => { useTabStore.getState().closeTab('t1') })
    expect(mockedHandToNex).not.toHaveBeenCalled()
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})

describe('HandoffConfirmDialog — errors keep the dialog open', () => {
  async function failWith(err: HandoffApiError) {
    mockedHandToNex.mockRejectedValueOnce(err)
    const r = renderDialog()
    await act(async () => { fireEvent.click(confirmBtn()) })
    return r
  }

  it('no_cc → error toast, dialog stays, buttons re-enabled', async () => {
    const { onClose } = await failWith(new HandoffApiError(409, 'no_cc', { error: 'x', code: 'no_cc' }))
    expect(toast()?.message).toBe('No Claude Code session is running in this pane.')
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByTestId('handoff-dialog')).toBeInTheDocument()
    await waitFor(() => expect(confirmBtn().disabled).toBe(false))
    expect(cancelBtn().disabled).toBe(false)
  })

  it('delegate_rejected rolled back → reason + restored; no manual-resume line even when session_id is present (CC is already back)', async () => {
    await failWith(new HandoffApiError(409, 'delegate_rejected', { reject_reason: 'quota', rolled_back: true }))
    expect(toast()?.message).toBe('nex rejected the handoff (quota); the terminal was restored.')
    cleanup()
    await failWith(new HandoffApiError(409, 'delegate_rejected', { reject_reason: 'quota', rolled_back: true, session_id: 'sid-9' }))
    expect(toast()?.message).toBe('nex rejected the handoff (quota); the terminal was restored.')
  })

  it('delegate_rejected NOT rolled back → second line with the manual resume command', async () => {
    await failWith(new HandoffApiError(409, 'delegate_rejected', { reject_reason: 'quota', rolled_back: false, session_id: 'sid-9' }))
    expect(toast()?.message).toBe(
      'nex rejected the handoff (quota); the terminal was not restored.\nResume by hand: claude --resume sid-9',
    )
  })

  it('cc_exit_timeout → step in the message; no manual-resume line (the code never carries session_id)', async () => {
    await failWith(new HandoffApiError(504, 'cc_exit_timeout', { step: 'exit_confirm', session_id: 'sid-9' }))
    expect(toast()?.message).toBe('Claude Code did not exit in time (step: exit_confirm).')
  })

  it('tmux_instance_mismatch with session_id → manual-resume line', async () => {
    await failWith(new HandoffApiError(409, 'tmux_instance_mismatch', { session_id: 'sid-7' }))
    expect(toast()?.message).toBe(
      'The tmux session was replaced since this pane opened; reopen it and try again.\nResume by hand: claude --resume sid-7',
    )
  })

  it('a second Confirm after an error retries (new handToNex call)', async () => {
    const { onClose } = await failWith(new HandoffApiError(503, 'nex_unavailable', {}))
    mockedHandToNex.mockResolvedValueOnce({ result: ok, swapped: true })
    await waitFor(() => expect(confirmBtn().disabled).toBe(false))
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(mockedHandToNex).toHaveBeenCalledTimes(2)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('a non-API error falls back to the generic message and keeps the dialog open', async () => {
    mockedHandToNex.mockRejectedValueOnce(new TypeError('boom'))
    const { onClose } = renderDialog()
    await act(async () => { fireEvent.click(confirmBtn()) })
    expect(toast()?.message).toBe('Handoff failed (unknown).')
    expect(onClose).not.toHaveBeenCalled()
  })
})
