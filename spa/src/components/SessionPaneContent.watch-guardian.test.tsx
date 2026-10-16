// T-a3.5 / T-a3.6: the guardian tab is a stock session tab. Only two slots differ, for the `watch-guardian` tab id alone:
// a terminated pane and a resting `pdx-watch` at a shell (Deck / Chat) show the watcher's launch panel, never TerminatedPane
// or the stock LaunchPanel. The status bar's view buttons are the stock ones and the chosen view survives a tab switch.
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
import { useWatchStatusStore } from '../lib/watch/status-store'
import { createTab } from '../types/tab'
import type { PaneContent, Tab } from '../types/tab'
import type { LaunchPlan } from '../lib/launch-plan'

const conv = vi.hoisted(() => ({ value: { state: 'off' } as unknown }))
const gate = vi.hoisted(() => ({ value: { ok: false, reason: 'not_agent' } as { ok: boolean; reason: string | null } }))
const plans = vi.hoisted(() => ({ fetch: vi.fn() }))
vi.mock('../hooks/useConversationOfPane', () => ({ useConversationOfPane: () => conv.value }))
vi.mock('../hooks/useConversationViewGate', () => ({ useConversationViewGate: () => gate.value }))
vi.mock('../lib/launch-plan', async (orig) => ({ ...(await orig<typeof import('../lib/launch-plan')>()), fetchLaunchPlan: plans.fetch }))
vi.mock('./TerminalView', () => ({ default: () => <div data-testid="terminal-view" /> }))
vi.mock('./TerminatedPane', () => ({ TerminatedPane: () => <div data-testid="terminated-pane" /> }))
vi.mock('../lib/host-api', async (orig) => ({ ...(await orig<typeof import('../lib/host-api')>()), fetchWsTicket: vi.fn(async () => 't') }))
vi.mock('../lib/rebuild/cwd-probe', () => ({ probeSessionCwd: vi.fn() }))
vi.mock('../lib/rebuild/provenance-probe', () => ({ probeSessionProvenance: vi.fn() }))
vi.mock('../lib/watch/watch-row', async (orig) => ({
  ...(await orig<typeof import('../lib/watch/watch-row')>()),
  readWatchRow: vi.fn(async () => ({ revision: 1, row: {}, agentCommand: 'claude --model opus', workdir: '~/w' })),
}))
vi.mock('./deck/SessionInput', () => ({ SessionInput: () => <div data-testid="input-footer" /> }))

const H = 'host-1'
const CODE = 'wc001'
const tmux: PaneContent = { kind: 'tmux-session', hostId: H, sessionCode: CODE, mode: 'terminal', cachedName: 'pdx-watch', tmuxInstance: 'i' }
const guardianTab = (content: PaneContent = tmux): Tab => ({ ...createTab(content), id: 'watch-guardian', locked: true })
const ordinaryTab = (content: PaneContent = tmux): Tab => ({ ...createTab(content), id: 't-session' })
const paneIdOf = (tab: Tab) => (tab.layout as { pane: { id: string } }).pane.id
const contentOf = (tab: Tab) => (tab.layout as { pane: { content: PaneContent } }).pane.content
const plan = (): LaunchPlan => ({ agentRunning: false, tmuxInstance: 'i', dir: { path: '/w', state: 'move' }, resume: { line: 'claude --resume x' }, fresh: { line: 'claude' }, busy: null })
const other: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }

const setView = (tab: Tab, v: 'terminal' | 'deck' | 'chat') =>
  act(() => useSessionViewStore.getState().setView(tab.id, paneIdOf(tab), sessionBinding(H, CODE), v))
const load = (...tabs: Tab[]) => useTabStore.setState({ tabs: Object.fromEntries(tabs.map((x) => [x.id, x])), tabOrder: tabs.map((x) => x.id), activeTabId: tabs[0].id, visitHistory: [] })

afterEach(() => { vi.restoreAllMocks() })
beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(() => ({ width: 1000, height: 600, top: 0, left: 0, right: 1000, bottom: 600, x: 0, y: 0, toJSON: () => ({}) }))
  cleanup()
  conv.value = { state: 'off' }
  gate.value = { ok: false, reason: 'not_agent' }
  plans.fetch.mockReset()
  plans.fetch.mockResolvedValue(plan())
  useLaunchPlanStore.setState({ entries: {} })
  resetLaunchPlanFlights()
  clearModuleRegistry()
  registerModule({ id: 'terminal', name: 'Terminal', panes: [{ kind: 'tmux-session', component: SessionPaneContent }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: () => <div data-testid="other-tab" /> }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0, daemonId: 'd1' } },
    hostOrder: [H], activeHostId: H, runtime: { [H]: { status: 'connected' as const, attachReady: true } },
  })
  useSessionViewStore.setState({ byPane: {} })
  useSessionStore.setState({ sessions: { [H]: [{ code: CODE, name: 'pdx-watch', cwd: '/w', mode: 'terminal', tmux_instance: 'i' }] } })
  useWatchStatusStore.setState({ byHost: { [H]: { kind: 'ok', at: 1, status: { self: { host_id: 'd1', state: 'resting', since: '2026-10-16T01:00:00Z', session_id: 'conv-77' }, enabled: true, auto_wake: true, mode: 'patrol', targets: [] } } } })
  useNexHostStore.setState({ ensure: async () => {}, byHost: { [H]: { daemonCapabilities: ['session.launch.v1'] } } as unknown as ReturnType<typeof useNexHostStore.getState>['byHost'] })
})

