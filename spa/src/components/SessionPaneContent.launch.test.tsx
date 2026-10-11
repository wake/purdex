// NA-1: SessionPaneContent decides when a live session with no agent shows the launch panel, and renders the SAME panel for the
// deck and the chat, before the split into DeckPane / ChatPane (so neither DeckUnreadable no_session nor an empty ChatView).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup, act, fireEvent, waitFor } from '@testing-library/react'
import { TabContent } from './TabContent'
import { PaneModeButtons } from './status/PaneModeButtons'
import { SessionPaneContent } from './SessionPaneContent'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useSessionViewStore, sessionBinding } from '../stores/useSessionViewStore'
import { useLaunchPlanStore, resetLaunchPlanFlights } from '../stores/useLaunchPlanStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { createTab } from '../types/tab'
import type { Tab } from '../types/tab'
import type { LaunchPlan } from '../lib/launch-plan'

const conv = vi.hoisted(() => ({ value: { state: 'off' } as unknown }))
const gate = vi.hoisted(() => ({ value: { ok: false, reason: 'not_agent' } as { ok: boolean; reason: string | null } }))
const plans = vi.hoisted(() => ({ fetch: vi.fn() }))
vi.mock('../hooks/useConversationOfPane', () => ({ useConversationOfPane: () => conv.value }))
vi.mock('../hooks/useConversationViewGate', () => ({ useConversationViewGate: () => gate.value }))
vi.mock('../lib/launch-plan', async (orig) => ({ ...(await orig<typeof import('../lib/launch-plan')>()), fetchLaunchPlan: plans.fetch }))
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
const paneIdOf = (tab: Tab) => (tab.layout as { pane: { id: string } }).pane.id
const plan = (over: Partial<LaunchPlan> = {}): LaunchPlan => ({
  agentRunning: false, tmuxInstance: 'i', dir: { path: '/w/proj', state: 'move' },
  resume: { line: 'claude --resume x' }, fresh: { line: 'claude' }, busy: null, ...over,
})
const setCaps = (caps: string[]) =>
  useNexHostStore.setState({
    ensure: async () => {}, // the fixture entry has no phase; a real ensure would refetch it away
    byHost: { [H]: { daemonCapabilities: caps } } as unknown as ReturnType<typeof useNexHostStore.getState>['byHost'],
  })
const setView = (v: 'terminal' | 'deck' | 'chat') =>
  act(() => useSessionViewStore.getState().setView(sessionTab.id, paneIdOf(sessionTab), sessionBinding(H, CODE), v))
const mount = () => render(<TabContent activeTab={sessionTab} allTabs={[sessionTab]} />)

afterEach(() => { vi.restoreAllMocks() })
beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(() => ({ width: 1000, height: 600, top: 0, left: 0, right: 1000, bottom: 600, x: 0, y: 0, toJSON: () => ({}) }))
  cleanup()
  conv.value = { state: 'off' }
  gate.value = { ok: false, reason: 'not_agent' }
  plans.fetch.mockReset()
  useLaunchPlanStore.setState({ entries: {} })
  resetLaunchPlanFlights()
  plans.fetch.mockResolvedValue(plan())
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
  useSessionStore.setState({ sessions: { [H]: [{ code: CODE, name: CODE, cwd: '/w/proj', mode: 'terminal', branch: 'main', tmux_instance: 'i' }] } })
  setCaps(['session.launch.v1'])
})

