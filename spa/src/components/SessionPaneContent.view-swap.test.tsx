// U3-1a (plan D1/D2): the view of a session pane (terminal / deck / chat) is swapped without unmounting the terminal, and
// it is device-local state outside the component, so it survives the tab itself unmounting. The second half mounts the
// real TabContent (the alive pool keeps no tab by default: keepAliveCount 0, and a session pane is not in the light
// whitelist, so switching away really unmounts it — the same shape as ExecutionView.tab-switch.test.tsx).
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, cleanup, act } from '@testing-library/react'
import { useEffect } from 'react'
import { TabContent } from './TabContent'
import { SessionPaneContent } from './SessionPaneContent'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useSessionViewStore, sessionBinding } from '../stores/useSessionViewStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { createTab } from '../types/tab'
import type { Tab } from '../types/tab'

const seen = vi.hoisted(() => ({ mounts: 0, unmounts: 0, last: undefined as Record<string, unknown> | undefined }))

vi.mock('./TerminalView', () => ({
  default: function TerminalViewMock(props: Record<string, unknown>) {
    seen.last = props
    useEffect(() => {
      seen.mounts += 1
      return () => { seen.unmounts += 1 }
    }, [])
    return <div data-testid="terminal-view" />
  },
}))
vi.mock('../lib/host-api', async (orig) => ({ ...(await orig<typeof import('../lib/host-api')>()), fetchWsTicket: vi.fn(async () => 't') }))
vi.mock('../lib/rebuild/cwd-probe', () => ({ probeSessionCwd: vi.fn() }))
vi.mock('../lib/rebuild/provenance-probe', () => ({ probeSessionProvenance: vi.fn() }))

const H = 'host-1'
const CODE = 'dev001'
const sessionTab: Tab = {
  ...createTab({ kind: 'tmux-session', hostId: H, sessionCode: CODE, mode: 'terminal', cachedName: CODE, tmuxInstance: 'i' }),
  id: 't-session',
}
const dashTab: Tab = { ...createTab({ kind: 'dashboard' }), id: 't-dash' }
const paneIdOf = (tab: Tab) => (tab.layout as { pane: { id: string } }).pane.id
const all = [sessionTab, dashTab]

beforeEach(() => {
  cleanup()
  seen.mounts = 0
  seen.unmounts = 0
  seen.last = undefined
  clearModuleRegistry()
  registerModule({ id: 'terminal', name: 'Terminal', panes: [{ kind: 'tmux-session', component: SessionPaneContent }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: () => <div data-testid="other-tab" /> }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: [H] })
  useHostStore.setState({
    hosts: { [H]: { id: H, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 } },
    hostOrder: [H],
    activeHostId: H,
    runtime: { [H]: { status: 'connected' as const, attachReady: true } },
  })
  useTabStore.setState({ tabs: { [sessionTab.id]: sessionTab, [dashTab.id]: dashTab }, tabOrder: [sessionTab.id, dashTab.id], activeTabId: sessionTab.id, visitHistory: [] })
  useSessionViewStore.setState({ byPane: {} })
})

const layer = () => screen.getByTestId('session-terminal-layer')
const setView = (v: 'terminal' | 'deck' | 'chat') =>
  act(() => useSessionViewStore.getState().setView(sessionTab.id, paneIdOf(sessionTab), sessionBinding(H, CODE), v))

describe('the swap inside a pane', () => {
  it('keeps the same terminal mounted across deck, chat and back (no reconnect)', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(seen.mounts).toBe(1)
    expect(layer().style.visibility).toBe('')
    expect(seen.last?.visible).toBe(true)

    setView('deck')
    expect(screen.getByTestId('session-view-deck')).toBeInTheDocument()
    expect(layer().style.visibility).toBe('hidden') // not display:none: the fit observer skips zero-size boxes
    expect(layer().hasAttribute('inert')).toBe(true)
    expect(seen.last?.visible).toBe(false)

    setView('chat')
    expect(screen.getByTestId('session-view-chat')).toBeInTheDocument()
    expect(screen.queryByTestId('session-view-deck')).toBeNull()

    setView('terminal')
    expect(screen.queryByTestId('session-view-chat')).toBeNull()
    expect(layer().style.visibility).toBe('')
    expect(layer().hasAttribute('inert')).toBe(false)
    expect(seen.last?.visible).toBe(true)

    expect(seen.mounts).toBe(1)
    expect(seen.unmounts).toBe(0)
  })

  it('the terminal is not asked to take focus while another view is up', () => {
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    setView('deck')
    expect(seen.last?.isFocusTarget).toBe(false)
  })

  it('a view chosen for the same code on another host does not apply to this pane', () => {
    act(() => useSessionViewStore.getState().setView(sessionTab.id, paneIdOf(sessionTab), sessionBinding('host-2', CODE), 'deck'))
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.queryByTestId('session-view-deck')).toBeNull()
  })

  it('a view chosen for another session does not apply to this pane', () => {
    act(() => useSessionViewStore.getState().setView(sessionTab.id, paneIdOf(sessionTab), sessionBinding(H, 'other01'), 'deck'))
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.queryByTestId('session-view-deck')).toBeNull()
    expect(seen.last?.visible).toBe(true)
  })
})

describe('the view across a real tab switch', () => {
  it('comes back as it was: the pane unmounts, the choice lives in the store', () => {
    const { rerender } = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    setView('chat')
    expect(screen.getByTestId('session-view-chat')).toBeInTheDocument()

    rerender(<TabContent activeTab={dashTab} allTabs={all} />)
    // Really gone, not merely hidden — otherwise this would prove nothing about the store.
    expect(screen.queryByTestId('terminal-view')).toBeNull()
    expect(screen.queryByTestId('session-view-chat')).toBeNull()
    expect(seen.unmounts).toBe(1)

    rerender(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.getByTestId('session-view-chat')).toBeInTheDocument()
    expect(seen.mounts).toBe(2)
    expect(seen.last?.visible).toBe(false) // the terminal behind it is mounted again, hidden
  })

  it('survives a full unmount and remount', () => {
    const first = render(<TabContent activeTab={sessionTab} allTabs={all} />)
    setView('deck')
    first.unmount()
    render(<TabContent activeTab={sessionTab} allTabs={all} />)
    expect(screen.getByTestId('session-view-deck')).toBeInTheDocument()
  })
})
