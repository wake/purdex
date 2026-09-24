import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, cleanup, act } from '@testing-library/react'
import { SessionPaneContent } from './SessionPaneContent'
import { useHostStore } from '../stores/useHostStore'
import { useHostLookStore } from '../stores/useHostLookStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useTabStore } from '../stores/useTabStore'
import { useConfigStore } from '../stores/useConfigStore'
import { useWorkspaceStore } from '../features/workspace/store'
import type { Pane, PaneContent, Tab, Workspace } from '../types/tab'
import type { ConfigData } from '../lib/host-api'
import { probeSessionCwd } from '../lib/rebuild/cwd-probe'
import { probeSessionProvenance } from '../lib/rebuild/provenance-probe'
import { fetchWsTicket } from '../lib/host-api'

const terminalViewProps = vi.hoisted(() => ({ last: undefined as Record<string, unknown> | undefined }))

vi.mock('./TerminalView', () => ({
  default: (props: Record<string, unknown>) => {
    terminalViewProps.last = props
    // The real view asks for its ticket as soon as it mounts; doing the same
    // here makes "no ticket was fetched" mean "nothing tried to attach".
    void (props.getTicket as () => Promise<string>)()
    return <div data-testid="terminal-view" />
  },
}))

vi.mock('./TerminatedPane', () => ({
  TerminatedPane: ({ content }: { content: { terminated: string } }) => (
    <div data-testid="terminated-pane">Terminated: {content.terminated}</div>
  ),
}))

vi.mock('../lib/host-api', async (orig) => ({
  ...(await orig<typeof import('../lib/host-api')>()),
  fetchWsTicket: vi.fn(async () => 'ticket'),
}))

vi.mock('../lib/rebuild/cwd-probe', () => ({ probeSessionCwd: vi.fn() }))
vi.mock('../lib/rebuild/provenance-probe', () => ({ probeSessionProvenance: vi.fn() }))

const HOST_ID = 'test-host'

const makePane = (overrides?: Partial<Pane>): Pane => ({
  id: 'pane-1',
  content: { kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' },
  ...overrides,
})

const defaultConfig: ConfigData = {
  bind: '0.0.0.0',
  port: 7860,
  detect: { cc_commands: [], poll_interval: 5 },
}

// A tmux-session pane as a pre-P-D.3 blob would have carried it. The type no
// longer admits 'stream' (tab-store persist v3 rewrites it on rehydrate), so
// the cast stands in for a value that reached the renderer without going
// through the migration — the renderer must still take the terminal path.
const legacyStreamContent = {
  kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001', mode: 'stream', cachedName: '', tmuxInstance: '',
} as unknown as PaneContent

function setupTabStore(pane: Pane) {
  const tab: Tab = {
    id: 'tab-1',
    pinned: false,
    locked: false,
    createdAt: Date.now(),
    layout: { type: 'leaf', pane },
  }
  useTabStore.setState({
    tabs: { 'tab-1': tab },
    tabOrder: ['tab-1'],
    activeTabId: 'tab-1',
  })
}

function makeWorkspace(id: string, tabs: string[]): Workspace {
  return {
    id,
    name: id,
    tabs,
    activeTabId: tabs[0] ?? null,
  }
}

beforeEach(() => {
  cleanup()
  vi.mocked(probeSessionCwd).mockClear()
  vi.mocked(probeSessionProvenance).mockClear()
  vi.mocked(fetchWsTicket).mockClear()
  terminalViewProps.last = undefined
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    activeHostId: HOST_ID,
    // The attach gate is open by default here; the probe waits on it
    // (spec §4.6.2) and its own suite covers the closed case.
    runtime: { [HOST_ID]: { status: 'connected' as const, attachReady: true } },
  })
  useSessionStore.setState({
    sessions: {
      [HOST_ID]: [{
        code: 'dev001', name: 'dev001', cwd: '/tmp', mode: 'terminal',
      }],
    },
    activeHostId: HOST_ID,
    activeCode: null,
  })
  useConfigStore.setState({ config: defaultConfig })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
})