describe('a terminated guardian pane', () => {
  it('shows the watcher\'s launch panel, never TerminatedPane (no rebuild)', () => {
    const tab = guardianTab({ ...tmux, terminated: 'session-closed' } as PaneContent)
    load(tab)
    render(<TabContent activeTab={tab} allTabs={[tab]} />)
    expect(screen.getByTestId('watch-launch-panel')).toBeInTheDocument()
    expect(screen.queryByTestId('terminated-pane')).toBeNull()
  })

  it('a terminated session tab elsewhere still shows TerminatedPane', () => {
    const tab = ordinaryTab({ ...tmux, terminated: 'session-closed' } as PaneContent)
    load(tab)
    render(<TabContent activeTab={tab} allTabs={[tab]} />)
    expect(screen.getByTestId('terminated-pane')).toBeInTheDocument()
    expect(screen.queryByTestId('watch-launch-panel')).toBeNull()
  })
})

describe('a resting pdx-watch at a shell, in Deck and Chat', () => {
  it.each(['deck', 'chat'] as const)('%s: the guardian tab shows the watcher\'s launch panel, not the stock LaunchPanel', async (view) => {
    const tab = guardianTab()
    load(tab)
    setView(tab, view)
    render(<TabContent activeTab={tab} allTabs={[tab]} />)
    expect(await screen.findByTestId('watch-launch-panel')).toBeInTheDocument()
    expect(screen.queryByTestId('launch-panel')).toBeNull()
    expect(screen.getByTestId('watch-launch-conversation')).toHaveTextContent('conv-77')
    await waitFor(() => expect(screen.getByTestId('watch-launch-command')).toHaveTextContent('claude --model opus'))
  })

  it.each(['deck', 'chat'] as const)('%s: a session tab elsewhere still shows the stock LaunchPanel', async (view) => {
    const tab = ordinaryTab()
    load(tab)
    setView(tab, view)
    render(<TabContent activeTab={tab} allTabs={[tab]} />)
    expect(await screen.findByTestId('launch-panel')).toBeInTheDocument()
    expect(screen.queryByTestId('watch-launch-panel')).toBeNull()
  })

  it('the terminal view stays the terminal (the panel is only for Deck and Chat)', () => {
    const tab = guardianTab()
    load(tab)
    render(<TabContent activeTab={tab} allTabs={[tab]} />)
    expect(screen.getByTestId('terminal-view')).toBeInTheDocument()
    expect(screen.queryByTestId('watch-launch-panel')).toBeNull()
  })
})

describe('the three views through the stock status bar buttons', () => {
  it('terminal → Deck → Chat → terminal, as in any session tab', async () => {
    const tab = guardianTab()
    load(tab)
    render(
      <>
        <PaneModeButtons tabId={tab.id} pane={{ id: paneIdOf(tab), content: contentOf(tab) }} />
        <TabContent activeTab={tab} allTabs={[tab]} />
      </>,
    )
    const deck = screen.getByRole('button', { name: 'Deck' })
    await waitFor(() => expect(deck).toBeEnabled())
    fireEvent.click(deck)
    expect(await screen.findByTestId('watch-launch-panel')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Chat' }))
    expect(await screen.findByTestId('watch-launch-panel')).toBeInTheDocument()
    expect(useSessionViewStore.getState().byPane).not.toEqual({})
    fireEvent.click(screen.getByRole('button', { name: 'Terminal' }))
    await waitFor(() => expect(screen.queryByTestId('watch-launch-panel')).toBeNull())
    expect(screen.getByTestId('session-terminal-layer').getAttribute('style') ?? '').not.toContain('hidden')
  })
})

describe('across a tab switch', () => {
  it('keeps the chosen view when the reader goes to another tab and back', async () => {
    const tab = guardianTab()
    load(tab, other)
    const all = [tab, other]
    const { rerender } = render(<TabContent activeTab={tab} allTabs={all} />)
    setView(tab, 'chat')
    expect(await screen.findByTestId('watch-launch-panel')).toBeInTheDocument()
    rerender(<TabContent activeTab={other} allTabs={all} />)
    expect(screen.queryByTestId('watch-launch-panel')).toBeNull() // really unmounted (keepAliveCount 0)
    expect(screen.getByTestId('other-tab')).toBeInTheDocument()
    rerender(<TabContent activeTab={tab} allTabs={all} />)
    expect(await screen.findByTestId('watch-launch-panel')).toBeInTheDocument()
    expect(useSessionViewStore.getState().byPane).not.toEqual({})
  }, 30_000)
})
