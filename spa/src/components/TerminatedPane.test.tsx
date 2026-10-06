import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor, act } from '@testing-library/react'
import { TerminatedPane } from './TerminatedPane'
import { useTabStore } from '../stores/useTabStore'
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { useRebuildStore } from '../stores/useRebuildStore'
import { rebuildPane } from '../lib/rebuild/engine'
import { findPane } from '../lib/pane-tree'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useUndoToast } from '../stores/useUndoToast'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { rebuildAsWorker } from '../lib/nex/worker-rebuild'
import { HandoffApiError } from '../lib/nex/handoff-api'
import type { NexCapabilities } from '../lib/nex/types'
import type { ConversationRow } from '../lib/nex/conversations-api'
import { conversationRebuildContent, openConversationRebuild } from '../lib/nex/open-conversation-rebuild'
import type { PaneContent, Tab, PaneRebuildRecord } from '../types/tab'

vi.mock('../lib/nex/worker-rebuild', async (o) => ({ ...(await o<typeof import('../lib/nex/worker-rebuild')>()), rebuildAsWorker: vi.fn() }))

vi.mock('./SessionPickerList', () => ({
  SessionPickerList: ({ onSelect }: { onSelect: (sel: unknown) => void }) => (
    <button
      data-testid="session-picker"
      onClick={() =>
        onSelect({
          hostId: 'new-host',
          sessionCode: 'new001',
          cachedName: 'new-session',
          tmuxInstance: 'tmux:inst',
        })
      }
    >
      Mock SessionPickerList
    </button>
  ),
}))

const TAB_ID = 'tab-1'
const PANE_ID = 'pane-1'

function makeContent(reason: 'session-closed' | 'tmux-restarted' | 'host-removed', mode: 'terminal' = 'terminal'): Extract<PaneContent, { kind: 'tmux-session' }> {
  return {
    kind: 'tmux-session',
    hostId: 'host-1',
    sessionCode: 'dev001',
    mode,
    cachedName: 'my-session',
    tmuxInstance: '123:456',
    terminated: reason,
  }
}

function setupTab(content: PaneContent) {
  const tab: Tab = {
    id: TAB_ID,
    pinned: false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane: { id: PANE_ID, content } },
  }
  useTabStore.setState({
    tabs: { [TAB_ID]: tab },
    tabOrder: [TAB_ID],
    activeTabId: TAB_ID,
  })
}

beforeEach(() => {
  cleanup()
  useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {}, activeHostId: null })
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  useRebuildStore.setState({ operations: {}, lockedBy: null })
})

describe('TerminatedPane', () => {
  it('shows session-closed message', () => {
    const content = makeContent('session-closed')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.getByText('Session closed')).toBeInTheDocument()
    expect(screen.getByText('my-session no longer exists')).toBeInTheDocument()
  })

  it('shows tmux-restarted message', () => {
    const content = makeContent('tmux-restarted')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.getByText('tmux restarted')).toBeInTheDocument()
    expect(screen.getByText('Previous sessions are no longer valid')).toBeInTheDocument()
  })

  it('shows host-removed message', () => {
    const content = makeContent('host-removed')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.getByText('Host removed')).toBeInTheDocument()
    expect(screen.getByText('This host has been removed')).toBeInTheDocument()
  })

  it('has a close tab button that calls closeTab', () => {
    const content = makeContent('session-closed')
    setupTab(content)
    useWorkspaceStore.getState().reset()
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, TAB_ID)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)

    const closeBtn = screen.getByText('Close tab')
    expect(closeBtn).toBeInTheDocument()

    fireEvent.click(closeBtn)
    // Tab should be closed
    expect(useTabStore.getState().tabs[TAB_ID]).toBeUndefined()
  })

  it('session selection calls setPaneContent with correct data, as a terminal pane', () => {
    const content = makeContent('session-closed')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)

    fireEvent.click(screen.getByTestId('session-picker'))

    const updatedTab = useTabStore.getState().tabs[TAB_ID]
    expect(updatedTab).toBeDefined()
    if (updatedTab.layout.type === 'leaf') {
      const newContent = updatedTab.layout.pane.content
      expect(newContent).toEqual({
        kind: 'tmux-session',
        hostId: 'new-host',
        sessionCode: 'new001',
        mode: 'terminal',
        cachedName: 'new-session',
        tmuxInstance: 'tmux:inst',
      })
    }
  })

  it('session selection for terminal mode tab preserves terminal mode', () => {
    const content = makeContent('session-closed', 'terminal')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)

    fireEvent.click(screen.getByTestId('session-picker'))

    const updatedTab = useTabStore.getState().tabs[TAB_ID]
    if (updatedTab.layout.type === 'leaf') {
      const newContent = updatedTab.layout.pane.content
      expect(newContent.kind).toBe('tmux-session')
      if (newContent.kind === 'tmux-session') {
        expect(newContent.mode).toBe('terminal')
      }
    }
  })

  it('renders the SessionPickerList', () => {
    const content = makeContent('session-closed')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.getByTestId('session-picker')).toBeInTheDocument()
  })

  // Plain `justify-center` in a scrolling column pushes overflow above the
  // scroll origin, where it can never be scrolled back into view.
  it('centres its content without clipping the top when it overflows', () => {
    const content = makeContent('session-closed')
    setupTab(content)
    const { container } = render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    const root = container.firstElementChild as HTMLElement
    expect(root).toHaveClass('overflow-y-auto', 'justify-center-safe')
    expect(root).not.toHaveClass('justify-center')
  })
})