describe('SessionPaneContent', () => {
  it('returns null for non-session pane content', () => {
    const pane: Pane = { id: 'pane-1', content: { kind: 'dashboard' } }
    setupTabStore(pane)
    const { container } = render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(container.innerHTML).toBe('')
  })

  it('renders TerminalView for terminal mode', () => {
    const pane = makePane()
    setupTabStore(pane)
    render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(screen.getByTestId('terminal-view')).toBeInTheDocument()
  })

  it('renders the terminal path for every live tmux-session pane, legacy stream mode included (P-D.3)', () => {
    const pane = makePane({ content: legacyStreamContent })
    setupTabStore(pane)
    render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(screen.getByTestId('terminal-view')).toBeInTheDocument()
    expect(screen.queryByTestId('conversation-view')).not.toBeInTheDocument()
    expect(terminalViewProps.last?.sessionCode).toBe('dev001')
  })

  it('renders TerminatedPane when content.terminated is set', () => {
    const pane = makePane({
      content: {
        kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
        mode: 'terminal', cachedName: 'my-session', tmuxInstance: '',
        terminated: 'session-closed',
      },
    })
    setupTabStore(pane)
    render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(screen.getByTestId('terminated-pane')).toBeInTheDocument()
    expect(screen.getByText('Terminated: session-closed')).toBeInTheDocument()
  })

  describe('TerminalView workspaceId plumbing (PR-5)', () => {
    it('passes workspaceId when pane belongs to a workspace tab', () => {
      const pane = makePane()
      setupTabStore(pane)
      useWorkspaceStore.setState({
        workspaces: [makeWorkspace('wsA', ['tab-1'])],
        activeWorkspaceId: 'wsA',
      })
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(terminalViewProps.last?.workspaceId).toBe('wsA')
    })

    it('passes undefined workspaceId for standalone tab (not in any workspace)', () => {
      const pane = makePane()
      setupTabStore(pane)
      useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(terminalViewProps.last?.workspaceId).toBeUndefined()
    })

    it('uses link-source workspace, not active workspace (multi-workspace isolation)', () => {
      const pane = makePane()
      setupTabStore(pane)
      useWorkspaceStore.setState({
        workspaces: [
          makeWorkspace('wsA', []),
          makeWorkspace('wsB', ['tab-1']),
        ],
        activeWorkspaceId: 'wsA',
      })
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(terminalViewProps.last?.workspaceId).toBe('wsB')
    })
  })

  // A pane opened after the session list has settled gets no further
  // broadcast, so attach is the second cwd-probe trigger (spec §4.4).
  describe('cwd probe on attach', () => {
    it('probes the pane binding once when a terminal pane attaches', () => {
      const pane = makePane({
        content: {
          kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
          mode: 'terminal', cachedName: '', tmuxInstance: '222:2000',
        },
      })
      setupTabStore(pane)
      const view = render(<SessionPaneContent pane={pane} isActive={true} />)
      view.rerender(<SessionPaneContent pane={pane} isActive={false} />)
      expect(probeSessionCwd).toHaveBeenCalledTimes(1)
      expect(probeSessionCwd).toHaveBeenCalledWith(HOST_ID, 'dev001', '222:2000')
    })

    // The mount trigger must respect the same gate as the terminal attach
    // (spec §4.6.2): a connection whose first `sessions` payload has not
    // landed has not proved which generation the pane's code belongs to.
    it('does not probe while the host attach gate is closed', () => {
      useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected' as const, attachReady: false } } })
      const pane = makePane({
        content: {
          kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
          mode: 'terminal', cachedName: '', tmuxInstance: '222:2000',
        },
      })
      setupTabStore(pane)
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(probeSessionCwd).not.toHaveBeenCalled()
    })

    it('probes as soon as the gate opens under the mounted pane', async () => {
      useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected' as const, attachReady: false } } })
      const pane = makePane({
        content: {
          kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
          mode: 'terminal', cachedName: '', tmuxInstance: '222:2000',
        },
      })
      setupTabStore(pane)
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(probeSessionCwd).not.toHaveBeenCalled()

      await act(async () => {
        useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected' as const, attachReady: true } } })
      })
      expect(probeSessionCwd).toHaveBeenCalledWith(HOST_ID, 'dev001', '222:2000')
    })

    it('does not probe a terminated pane', () => {
      const dead = makePane({
        content: {
          kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
          mode: 'terminal', cachedName: '', tmuxInstance: '222:2000',
          terminated: 'session-closed',
        },
      })
      setupTabStore(dead)
      render(<SessionPaneContent pane={dead} isActive={true} />)

      expect(probeSessionCwd).not.toHaveBeenCalled()
    })
  })

  // The third provenance trigger (spec §5.4): a pane opened after the session
  // list has settled gets no further `sessions` sweep and, until its agent
  // speaks, no hook broadcast either — so attach is the only thing that can
  // ask on its behalf.
  describe('provenance probe on attach', () => {
    it('asks who owns the pane binding when a terminal pane attaches', () => {
      const pane = makePane({
        content: {
          kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
          mode: 'terminal', cachedName: '', tmuxInstance: '222:2000',
        },
      })
      setupTabStore(pane)
      const view = render(<SessionPaneContent pane={pane} isActive={true} />)
      view.rerender(<SessionPaneContent pane={pane} isActive={false} />)
      expect(probeSessionProvenance).toHaveBeenCalledTimes(1)
      expect(probeSessionProvenance).toHaveBeenCalledWith(HOST_ID, 'dev001', '222:2000')
    })

    it('does not ask while the host attach gate is closed', () => {
      useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected' as const, attachReady: false } } })
      const pane = makePane({
        content: {
          kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
          mode: 'terminal', cachedName: '', tmuxInstance: '222:2000',
        },
      })
      setupTabStore(pane)
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(probeSessionProvenance).not.toHaveBeenCalled()
    })

    it('asks as soon as the gate opens under the mounted pane', async () => {
      useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected' as const, attachReady: false } } })
      const pane = makePane({
        content: {
          kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
          mode: 'terminal', cachedName: '', tmuxInstance: '222:2000',
        },
      })
      setupTabStore(pane)
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(probeSessionProvenance).not.toHaveBeenCalled()

      await act(async () => {
        useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected' as const, attachReady: true } } })
      })
      expect(probeSessionProvenance).toHaveBeenCalledWith(HOST_ID, 'dev001', '222:2000')
    })

    it('does not ask for a terminated pane', () => {
      const dead = makePane({
        content: {
          kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
          mode: 'terminal', cachedName: '', tmuxInstance: '222:2000',
          terminated: 'session-closed',
        },
      })
      setupTabStore(dead)
      render(<SessionPaneContent pane={dead} isActive={true} />)

      expect(probeSessionProvenance).not.toHaveBeenCalled()
    })
  })

  it('does not render TerminalView when terminated', () => {
    const pane = makePane({
      content: {
        kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'dev001',
        mode: 'terminal', cachedName: '', tmuxInstance: '',
        terminated: 'tmux-restarted',
      },
    })
    setupTabStore(pane)
    render(<SessionPaneContent pane={pane} isActive={true} />)
    expect(screen.queryByTestId('terminal-view')).not.toBeInTheDocument()
    expect(screen.getByTestId('terminated-pane')).toBeInTheDocument()
  })

  // Host ownership §3.2 / plan §0.4: a pane naming a host this device does not
  // have (an unresolvable wire id) is kept verbatim and shown as missing. It
  // must never reach the network — `getWsBase` falls back to the active host
  // for an unknown id, so attaching would open a terminal on the WRONG host.
  describe('host this device does not have', () => {
    const missing = (): Pane => makePane({
      content: {
        kind: 'tmux-session', hostId: 'd1_unknownhost', sessionCode: 'dev001',
        mode: 'terminal', cachedName: 'dev', tmuxInstance: '222:2000',
      },
    })

    it('renders the missing-host state naming the id, not a terminal', () => {
      const pane = missing()
      setupTabStore(pane)
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(screen.getByTestId('missing-host-pane')).toBeInTheDocument()
      expect(screen.getByText(/This device has no host d1_unknownhost/)).toBeInTheDocument()
      expect(screen.queryByTestId('terminal-view')).not.toBeInTheDocument()
      expect(terminalViewProps.last).toBeUndefined()
    })

    // H2c-2 T4: the look store names a host by wire id even when this device has no such host.
    it('names the host by the workbench look for that id when there is one', () => {
      useHostLookStore.setState({ looks: { d1_unknownhost: { name: 'air26' } } })
      try {
        const pane = missing()
        setupTabStore(pane)
        render(<SessionPaneContent pane={pane} isActive={true} />)
        expect(screen.getByText('This device has no host air26')).toBeInTheDocument()
      } finally {
        useHostLookStore.setState({ looks: {} })
      }
    })

    it('without a look entry for that id it names the id', () => {
      useHostLookStore.setState({ looks: { d1_otherhost: { name: 'air26' } } })
      try {
        const pane = missing()
        setupTabStore(pane)
        render(<SessionPaneContent pane={pane} isActive={true} />)
        expect(screen.getByText('This device has no host d1_unknownhost')).toBeInTheDocument()
      } finally {
        useHostLookStore.setState({ looks: {} })
      }
    })

    it('asks for no ticket and runs no probe, even with a gate entry for that id', () => {
      // A stale runtime entry must not open the probe gate for a host the
      // store no longer has.
      useHostStore.setState({
        runtime: {
          [HOST_ID]: { status: 'connected' as const, attachReady: true },
          d1_unknownhost: { status: 'connected' as const, attachReady: true },
        },
      })
      const pane = missing()
      setupTabStore(pane)
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(fetchWsTicket).not.toHaveBeenCalled()
      expect(probeSessionCwd).not.toHaveBeenCalled()
      expect(probeSessionProvenance).not.toHaveBeenCalled()
    })

    // R1 (PR #1400): an `in`-style lookup would take a prototype member for a host.
    it.each(['toString', 'constructor', '__proto__', 'hasOwnProperty'])('a pane naming the prototype member %s is a missing host, not a known one', (name) => {
      const pane = makePane({
        content: { kind: 'tmux-session', hostId: name, sessionCode: 'dev001', mode: 'terminal', cachedName: 'dev', tmuxInstance: '' },
      })
      setupTabStore(pane)
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(screen.getByTestId('missing-host-pane')).toBeInTheDocument()
      expect(screen.queryByTestId('terminal-view')).not.toBeInTheDocument()
      expect(fetchWsTicket).not.toHaveBeenCalled()
    })

    // PR #1400 (attacker, high): the host goes away after the pane rendered its terminal, while the terminal's
    // ticket request is still to come (a pending effect, a retry). The request must not fall back to another host.
    it('host deleted after render: the terminal\'s ticket request fetches nothing', async () => {
      const actual = await vi.importActual<typeof import('../lib/host-api')>('../lib/host-api')
      vi.mocked(fetchWsTicket).mockImplementation((id) => actual.fetchWsTicket(id))
      const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ ticket: 't' }), { status: 200 }))
      try {
        const pane = makePane()
        setupTabStore(pane)
        render(<SessionPaneContent pane={pane} isActive={true} />)
        const getTicket = terminalViewProps.last?.getTicket as () => Promise<string>
        await act(async () => {})
        fetchSpy.mockClear()
        useHostStore.setState({ hosts: {}, hostOrder: [], activeHostId: null })
        // a second host is active now: the fallback would pick it
        useHostStore.setState({ hosts: { other: { id: 'other', name: 'o', ip: '100.64.0.9', port: 7860, order: 0 } }, hostOrder: ['other'], activeHostId: 'other' })
        await expect(getTicket()).rejects.toThrow(/not configured/)
        expect(fetchSpy).not.toHaveBeenCalled()
      } finally {
        fetchSpy.mockRestore()
        vi.mocked(fetchWsTicket).mockImplementation(async () => 'ticket')
      }
    })

    it('an existing host-removed mark still renders as today', () => {
      const pane = makePane({
        content: {
          kind: 'tmux-session', hostId: 'd1_unknownhost', sessionCode: 'dev001',
          mode: 'terminal', cachedName: '', tmuxInstance: '', terminated: 'host-removed',
        },
      })
      setupTabStore(pane)
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(screen.getByTestId('terminated-pane')).toBeInTheDocument()
      expect(screen.queryByTestId('missing-host-pane')).not.toBeInTheDocument()
    })

    it('goes live once the host exists on this device', async () => {
      const pane = missing()
      setupTabStore(pane)
      render(<SessionPaneContent pane={pane} isActive={true} />)
      expect(screen.getByTestId('missing-host-pane')).toBeInTheDocument()
      await act(async () => {
        useHostStore.setState((s) => ({
          hosts: { ...s.hosts, d1_unknownhost: { id: 'd1_unknownhost', name: 'x', ip: '100.64.0.4', port: 7860, order: 1 } },
        }))
      })
      expect(screen.getByTestId('terminal-view')).toBeInTheDocument()
      expect(terminalViewProps.last?.wsUrl).toBe('ws://100.64.0.4:7860/ws/terminal/dev001')
    })
  })
})
