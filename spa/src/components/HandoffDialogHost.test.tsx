// spa/src/components/HandoffDialogHost.test.tsx — the one app-level "Hand to nex" dialog (shell cleanup spec §9.4):
// it renders from `useHandoffDialogStore`, and closes by itself when the target's host is hidden or the pane stops
// holding the session. `handToNex` is the real one; only the daemon call is mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { HandoffDialogHost } from './HandoffDialogHost'
import { useHandoffDialogStore, type HandoffDialogTarget } from '../stores/useHandoffDialogStore'
import { useTabStore } from '../stores/useTabStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useHostConfigStore } from '../stores/useHostConfigStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useUndoToast } from '../stores/useUndoToast'
import { nexHandoff, type NexHandoffResult } from '../lib/nex/handoff-api'
import { createTab, type PaneContent, type TmuxSessionContent } from '../types/tab'
import { getPrimaryPane } from '../lib/pane-tree'

vi.mock('../lib/nex/handoff-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/nex/handoff-api')>()),
  nexHandoff: vi.fn(),
}))
const mockedHandoff = vi.mocked(nexHandoff)

const H = 'h1'
const session = (over: Partial<TmuxSessionContent> = {}): TmuxSessionContent => ({
  kind: 'tmux-session', hostId: H, sessionCode: 'zk16vd', mode: 'terminal', cachedName: 'purdex', tmuxInstance: 'inst-1', ...over,
})
const ok: NexHandoffResult = { execution_id: 'exc_1', state: 'running', effective_profile: 'handoff', session_id: 'sid-1', cwd: '/w', session_kept: true }

function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((res) => { resolve = res })
  return { promise, resolve }
}

/** A tab whose only pane holds `content`; returns the dialog target for it. */
function seedPane(content: TmuxSessionContent = session()): HandoffDialogTarget {
  const tab = createTab(content)
  useTabStore.getState().addTab(tab)
  return { tabId: tab.id, paneId: getPrimaryPane(tab.layout).id, content }
}
const paneContent = (t: HandoffDialogTarget): PaneContent | undefined => {
  const tab = useTabStore.getState().tabs[t.tabId]
  return tab ? getPrimaryPane(tab.layout).content : undefined
}

const dialog = () => screen.queryByTestId('handoff-dialog')
const open = (t: HandoffDialogTarget) => act(() => { useHandoffDialogStore.getState().open(t) })

beforeEach(() => {
  cleanup()
  useHandoffDialogStore.setState({ target: null })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useShownHostsStore.setState({ ids: [H] })
  useNexHostStore.setState({
    byHost: {
      [H]: {
        info: null,
        capabilities: { delegate: { resume_session_id: true }, sandbox_profiles: ['default', 'handoff'] } as never,
        phase: 'ready', error: null, fetchedAt: 0, generation: 1, fingerprint: 'f',
      },
    },
    ensure: vi.fn().mockResolvedValue(undefined),
  } as never)
  useHostConfigStore.setState({ byHost: {}, ensureLoaded: vi.fn().mockResolvedValue(undefined) } as never)
  useSessionStore.setState({ sessions: {} })
  // Claude Code runs in every seeded session: the gate (§9.3) is open until a test closes it.
  useAgentStore.setState({ agentTypes: { [`${H}:zk16vd`]: 'cc', [`${H}:second`]: 'cc' } })
  useUndoToast.setState({ toast: null })
  mockedHandoff.mockReset()
})
afterEach(() => vi.restoreAllMocks())