describe('TerminatedPane rebuild action set', () => {
  function setupSplitTab(contents: Array<{ id: string; content: PaneContent }>) {
    const tab: Tab = {
      id: TAB_ID,
      pinned: false,
      locked: false,
      createdAt: Date.now(),
      layout: {
        type: 'split',
        id: 'split-1',
        direction: 'h',
        sizes: [50, 50],
        children: contents.map(({ id, content }) => ({ type: 'leaf' as const, pane: { id, content } })),
      },
    }
    useTabStore.setState({ tabs: { [TAB_ID]: tab }, tabOrder: [TAB_ID], activeTabId: TAB_ID })
  }

  function paneRebuild(paneId: string) {
    const layout = useTabStore.getState().tabs[TAB_ID].layout
    const pane = findPane(layout, paneId)
    const c = pane?.content
    return c && c.kind === 'tmux-session' ? c.rebuild : undefined
  }

  it('renders the action set on a terminal pane, seeded from the cached name', () => {
    const content = makeContent('tmux-restarted')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.getByTestId('rebuild-action-set')).toBeInTheDocument()
    expect(screen.getByTestId('rebuild-session-name-cell')).toHaveTextContent('my-session')
    // The pane is dead, so the create row is locked on.
    const create = screen.getByRole('checkbox', { name: 'Create tmux session' })
    expect(create).toBeChecked()
    expect(create).toBeDisabled()
  })

  it('hides Rebuild when the host is gone', () => {
    const content = makeContent('host-removed')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.queryByRole('button', { name: 'Rebuild' })).toBeNull()
    expect(screen.getByTestId('rebuild-host-removed-hint')).toBeInTheDocument()
  })

  // A rebuild that is refused before it starts never reaches `beginOperation`,
  // so nothing but the returned report knows why. The panel has to say so
  // rather than silently restoring its button.
  it('reports a refusal that happened before the operation started', async () => {
    const content = makeContent('tmux-restarted')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    // No host is configured in this suite, so `pinHost` refuses immediately.
    fireEvent.click(screen.getByRole('button', { name: 'Rebuild' }))
    expect(await screen.findByTestId('rebuild-error-create')).toHaveTextContent(/not configured/)
  })

  it('an edit lands on this pane only, not on its split sibling', () => {
    const rebuild = {
      sessionName: 'my-session',
      tmuxInstance: '123:456',
      cwd: '/w/p',
      resumeCommandOverride: 'claude --resume S1',
      capturedAt: 1,
    }
    const content = { ...makeContent('tmux-restarted'), rebuild }
    const sibling = { ...makeContent('tmux-restarted'), rebuild: { ...rebuild } }
    setupSplitTab([{ id: PANE_ID, content }, { id: 'pane-2', content: sibling }])
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)

    fireEvent.doubleClick(screen.getByTestId('rebuild-cwd-cell'))
    const input = screen.getByTestId('rebuild-cwd-input')
    fireEvent.change(input, { target: { value: '/w/edited' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    expect(paneRebuild(PANE_ID)?.cwd).toBe('/w/edited')
    expect(paneRebuild('pane-2')?.cwd).toBe('/w/p')
  })
})