describe('the user path: status bar deck / chat button to the panel', () => {
  // The real status bar buttons and the real pane, mounted together (the gate is mocked closed `not_agent`, as for a shell).
  const content = (sessionTab.layout as { pane: { content: import('../types/tab').PaneContent } }).pane.content
  const mountBoth = () => render(
    <>
      <PaneModeButtons tabId={sessionTab.id} pane={{ id: paneIdOf(sessionTab), content }} />
      <TabContent activeTab={sessionTab} allTabs={[sessionTab]} />
    </>,
  )

  it.each([['Deck'], ['Chat']])('starting in the terminal view, the plan resolves there; then clicking %s renders the launch panel', async (name) => {
    mountBoth()
    expect(screen.queryByTestId('launch-panel')).toBeNull()
    const btn = screen.getByRole('button', { name })
    await waitFor(() => expect(btn).toBeEnabled())
    expect(plans.fetch).toHaveBeenCalledTimes(1)
    fireEvent.click(btn)
    expect(await screen.findByTestId('launch-panel')).toBeInTheDocument()
  })

  it('while the plan GET is pending the deck / chat buttons are disabled ("checking")', async () => {
    plans.fetch.mockReturnValue(new Promise(() => {}))
    mountBoth()
    await act(async () => { await Promise.resolve() })
    for (const name of ['Deck', 'Chat']) {
      expect(screen.getByRole('button', { name })).toBeDisabled()
      expect(screen.getByRole('button', { name }).getAttribute('title')).toBe('Checking this session…')
    }
  })

  it('a plan with agent_running true keeps the buttons disabled with the not_agent reason', async () => {
    plans.fetch.mockResolvedValue(plan({ agentRunning: true }))
    mountBoth()
    await waitFor(() => expect(plans.fetch).toHaveBeenCalled())
    await act(async () => { await Promise.resolve() })
    expect(screen.getByRole('button', { name: 'Deck' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Deck' }).getAttribute('title')).toBe('This terminal is not running Claude Code')
  })

  it('a failed plan GET keeps the buttons disabled', async () => {
    plans.fetch.mockRejectedValue(new Error('boom'))
    mountBoth()
    await waitFor(() => expect(plans.fetch).toHaveBeenCalled())
    await act(async () => { await Promise.resolve() })
    expect(screen.getByRole('button', { name: 'Deck' })).toBeDisabled()
  })

  it('without session.launch.v1 the buttons stay disabled and nothing is asked', () => {
    setCaps([])
    mountBoth()
    expect(screen.getByRole('button', { name: 'Deck' })).toBeDisabled()
    expect(plans.fetch).not.toHaveBeenCalled()
  })
})

describe('when the panel shows', () => {
  it.each(['deck', 'chat'] as const)('%s: a live session with no agent and a plan with agent_running false', async (view) => {
    setView(view)
    mount()
    expect(await screen.findByTestId('launch-panel')).toBeInTheDocument()
    expect(screen.queryByTestId('deck-unreadable')).toBeNull()
    expect(screen.getByTestId('launch-dir').textContent).toContain('main') // the branch comes from the sessions list
  })

  it('deck and chat mount the same panel in the same state (one decision)', async () => {
    const seen: string[] = []
    for (const view of ['deck', 'chat'] as const) {
      cleanup()
      setView(view)
      mount()
      const panel = await screen.findByTestId('launch-panel')
      seen.push(panel.outerHTML)
    }
    expect(seen[0]).toBe(seen[1])
  })

  it('also when the host does not serve conversations (gate no_conversations)', async () => {
    gate.value = { ok: false, reason: 'no_conversations' }
    setView('deck')
    mount()
    expect(await screen.findByTestId('launch-panel')).toBeInTheDocument()
  })

  it('「切到終端機」 switches this pane to the terminal view; nothing switches by itself', async () => {
    setView('chat')
    mount()
    fireEvent.click(await screen.findByTestId('launch-to-terminal'))
    await waitFor(() => expect(screen.queryByTestId('launch-panel')).toBeNull())
    expect(screen.getByTestId('session-terminal-layer').getAttribute('style') ?? '').not.toContain('hidden')
  })
})

describe('when it does not', () => {
  it.each(['deck', 'chat'] as const)('%s: a codex (or any) agent is running -> agent_running true -> no panel', async (view) => {
    plans.fetch.mockResolvedValue(plan({ agentRunning: true }))
    setView(view)
    mount()
    await waitFor(() => expect(plans.fetch).toHaveBeenCalled())
    await act(async () => { await Promise.resolve() })
    expect(screen.queryByTestId('launch-panel')).toBeNull()
  })

  it('a host without session.launch.v1 keeps today\'s view and never asks', async () => {
    setCaps(['conversations.v1'])
    setView('deck')
    mount()
    expect(await screen.findByTestId('deck-unreadable')).toBeInTheDocument()
    expect(plans.fetch).not.toHaveBeenCalled()
    expect(screen.queryByTestId('launch-panel')).toBeNull()
  })

  it('the terminal view asks (the status bar needs the answer) but shows no panel', async () => {
    setView('terminal')
    mount()
    await waitFor(() => expect(plans.fetch).toHaveBeenCalledTimes(1))
    await act(async () => { await Promise.resolve() })
    expect(screen.queryByTestId('launch-panel')).toBeNull()
    expect(screen.queryByTestId('session-view-launch')).toBeNull()
  })

  it('a conversation that exists is not replaced by the panel', async () => {
    gate.value = { ok: true, reason: null }
    conv.value = { state: 'unreadable', reason: 'no_session' }
    setView('deck')
    mount()
    expect(await screen.findByTestId('deck-unreadable')).toBeInTheDocument()
    expect(plans.fetch).not.toHaveBeenCalled()
  })

  it.each(['host_hidden', 'not_session'])('other gate reason %s keeps today\'s behaviour', async (reason) => {
    gate.value = { ok: false, reason }
    setView('deck')
    mount()
    await act(async () => { await Promise.resolve() })
    expect(plans.fetch).not.toHaveBeenCalled()
    expect(screen.queryByTestId('launch-panel')).toBeNull()
  })

})

describe('while the plan is unknown or failed (deck / chat)', () => {
  it.each(['deck', 'chat'] as const)('%s: a pending read renders the empty launch container, never DeckPane / ChatPane', async (view) => {
    plans.fetch.mockReturnValue(new Promise(() => {}))
    setView(view)
    mount()
    await act(async () => { await Promise.resolve() })
    const box = screen.getByTestId('session-view-launch')
    expect(box.childElementCount).toBe(0)
    expect(screen.queryByTestId('deck-unreadable')).toBeNull()
    expect(screen.queryByTestId('launch-panel')).toBeNull()
    expect(screen.queryByTestId('session-footer')).toBeNull()
  })

  it('already in the deck, a plan that reloads for a new key (another session) does not flash DeckUnreadable', async () => {
    setView('deck')
    const { rerender } = mount()
    await screen.findByTestId('launch-panel')
    const next = { ...sessionTab, id: 't-other', layout: { type: 'leaf', pane: { id: 'p-other', content: { kind: 'tmux-session', hostId: H, sessionCode: 'dev002', mode: 'terminal', cachedName: 'dev002', tmuxInstance: 'j' } } } } as unknown as Tab
    plans.fetch.mockReturnValue(new Promise(() => {}))
    act(() => useSessionViewStore.getState().setView(next.id, 'p-other', sessionBinding(H, 'dev002'), 'deck'))
    useSessionStore.setState({ sessions: { [H]: [{ code: 'dev002', name: 'dev002', cwd: '/x', mode: 'terminal', tmux_instance: 'j' }] } })
    useTabStore.setState({ tabs: { [next.id]: next }, tabOrder: [next.id], activeTabId: next.id })
    rerender(<TabContent activeTab={next} allTabs={[next]} />)
    await act(async () => { await Promise.resolve() })
    expect(screen.queryByTestId('deck-unreadable')).toBeNull()
    expect(screen.queryByTestId('launch-panel')).toBeNull()
    expect(screen.getByTestId('session-view-launch')).toBeInTheDocument()
  })

  it.each(['deck', 'chat'] as const)('%s: a failed read shows the error block; refresh re-reads, terminal switches the view', async (view) => {
    plans.fetch.mockRejectedValue(new Error('boom'))
    setView(view)
    mount()
    expect(await screen.findByTestId('launch-plan-error')).toBeInTheDocument()
    expect(screen.queryByTestId('deck-unreadable')).toBeNull()
    plans.fetch.mockResolvedValue(plan())
    fireEvent.click(screen.getByTestId('launch-plan-error-refresh'))
    await waitFor(() => expect(plans.fetch).toHaveBeenCalledTimes(2))
    expect(await screen.findByTestId('launch-panel')).toBeInTheDocument()
  })

  it('the error block 「切到終端機」 switches this pane to the terminal view', async () => {
    plans.fetch.mockRejectedValue(new Error('boom'))
    setView('deck')
    mount()
    fireEvent.click(await screen.findByTestId('launch-plan-error-terminal'))
    await waitFor(() => expect(screen.queryByTestId('launch-plan-error')).toBeNull())
    expect(screen.getByTestId('session-terminal-layer').getAttribute('style') ?? '').not.toContain('hidden')
  })

  it('already in the deck when the agent turns out to be running: today\'s deck view', async () => {
    plans.fetch.mockResolvedValue(plan({ agentRunning: true }))
    setView('deck')
    mount()
    expect(await screen.findByTestId('deck-unreadable')).toBeInTheDocument()
    expect(screen.queryByTestId('session-view-launch')).toBeNull()
  })
})

describe('refresh', () => {
  it('re-reads the plan on 「重新整理」', async () => {
    setView('deck')
    mount()
    fireEvent.click(await screen.findByTestId('launch-refresh'))
    await waitFor(() => expect(plans.fetch).toHaveBeenCalledTimes(2))
  })

  it('re-reads when the sessions list changes for this session, and not for another one', async () => {
    setView('deck')
    mount()
    await screen.findByTestId('launch-panel')
    expect(plans.fetch).toHaveBeenCalledTimes(1)
    act(() => useSessionStore.getState().replaceHost(H, [
      { code: CODE, name: CODE, cwd: '/w/proj', mode: 'terminal', branch: 'main', tmux_instance: 'i' },
      { code: 'other', name: 'o', cwd: '/x', mode: 'terminal' },
    ]))
    await act(async () => { await Promise.resolve() })
    expect(plans.fetch).toHaveBeenCalledTimes(1)
    act(() => useSessionStore.getState().replaceHost(H, [{ code: CODE, name: CODE, cwd: '/w/other', mode: 'terminal', branch: 'main', tmux_instance: 'i', current_command: 'zsh' }]))
    await waitFor(() => expect(plans.fetch).toHaveBeenCalledTimes(2))
  })

  it('an agent_running refusal from a launch re-reads and the panel goes away', async () => {
    const { LaunchError } = await import('../lib/launch-plan')
    const post = vi.spyOn(await import('../lib/launch-plan'), 'postLaunch').mockRejectedValue(new LaunchError(409, 'agent_running'))
    void post
    setView('deck')
    mount()
    plans.fetch.mockResolvedValue(plan({ agentRunning: true }))
    fireEvent.click(await screen.findByTestId('launch-fresh'))
    await waitFor(() => expect(screen.queryByTestId('launch-panel')).toBeNull())
  })
})

describe('one answer per tmux generation, no stale plan', () => {
  const content = (sessionTab.layout as { pane: { content: import('../types/tab').PaneContent } }).pane.content
  const both = () => (
    <>
      <PaneModeButtons tabId={sessionTab.id} pane={{ id: paneIdOf(sessionTab), content }} />
      <TabContent activeTab={sessionTab} allTabs={[sessionTab]} />
    </>
  )
  const sessionRow = (inst: string) => ({ code: CODE, name: CODE, cwd: '/w/proj', mode: 'terminal' as const, branch: 'main', tmux_instance: inst })

  it('(a) the same code reused by a new generation: checking and an empty pane until that generation answers', async () => {
    setView('deck')
    plans.fetch.mockResolvedValue(plan({ tmuxInstance: '' })) // no generation named: only the key tells the two apart
    render(both())
    await screen.findByTestId('launch-panel')
    expect(screen.getByRole('button', { name: 'Chat' })).toBeEnabled()
    let resolveB!: (p: LaunchPlan) => void
    plans.fetch.mockReturnValue(new Promise<LaunchPlan>((r) => { resolveB = r }))
    act(() => useSessionStore.getState().replaceHost(H, [sessionRow('j')]))
    await act(async () => { await Promise.resolve() })
    expect(screen.getByRole('button', { name: 'Chat' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Chat' }).getAttribute('title')).toBe('Checking this session…')
    expect(screen.queryByTestId('launch-panel')).toBeNull()
    expect(screen.getByTestId('session-view-launch').childElementCount).toBe(0)
    await act(async () => { resolveB(plan({ tmuxInstance: 'j' })) })
    expect(await screen.findByTestId('launch-panel')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Chat' })).toBeEnabled()
  })

  it('(b) a gate flip (an agent ran and ended) drops the old plan: checking, no stale panel', async () => {
    setView('deck')
    const { rerender } = render(both())
    await screen.findByTestId('launch-panel')
    conv.value = { state: 'loading' } // the agent is running: not a candidate any more
    rerender(both())
    expect(screen.queryByTestId('launch-panel')).toBeNull()
    plans.fetch.mockReturnValue(new Promise(() => {}))
    conv.value = { state: 'off' } // and it ended
    rerender(both())
    await act(async () => { await Promise.resolve() })
    expect(screen.getByRole('button', { name: 'Chat' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Chat' }).getAttribute('title')).toBe('Checking this session…')
    expect(screen.queryByTestId('launch-panel')).toBeNull()
    expect(screen.getByTestId('session-view-launch').childElementCount).toBe(0)
  })

  it('(c) a tab remount with a ready entry shows the panel at once, re-reading in the background', async () => {
    setView('deck')
    const first = mount()
    await screen.findByTestId('launch-panel')
    first.unmount()
    plans.fetch.mockClear()
    plans.fetch.mockReturnValue(new Promise(() => {}))
    mount()
    expect(screen.getByTestId('launch-panel')).toBeInTheDocument()
    expect(plans.fetch).toHaveBeenCalledTimes(1)
  })

  it('(d) two panes of the same session mounted together ask once', async () => {
    setView('deck')
    render(<><TabContent activeTab={sessionTab} allTabs={[sessionTab]} /><TabContent activeTab={sessionTab} allTabs={[sessionTab]} /></>)
    await act(async () => { await Promise.resolve() })
    expect(plans.fetch).toHaveBeenCalledTimes(1)
  })
})
