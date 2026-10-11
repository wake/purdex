// NA-1: SessionPaneContent decides when a live session with no agent shows the launch panel, and renders the SAME panel for the
// deck and the chat, before the split into DeckPane / ChatPane (so neither DeckUnreadable no_session nor an empty ChatView).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, cleanup, act, fireEvent, waitFor } from '@testing-library/react'
import { TabContent } from './TabContent'
import { SessionPaneContent } from './SessionPaneContent'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useNexHostStore } from '../stores/useNexHostStore'
import { useSessionViewStore, sessionBinding } from '../stores/useSessionViewStore'
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

  it('the terminal view never asks and shows no panel', async () => {
    setView('terminal')
    mount()
    await act(async () => { await Promise.resolve() })
    expect(plans.fetch).not.toHaveBeenCalled()
    expect(screen.queryByTestId('launch-panel')).toBeNull()
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

  it('a failed plan read keeps today\'s view', async () => {
    plans.fetch.mockRejectedValue(new Error('boom'))
    setView('deck')
    mount()
    expect(await screen.findByTestId('deck-unreadable')).toBeInTheDocument()
    expect(screen.queryByTestId('launch-panel')).toBeNull()
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