// A finished operation belongs to the rebuild cycle it started from. When the
// pane dies again under a NEW binding, the panel must start clean — otherwise
// every field stays frozen on the previous cycle's created session and neither
// Rebuild nor Retry nor Attach is reachable (only an app reload clears it).
describe('TerminatedPane rebuild operation scope', () => {
  const previousCycle = {
    paneId: PANE_ID,
    tabId: TAB_ID,
    hostId: 'host-1',
    plan: { createSession: true, applyCwd: true, runResume: true },
    binding: { hostId: 'host-1', sessionCode: 'dev001', tmuxInstance: '123:456' },
    resumeCommand: 'claude --resume S1',
    createdSession: {
      code: 'new001', name: 'my-session-2', cwd: '/w/p', mode: 'terminal',
      tmux_instance: '222:2000',
    },
    status: 'done' as const,
    report: {
      hostId: 'host-1',
      created: { code: 'new001', name: 'my-session-2', tmuxInstance: '222:2000' },
      steps: {
        create: { status: 'ok' as const },
        resume: { status: 'ok' as const },
        repoint: { status: 'ok' as const },
      },
      repointed: true,
    },
    startedAt: 1,
  }

  it('offers Rebuild again when the pane died under a new binding', () => {
    useRebuildStore.setState({ operations: { [PANE_ID]: previousCycle }, lockedBy: null })
    // The pane now sits on the session that rebuild created, and that one died.
    const content = {
      ...makeContent('tmux-restarted'),
      sessionCode: 'new001',
      tmuxInstance: '222:2000',
      rebuild: { sessionName: 'my-session-2', tmuxInstance: '222:2000', cwd: '/w/p', capturedAt: 2 },
    }
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)

    expect(screen.getByRole('button', { name: 'Rebuild' })).toBeEnabled()
    expect(screen.queryByTestId('rebuild-created-name')).toBeNull()
    // The rows describe nothing that already happened, so they stay editable.
    expect(screen.getByRole('checkbox', { name: 'Working directory' })).toBeEnabled()
  })

  it('still shows the operation that belongs to the pane binding it started from', () => {
    useRebuildStore.setState({
      operations: {
        [PANE_ID]: {
          ...previousCycle,
          report: {
            ...previousCycle.report,
            steps: {
              create: { status: 'ok' as const },
              resume: { status: 'failed' as const, error: 'send-keys failed: 500' },
              repoint: { status: 'skipped' as const },
            },
            repointed: false,
          },
        },
      },
      lockedBy: null,
    })
    const content = makeContent('tmux-restarted')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)

    expect(screen.getByRole('button', { name: 'Retry resume' })).toBeEnabled()
    expect(screen.getByTestId('rebuild-created-name')).toHaveTextContent('my-session-2')
  })
})