describe('HandoffDialogHost', () => {
  it('renders nothing until the store has a target', () => {
    render(<HandoffDialogHost />)
    expect(dialog()).toBeNull()
  })

  it('open → the dialog; Confirm hands off the target pane and closes it', async () => {
    const target = seedPane()
    mockedHandoff.mockResolvedValueOnce(ok)
    render(<HandoffDialogHost />)
    open(target)
    expect(dialog()).toBeInTheDocument()
    await act(async () => { fireEvent.click(screen.getByTestId('handoff-confirm')) })
    expect(mockedHandoff).toHaveBeenCalledTimes(1)
    expect(mockedHandoff.mock.calls[0].slice(0, 2)).toEqual([H, 'zk16vd'])
    expect(paneContent(target)).toMatchObject({ kind: 'execution', executionId: 'exc_1', host: H })
    expect(dialog()).toBeNull()
    expect(useHandoffDialogStore.getState().target).toBeNull()
    expect(useUndoToast.getState().toast?.message).toBe('Handed to nex.')
  })

  it('Cancel closes it without a request', () => {
    const target = seedPane()
    render(<HandoffDialogHost />)
    open(target)
    fireEvent.click(screen.getByTestId('handoff-cancel'))
    expect(dialog()).toBeNull()
    expect(useHandoffDialogStore.getState().target).toBeNull()
    expect(mockedHandoff).not.toHaveBeenCalled()
  })

  it('hiding the target\'s host closes it (the pane is gated; nothing is sent); showing it again does not reopen it', () => {
    const target = seedPane()
    render(<HandoffDialogHost />)
    open(target)
    act(() => { useShownHostsStore.setState({ ids: [] }) })
    expect(dialog()).toBeNull()
    expect(useHandoffDialogStore.getState().target).toBeNull()
    act(() => { useShownHostsStore.setState({ ids: [H] }) })
    expect(dialog()).toBeNull()
    expect(mockedHandoff).not.toHaveBeenCalled()
  })

  it('a target on a host already hidden never shows', () => {
    const target = seedPane()
    useShownHostsStore.setState({ ids: [] })
    render(<HandoffDialogHost />)
    open(target)
    expect(dialog()).toBeNull()
    expect(useHandoffDialogStore.getState().target).toBeNull()
  })

  describe('closes when the pane no longer holds that session', () => {
    it('the pane now shows another session', () => {
      const target = seedPane()
      render(<HandoffDialogHost />)
      open(target)
      act(() => { useTabStore.getState().setPaneContent(target.tabId, target.paneId, session({ sessionCode: 'other' })) })
      expect(dialog()).toBeNull()
    })

    it('the same session under a new tmux instance (a different process)', () => {
      const target = seedPane()
      render(<HandoffDialogHost />)
      open(target)
      act(() => { useTabStore.getState().setPaneContent(target.tabId, target.paneId, session({ tmuxInstance: 'inst-2' })) })
      expect(dialog()).toBeNull()
    })

    it('the tab was closed', () => {
      const target = seedPane()
      render(<HandoffDialogHost />)
      open(target)
      act(() => { useTabStore.getState().closeTab(target.tabId) })
      expect(dialog()).toBeNull()
      expect(useHandoffDialogStore.getState().target).toBeNull()
    })

    it('a target whose pane is already gone never shows', () => {
      render(<HandoffDialogHost />)
      open({ tabId: 'gone', paneId: 'p1', content: session() })
      expect(dialog()).toBeNull()
    })

    it('a rename of the same session keeps it open', () => {
      const target = seedPane()
      render(<HandoffDialogHost />)
      open(target)
      act(() => { useTabStore.getState().setPaneContent(target.tabId, target.paneId, session({ cachedName: 'renamed' })) })
      expect(dialog()).toBeInTheDocument()
    })
  })

  // P6 review A1: the dialog lives only while the shared gate (useHandoffGate) is open on the pane's LIVE content.
  describe('closes when the handoff gate closes on the live pane', () => {
    it('Claude Code exits (its agent type is cleared) → closes; nothing is sent', () => {
      const target = seedPane()
      render(<HandoffDialogHost />)
      open(target)
      expect(dialog()).toBeInTheDocument()
      act(() => { useAgentStore.getState().clearSession(H, 'zk16vd') })
      expect(dialog()).toBeNull()
      expect(useHandoffDialogStore.getState().target).toBeNull()
      expect(mockedHandoff).not.toHaveBeenCalled()
    })

    // P6 re-review: with a cc rebuild record, clearing the live type alone leaves the record as the source. The real
    // SessionEnd (a `clear` event carrying `pdx_exit`) marks that record exited AND clears the type → closes.
    it('Claude Code exits on a pane whose rebuild record says cc (the real SessionEnd path) → closes; nothing is sent', () => {
      const target = seedPane(session({
        rebuild: { sessionName: 'purdex', tmuxInstance: 'inst-1', agent: { type: 'cc', sessionId: 'S1', frameId: 'F1', updatedAt: 1 }, capturedAt: 1 },
      }))
      render(<HandoffDialogHost />)
      open(target)
      expect(dialog()).toBeInTheDocument()
      act(() => {
        useAgentStore.getState().handleNormalizedEvent(H, 'zk16vd', {
          agent_type: 'cc', status: 'clear', raw_event_name: 'PdxSessionEnd', broadcast_ts: 1, subagents: [],
          detail: { pdx_exit: { agent_type: 'cc', session_id: 'S1', tmux_pane_id: '%1', tmux_instance: 'inst-1', frame_id: 'F1', reason: 'session-end', at: 7_000 } },
        })
      })
      // The exit really landed through the store path, and the live type is gone.
      const live = paneContent(target)
      expect(live?.kind === 'tmux-session' && live.rebuild?.agentExited).toEqual({ at: 7_000, reason: 'session-end' })
      expect(useAgentStore.getState().agentTypes[`${H}:zk16vd`]).toBeUndefined()
      expect(dialog()).toBeNull()
      expect(useHandoffDialogStore.getState().target).toBeNull()
      expect(mockedHandoff).not.toHaveBeenCalled()
    })

    it('another agent now runs in the session → closes', () => {
      const target = seedPane()
      render(<HandoffDialogHost />)
      open(target)
      act(() => { useAgentStore.setState({ agentTypes: { [`${H}:zk16vd`]: 'codex' } }) })
      expect(dialog()).toBeNull()
      expect(useHandoffDialogStore.getState().target).toBeNull()
    })

    it('the pane\'s session is terminated (same identity) → closes', () => {
      const target = seedPane()
      render(<HandoffDialogHost />)
      open(target)
      act(() => { useTabStore.getState().setPaneContent(target.tabId, target.paneId, session({ terminated: 'session-closed' })) })
      expect(dialog()).toBeNull()
      expect(useHandoffDialogStore.getState().target).toBeNull()
    })

    it('Nex stops being ready on the host → closes', () => {
      const target = seedPane()
      render(<HandoffDialogHost />)
      open(target)
      act(() => {
        useNexHostStore.setState((s) => ({ byHost: { ...s.byHost, [H]: { ...s.byHost[H], phase: 'unavailable' } } }) as never)
      })
      expect(dialog()).toBeNull()
      expect(useHandoffDialogStore.getState().target).toBeNull()
    })

    it('a target the gate already refuses never shows', () => {
      const target = seedPane()
      useAgentStore.setState({ agentTypes: {} })
      render(<HandoffDialogHost />)
      open(target)
      expect(dialog()).toBeNull()
      expect(useHandoffDialogStore.getState().target).toBeNull()
    })
  })

  describe('the initial view mode (shell cleanup spec §9.4)', () => {
    it('a target with mode chat → the pane becomes the execution in chat', async () => {
      const target = seedPane()
      mockedHandoff.mockResolvedValueOnce(ok)
      render(<HandoffDialogHost />)
      open({ ...target, mode: 'chat' })
      await act(async () => { fireEvent.click(screen.getByTestId('handoff-confirm')) })
      expect(paneContent(target)).toMatchObject({ kind: 'execution', executionId: 'exc_1', mode: 'chat' })
    })

    it('mode chat, the pane closed mid-request → the dialog closes; "Open execution" opens a chat tab', async () => {
      const target = seedPane()
      const d = deferred<NexHandoffResult>()
      mockedHandoff.mockReturnValueOnce(d.promise)
      render(<HandoffDialogHost />)
      open({ ...target, mode: 'chat' })
      await act(async () => { fireEvent.click(screen.getByTestId('handoff-confirm')) })
      expect(mockedHandoff).toHaveBeenCalledTimes(1)
      act(() => { useTabStore.getState().closeTab(target.tabId) })
      expect(dialog()).toBeNull()
      await act(async () => { d.resolve(ok) })
      const toast = useUndoToast.getState().toast
      expect(toast?.actionLabel).toBe('Open execution')
      act(() => { toast!.action!() })
      const opened = Object.values(useTabStore.getState().tabs).map((tab) => getPrimaryPane(tab.layout).content)
      expect(opened).toEqual([{
        kind: 'execution', executionId: 'exc_1', host: H,
        from: { sessionCode: 'zk16vd', tmuxInstance: 'inst-1', cachedName: 'purdex' },
        mode: 'chat',
      }])
    })
  })

  it('a request that finishes after its dialog closed does not close a dialog opened since', async () => {
    const first = seedPane()
    const second = seedPane(session({ sessionCode: 'second' }))
    const d = deferred<NexHandoffResult>()
    mockedHandoff.mockReturnValueOnce(d.promise)
    render(<HandoffDialogHost />)
    open(first)
    await act(async () => { fireEvent.click(screen.getByTestId('handoff-confirm')) })
    expect(mockedHandoff).toHaveBeenCalledTimes(1)
    // The first pane goes away mid-request → its dialog closes; a second one is opened on another pane.
    act(() => { useTabStore.getState().closeTab(first.tabId) })
    expect(dialog()).toBeNull()
    open(second)
    expect(dialog()).toBeInTheDocument()
    await act(async () => { d.resolve(ok) })
    expect(useHandoffDialogStore.getState().target?.paneId).toBe(second.paneId)
    expect(dialog()).toBeInTheDocument()
  })
})