describe('TerminatedPane rebuild as worker', () => {
  const H = 'host-1'
  const caps = {
    phase: 'ga', host_id: H, verbs: [], providers: ['claude'], events: [], provider_events: [], transient_events: [],
    sandbox_profiles: ['default', 'handoff'], sandbox_default_profile: 'default', sandbox_max_profile: 'handoff',
    roots: [], send: { delivery: ['text'], max_text_bytes: 1 }, delegate: { resume_session_id: true },
  } as unknown as NexCapabilities
  const readyEntry = { info: null, capabilities: caps, phase: 'ready', error: null, fetchedAt: 0, generation: 1, fingerprint: 'f' }
  const fullRecord: PaneRebuildRecord = { sessionName: 'p1', tmuxInstance: 'i', cwd: '/w', agent: { type: 'cc', sessionId: 'S', updatedAt: 1 }, capturedAt: 1 }
  const refetch = vi.fn()
  const ensure = vi.fn().mockResolvedValue(undefined)

  function renderTerminated(rebuild: PaneRebuildRecord) {
    const content = { ...makeContent('session-closed'), rebuild }
    setupTab(content)
    return render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
  }

  beforeEach(() => {
    refetch.mockReset()
    ensure.mockClear()
    useExecutionListStore.setState({ refetch } as never)
    useUndoToast.setState({ toast: null, notice: null })
    useNexHostStore.setState({ byHost: { [H]: readyEntry }, ensure } as never)
    vi.mocked(rebuildAsWorker).mockReset().mockResolvedValue({ result: { execution_id: 'n', state: 'running' }, swapped: true })
  })

  it('offers worker rebuild when nex is ready and the record knows the session', async () => {
    renderTerminated(fullRecord)
    expect(screen.getByTestId('rebuild-mode-terminal')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('session-picker')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('rebuild-mode-worker'))
    expect(screen.queryByTestId('session-picker')).toBeNull()
    expect(screen.getByText('/w')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('terminated-rebuild-worker'))
    await waitFor(() => expect(rebuildAsWorker).toHaveBeenCalledWith(expect.objectContaining({ hostId: H, sessionId: 'S', cwd: '/w', tabId: TAB_ID, paneId: PANE_ID })))
    const { expect: guard } = vi.mocked(rebuildAsWorker).mock.calls[0][0]
    expect(guard(makeContent('session-closed'))).toBe(true)
    expect(guard({ ...makeContent('session-closed'), sessionCode: 'other' })).toBe(false)
  })

  it('shows no choice without a session id, a cwd, or nex', () => {
    renderTerminated({ sessionName: 'p1', tmuxInstance: 'i', capturedAt: 1 })
    expect(screen.queryByTestId('rebuild-mode-worker')).toBeNull()
    cleanup()
    renderTerminated({ ...fullRecord, cwd: undefined })
    expect(screen.queryByTestId('rebuild-mode-worker')).toBeNull()
    cleanup()
    useNexHostStore.setState({ byHost: {} } as never)
    renderTerminated(fullRecord)
    expect(screen.queryByTestId('rebuild-mode-worker')).toBeNull()
  })

  it('shows an owner refusal inline', async () => {
    vi.mocked(rebuildAsWorker).mockRejectedValue(new HandoffApiError(409, 'session_owned', { owner: 'worker' }))
    renderTerminated(fullRecord)
    fireEvent.click(screen.getByTestId('rebuild-mode-worker'))
    fireEvent.click(screen.getByTestId('terminated-rebuild-worker'))
    expect(await screen.findByTestId('terminated-rebuild-error')).toHaveTextContent('already has a worker in progress')
  })

  it('swapped:false toasts and refetches through the shared helper', async () => {
    vi.mocked(rebuildAsWorker).mockResolvedValue({ result: { execution_id: 'n', state: 'running' }, swapped: false })
    renderTerminated(fullRecord)
    fireEvent.click(screen.getByTestId('rebuild-mode-worker'))
    fireEvent.click(screen.getByTestId('terminated-rebuild-worker'))
    await waitFor(() => expect(useUndoToast.getState().toast?.message).toMatch(/Rebuilt as a new worker, but the original tab/))
    expect(refetch).toHaveBeenCalledWith(H)
  })

  it('ensures nex readiness for the host on mount', () => {
    renderTerminated(fullRecord)
    expect(ensure).toHaveBeenCalledWith(H)
  })

  it('a mode toggle clears a stale rebuild error', async () => {
    vi.mocked(rebuildAsWorker).mockRejectedValue(new HandoffApiError(409, 'session_owned', { owner: 'worker' }))
    renderTerminated(fullRecord)
    fireEvent.click(screen.getByTestId('rebuild-mode-worker'))
    fireEvent.click(screen.getByTestId('terminated-rebuild-worker'))
    await screen.findByTestId('terminated-rebuild-error')
    fireEvent.click(screen.getByTestId('rebuild-mode-terminal'))
    fireEvent.click(screen.getByTestId('rebuild-mode-worker'))
    expect(screen.queryByTestId('terminated-rebuild-error')).toBeNull()
  })

  it('a failing readiness check is handled: the terminal screen stays in charge', async () => {
    // jsdom's types leave out Node's process; the rejection would surface there.
    const proc = (globalThis as unknown as { process: { on(e: string, f: () => void): void; off(e: string, f: () => void): void } }).process
    const unhandled = vi.fn()
    proc.on('unhandledRejection', unhandled)
    try {
      // A plain function, not vi.fn: a mock's own settledResults bookkeeping would handle the rejection.
      let asked = 0
      useNexHostStore.setState({ byHost: {}, ensure: () => { asked++; return Promise.reject(new Error('down')) } } as never)
      renderTerminated(fullRecord)
      expect(asked).toBe(1)
      await act(async () => { await new Promise((r) => setTimeout(r, 0)) })
      expect(unhandled).not.toHaveBeenCalled()
      expect(screen.queryByTestId('rebuild-mode-worker')).toBeNull()
      expect(screen.getByTestId('session-picker')).toBeInTheDocument()
    } finally {
      proc.off('unhandledRejection', unhandled)
    }
  })

  describe('one operation lock per pane', () => {
    const lockOwner = `rebuild:${PANE_ID}`
    it('a terminal rebuild in progress disables the choice and the worker button, and sends nothing', () => {
      renderTerminated(fullRecord)
      fireEvent.click(screen.getByTestId('rebuild-mode-worker'))
      let grant: unknown
      act(() => { grant = useRebuildStore.getState().acquireOperationLock(lockOwner) })
      expect(screen.getByTestId('rebuild-mode-terminal')).toBeDisabled()
      expect(screen.getByTestId('rebuild-mode-worker')).toBeDisabled()
      expect(screen.getByTestId('terminated-rebuild-worker')).toBeDisabled()
      fireEvent.click(screen.getByTestId('terminated-rebuild-worker'))
      expect(rebuildAsWorker).not.toHaveBeenCalled()
      // settles: controls re-enable
      act(() => { useRebuildStore.getState().releaseOperationLock(grant as never) })
      expect(screen.getByTestId('rebuild-mode-terminal')).toBeEnabled()
      expect(screen.getByTestId('terminated-rebuild-worker')).toBeEnabled()
    })

    it('a lock taken between render and click refuses the worker request', async () => {
      renderTerminated(fullRecord)
      fireEvent.click(screen.getByTestId('rebuild-mode-worker'))
      const btn = screen.getByTestId('terminated-rebuild-worker')
      useRebuildStore.getState().acquireOperationLock('other')
      fireEvent.click(btn)
      await new Promise((r) => setTimeout(r, 0))
      expect(rebuildAsWorker).not.toHaveBeenCalled()
    })

    it('a worker rebuild in progress holds the pane lock: choice disabled, rebuildPane refuses, then re-enables', async () => {
      let done: (v: { result: { execution_id: string; state: string }; swapped: boolean }) => void = () => {}
      vi.mocked(rebuildAsWorker).mockReturnValue(new Promise((r) => { done = r as never }))
      renderTerminated(fullRecord)
      fireEvent.click(screen.getByTestId('rebuild-mode-worker'))
      fireEvent.click(screen.getByTestId('terminated-rebuild-worker'))
      await waitFor(() => expect(rebuildAsWorker).toHaveBeenCalledTimes(1))
      expect(useRebuildStore.getState().lockedBy).toBe(lockOwner)
      expect(screen.getByTestId('rebuild-mode-terminal')).toBeDisabled()
      expect(screen.getByTestId('terminated-rebuild-worker')).toBeDisabled()
      const report = await rebuildPane('host-1', TAB_ID, PANE_ID, { createSession: true, applyCwd: false, runResume: false })
      expect(report.steps.create.status).toBe('failed')
      await act(async () => { done({ result: { execution_id: 'n', state: 'running' }, swapped: true }) })
      await waitFor(() => expect(useRebuildStore.getState().lockedBy).toBeNull())
      expect(screen.getByTestId('rebuild-mode-terminal')).toBeEnabled()
      expect(screen.getByTestId('terminated-rebuild-worker')).toBeEnabled()
    })
  })
})

// The rebuild tab of an 已退出 conversation (conversation entity spec §13.4): R-4-3 copy, R-4-5 preselection of
// the mode it was last in, and R-4-4's recently-written notice in both modes, counting live until 120 s have passed.
describe('TerminatedPane — a conversation-ended pane', () => {
  const H = 'host-1'
  const S = 'aaaaaaaa-1111-2222-3333-444444444444'
  const NOW = 1_800_000_000_000
  const caps = {
    phase: 'ga', host_id: H, verbs: [], providers: ['claude'], events: [], provider_events: [], transient_events: [],
    sandbox_profiles: ['default', 'handoff'], sandbox_default_profile: 'default', sandbox_max_profile: 'handoff',
    roots: [], send: { delivery: ['text'], max_text_bytes: 1 }, delegate: { resume_session_id: true },
  } as unknown as NexCapabilities
  const readyEntry = { info: null, capabilities: caps, phase: 'ready', error: null, fetchedAt: 0, generation: 1, fingerprint: 'f' }
  const ensure = vi.fn().mockResolvedValue(undefined)

  const row = (over: Partial<ConversationRow> = {}): ConversationRow => ({
    session_id: S, title: 'Fix the login bug', title_source: 'ai', cwd: '/w/proj', cwd_exists: true,
    last_activity_at: NOW - 600_000, last_in: 'terminal', ...over,
  })

  function renderConversation(over: Partial<ConversationRow> = {}) {
    const content = conversationRebuildContent(H, row(over), 'proj-2', '111:1000', 1)
    setupTab(content)
    return render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
  }

  beforeEach(() => {
    ensure.mockClear()
    useNexHostStore.setState({ byHost: { [H]: readyEntry }, ensure } as never)
    vi.mocked(rebuildAsWorker).mockReset().mockResolvedValue({ result: { execution_id: 'n', state: 'running' }, swapped: true })
  })

  // Spies on the fake timer functions come off before the real timers go back on.
  const timerSpies: Array<{ mockRestore(): void }> = []
  const spyTimer = <K extends 'setInterval' | 'clearInterval'>(name: K) => {
    const spy = vi.spyOn(globalThis, name)
    timerSpies.push(spy)
    return spy
  }

  afterEach(() => {
    for (const spy of timerSpies.splice(0)) spy.mockRestore()
    vi.useRealTimers()
  })

  it('says the conversation ended, under its title', () => {
    renderConversation()
    expect(screen.getByText('Conversation ended')).toBeInTheDocument()
    expect(screen.getByText('Fix the login bug')).toBeInTheDocument()
  })

  it('last in a terminal: terminal preselected, the action set shown', () => {
    renderConversation({ last_in: 'terminal' })
    expect(screen.getByTestId('rebuild-mode-terminal')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('rebuild-action-set')).toBeInTheDocument()
    expect(screen.getByTestId('rebuild-session-name-cell')).toHaveTextContent('proj-2')
    expect(screen.queryByTestId('terminated-rebuild-worker')).toBeNull()
  })

  // The pane exists to resume S: Rebuild runs the resume without the user ticking anything.
  it('the terminal rebuild resumes S by default', () => {
    renderConversation({ last_in: 'terminal' })
    expect(screen.getByRole('checkbox', { name: 'Run resume command' })).toBeChecked()
    expect(screen.getByTestId('rebuild-action-set')).toHaveTextContent(`claude --resume ${S}`)
    // No "running when last seen": the agent never ran in this pane.
    expect(screen.queryByTestId('rebuild-agent-state')).toBeNull()
  })

  it('last in a worker with Nexen ready: worker preselected, and the rebuild resumes S in its cwd without replacing anything', async () => {
    renderConversation({ last_in: 'worker' })
    expect(screen.getByTestId('rebuild-mode-worker')).toHaveAttribute('aria-checked', 'true')
    expect(screen.queryByTestId('rebuild-action-set')).toBeNull()
    fireEvent.click(screen.getByTestId('terminated-rebuild-worker'))
    await waitFor(() => expect(rebuildAsWorker).toHaveBeenCalledTimes(1))
    const args = vi.mocked(rebuildAsWorker).mock.calls[0][0]
    expect(args).toMatchObject({ hostId: H, sessionId: S, cwd: '/w/proj', tabId: TAB_ID, paneId: PANE_ID })
    expect(args.replaceExecutionId).toBeUndefined()
  })

  it('last in a worker without Nexen: the terminal screen, as for any closed pane', () => {
    useNexHostStore.setState({ byHost: {} } as never)
    renderConversation({ last_in: 'worker' })
    expect(screen.queryByTestId('rebuild-mode-worker')).toBeNull()
    expect(screen.getByTestId('rebuild-action-set')).toBeInTheDocument()
  })

  for (const mode of ['terminal', 'worker'] as const) {
    const control = mode === 'terminal' ? 'rebuild-action-set' : 'terminated-rebuild-worker'
    it(`written 30 s ago, ${mode} mode: the notice counts with the ${control} before anything is clicked, and is gone at 120 s`, () => {
      vi.useFakeTimers()
      vi.setSystemTime(NOW)
      const ticker = spyTimer('setInterval')
      const stopper = spyTimer('clearInterval')
      renderConversation({ last_in: mode, last_activity_at: NOW - 30_000 })
      const tick = ticker.mock.calls.findIndex(([, ms]) => ms === 1_000)
      expect(tick).toBeGreaterThanOrEqual(0)

      expect(screen.getByTestId(control)).toBeInTheDocument()
      expect(screen.getByTestId('terminated-recent-write')).toHaveTextContent(
        'This conversation was written to 30 seconds ago; it may be in use outside Purdex',
      )
      act(() => { vi.advanceTimersByTime(1_000) })
      expect(screen.getByTestId('terminated-recent-write')).toHaveTextContent('31 seconds ago')
      act(() => { vi.advanceTimersByTime(88_000) })
      expect(screen.getByTestId('terminated-recent-write')).toHaveTextContent('119 seconds ago')
      act(() => { vi.advanceTimersByTime(1_000) })
      expect(screen.queryByTestId('terminated-recent-write')).toBeNull()
      expect(screen.getByTestId(control)).toBeInTheDocument()
      // The count stops with the window.
      expect(stopper).toHaveBeenCalledWith(ticker.mock.results[tick].value)
    })
  }

  it('written 200 s ago: no notice, and nothing ticking', () => {
    vi.useFakeTimers()
    vi.setSystemTime(NOW)
    const ticker = spyTimer('setInterval')
    renderConversation({ last_activity_at: NOW - 200_000 })
    expect(screen.queryByTestId('terminated-recent-write')).toBeNull()
    expect(ticker.mock.calls.filter(([, ms]) => ms === 1_000)).toEqual([])
  })

  it('a closed pane that is not a conversation shows no notice', () => {
    const content = makeContent('session-closed')
    setupTab(content)
    render(<TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />)
    expect(screen.queryByTestId('terminated-recent-write')).toBeNull()
    expect(screen.getByTestId('rebuild-action-set')).toBeInTheDocument()
  })

  describe('reopened from the row (R-4-18)', () => {
    /** The screen as the app mounts it: re-rendered from the tab store, never remounted by a content write. */
    function LivePane() {
      const content = useTabStore((s) => findPane(s.tabs[TAB_ID].layout, PANE_ID)!.content) as Extract<PaneContent, { kind: 'tmux-session' }>
      return <TerminatedPane content={content} tabId={TAB_ID} paneId={PANE_ID} />
    }

    function mountLive(over: Partial<ConversationRow> = {}) {
      useWorkspaceStore.getState().reset()
      setupTab(conversationRebuildContent(H, row(over), 'proj-2', '111:1000', 1))
      return render(<LivePane />)
    }

    it('a newer last write on the row: the mounted screen counts from it at once, under the new title', async () => {
      vi.useFakeTimers()
      vi.setSystemTime(NOW)
      mountLive({ last_activity_at: NOW - 600_000 })
      expect(screen.queryByTestId('terminated-recent-write')).toBeNull()
      // A minute on the screen with nothing ticking, then the row is opened again: written 20 s before now.
      act(() => { vi.advanceTimersByTime(60_000) })

      await act(async () => { await openConversationRebuild(H, row({ last_activity_at: NOW + 40_000, title: 'Renamed' })) })

      expect(screen.getByTestId('terminated-recent-write')).toHaveTextContent('20 seconds ago')
      expect(screen.getByText('Renamed')).toBeInTheDocument()
      act(() => { vi.advanceTimersByTime(1_000) })
      expect(screen.getByTestId('terminated-recent-write')).toHaveTextContent('21 seconds ago')
      act(() => { vi.advanceTimersByTime(99_000) })
      expect(screen.queryByTestId('terminated-recent-write')).toBeNull()
    })

    it('the user switched to worker: the refresh keeps worker (preselection is the initial state only)', async () => {
      mountLive({ last_in: 'terminal', last_activity_at: Date.now() - 600_000 })
      expect(screen.queryByTestId('terminated-recent-write')).toBeNull()
      fireEvent.click(screen.getByTestId('rebuild-mode-worker'))
      expect(screen.getByTestId('rebuild-mode-worker')).toHaveAttribute('aria-checked', 'true')

      await act(async () => { await openConversationRebuild(H, row({ last_in: 'terminal', last_activity_at: Date.now() - 20_000 })) })

      expect(screen.getByTestId('terminated-recent-write')).toBeInTheDocument()
      expect(screen.getByTestId('rebuild-mode-worker')).toHaveAttribute('aria-checked', 'true')
      expect(screen.getByTestId('terminated-rebuild-worker')).toBeInTheDocument()
    })
  })
})
