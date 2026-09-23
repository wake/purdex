// spa/src/components/hosts/SessionsSection.test.tsx
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useI18nStore } from '../../stores/useI18nStore'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { SessionsSection } from './SessionsSection'
import { useSessionStore } from '../../stores/useSessionStore'
import { useHostStore } from '../../stores/useHostStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { emptyHostConfigEntry, useHostConfigStore } from '../../stores/useHostConfigStore'
import { compositeKey } from '../../lib/composite-key'
import type { HostProject } from '../../lib/host-config-api'

const mockOpenSingletonTab = vi.fn(() => 'tab-1')
const mockSetActiveTab = vi.fn()
const mockInsertTab = vi.fn()

vi.mock('../../stores/useTabStore', () => ({
  useTabStore: {
    getState: () => ({
      openSingletonTab: mockOpenSingletonTab,
      setActiveTab: mockSetActiveTab,
    }),
  },
}))

vi.mock('../../stores/useWorkspaceStore', () => {
  const store = Object.assign(
    (selector: (s: Record<string, unknown>) => unknown) =>
      selector({ workspaces: [], insertTab: mockInsertTab }),
    {
      getState: () => ({
        insertTab: mockInsertTab,
        workspaces: [],
      }),
    },
  )
  return { useWorkspaceStore: store }
})

vi.mock('../../lib/host-api', () => ({
  hostFetch: vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve({}) }),
  renameSession: vi.fn().mockResolvedValue({ ok: true }),
}))

// The launcher has its own suite; this one only checks that the section mounts
// it for this host, keeps its `disabled` verdict live, and closes on callback.
// `real.value` swaps the stub for the real component where the whole
// post-create path is what matters.
const launcherProps = vi.hoisted(() => ({
  current: null as null | { hostId: string; disabled: boolean; onLaunched: (s: unknown) => void; onCancel: () => void },
}))
const real = vi.hoisted(() => ({ value: false }))
const launch = vi.hoisted(() => vi.fn())
vi.mock('../../lib/session-launch', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/session-launch')>()),
  launchSession: launch,
}))
vi.mock('../session-launcher/SessionLauncher', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../session-launcher/SessionLauncher')>()
  return {
    SessionLauncher: (props: { hostId: string; disabled: boolean; onLaunched: (s: unknown) => void; onCancel: () => void }) => {
      launcherProps.current = props
      if (real.value) return <actual.SessionLauncher {...props} onLaunched={props.onLaunched as never} />
      return <div data-testid={`launcher-stub-${props.hostId}`} data-disabled={String(props.disabled)} />
    },
  }
})

const HOST_ID = 'test-host'
const PROJECT: HostProject = { id: 'p1', name: 'Purdex', slug: 'purdex', path: '~/w/purdex' }
const SESSIONS = [
  { code: 'abc', name: 'dev', cwd: '/tmp', mode: 'terminal' },
]

beforeEach(() => {
  cleanup()
  mockOpenSingletonTab.mockClear()
  mockSetActiveTab.mockClear()
  mockInsertTab.mockClear()
  useSessionStore.setState({ sessions: { [HOST_ID]: SESSIONS } })
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } },
    activeHostId: HOST_ID,
  })
  useAgentStore.setState({ statuses: {} })
  launcherProps.current = null
  real.value = false
  launch.mockReset()
})

describe('SessionsSection', () => {
  it('shows "No sessions" when sessions list is empty', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    render(<SessionsSection hostId={HOST_ID} />)
    expect(screen.getByText('No sessions on this host')).toBeInTheDocument()
  })

  // Host never delivered a sessions payload (no token / unreachable):
  // `s.sessions[hostId]` is undefined. The selector must return a stable
  // reference or useSyncExternalStore loops until React throws
  // "Maximum update depth exceeded".
  it('does not loop when the host has no sessions entry at all (unloaded host)', () => {
    useSessionStore.setState({ sessions: {} })
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      runtime: {},
      activeHostId: HOST_ID,
    })
    expect(() => render(<SessionsSection hostId={HOST_ID} />)).not.toThrow()
    expect(screen.getByText('No sessions on this host')).toBeInTheDocument()
  })

  it('renders session table with name and cwd columns, no mode column (P-D.3)', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    // Column headers
    expect(screen.getByText('Session Name')).toBeInTheDocument()
    expect(screen.queryByText('Mode')).not.toBeInTheDocument()
    expect(screen.getByText('CWD')).toBeInTheDocument()
    // Session data
    expect(screen.getByText('dev')).toBeInTheDocument()
    expect(screen.queryByText('terminal')).not.toBeInTheDocument()
    expect(screen.getByText('/tmp')).toBeInTheDocument()
  })

  it('shows "New Session" button enabled when online', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    const btn = screen.getByRole('button', { name: /New Session/i })
    expect(btn).toBeInTheDocument()
    expect(btn).not.toBeDisabled()
  })

  it('shows "New Session" button disabled when offline', () => {
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      runtime: { [HOST_ID]: { status: 'disconnected' } },
      activeHostId: HOST_ID,
    })
    render(<SessionsSection hostId={HOST_ID} />)
    const btn = screen.getByRole('button', { name: /New Session/i })
    expect(btn).toBeDisabled()
  })

  it('renders agent status badge when agentStatuses has entry for session', () => {
    const ck = compositeKey(HOST_ID, 'abc')
    useAgentStore.setState({ statuses: { [ck]: 'running' } })
    render(<SessionsSection hostId={HOST_ID} />)
    expect(screen.getByText('running')).toBeInTheDocument()
  })

  describe('agent status badge is locale-aware', () => {
    afterEach(() => { useI18nStore.getState().setLocale('en') })

    it.each([
      ['running', '執行中'],
      ['waiting', '等待中'],
      ['idle', '閒置'],
      ['error', '錯誤'],
    ] as const)('%s renders in English for en and as %s for zh-TW', (status, zh) => {
      useAgentStore.setState({ statuses: { [compositeKey(HOST_ID, 'abc')]: status } })
      const { unmount } = render(<SessionsSection hostId={HOST_ID} />)
      expect(screen.getByText(status)).toBeInTheDocument()
      unmount()

      useI18nStore.getState().setLocale('zh-TW')
      render(<SessionsSection hostId={HOST_ID} />)
      expect(screen.getByText(zh)).toBeInTheDocument()
      expect(screen.queryByText(status)).not.toBeInTheDocument()
    })

    it('an unknown status from the wire still shows raw', () => {
      useAgentStore.setState({ statuses: { [compositeKey(HOST_ID, 'abc')]: 'compacting' as never } })
      useI18nStore.getState().setLocale('zh-TW')
      render(<SessionsSection hostId={HOST_ID} />)
      expect(screen.getByText('compacting')).toBeInTheDocument()
    })
  })

  it('renders dash when no agent status for session', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    // The "—" em-dash is shown when no agent status
    expect(screen.getByText('—')).toBeInTheDocument()
  })

  it('clicking Open calls openSingletonTab', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    const openBtn = screen.getByTitle('Open')
    fireEvent.click(openBtn)
    expect(mockOpenSingletonTab).toHaveBeenCalledWith({
      kind: 'tmux-session',
      hostId: HOST_ID,
      sessionCode: 'abc',
      mode: 'terminal',
      cachedName: 'dev',
      tmuxInstance: '',
    })
    expect(mockSetActiveTab).toHaveBeenCalledWith('tab-1')
  })

  it('clicking Open carries the session generation into the pane', () => {
    useSessionStore.setState({
      sessions: { [HOST_ID]: [{ ...SESSIONS[0], tmux_instance: '222:2000' }] },
    })
    render(<SessionsSection hostId={HOST_ID} />)
    fireEvent.click(screen.getByTitle('Open'))
    expect(mockOpenSingletonTab).toHaveBeenCalledWith(
      expect.objectContaining({ tmuxInstance: '222:2000' }),
    )
  })

  it('the header carries only the New Session button (no quick-command slot)', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    expect(screen.queryByRole('toolbar')).toBeNull()
    expect(screen.getByText('New Session')).toBeInTheDocument()
  })

  it('New Session opens the launcher for this host; launching closes it without opening a tab', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    fireEvent.click(screen.getByText('New Session'))
    expect(screen.getByTestId(`launcher-stub-${HOST_ID}`)).toBeInTheDocument()
    act(() => launcherProps.current!.onLaunched({ ...SESSIONS[0], code: 'new1' }))
    expect(screen.queryByTestId(`launcher-stub-${HOST_ID}`)).toBeNull()
    expect(mockOpenSingletonTab).not.toHaveBeenCalled() // same as the old dialog: create only
  })

  it('New Session toggles the launcher closed on a second click', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    fireEvent.click(screen.getByText('New Session'))
    fireEvent.click(screen.getByText('New Session'))
    expect(screen.queryByTestId(`launcher-stub-${HOST_ID}`)).toBeNull()
  })

  it('cancel closes the launcher', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    fireEvent.click(screen.getByText('New Session'))
    act(() => launcherProps.current!.onCancel())
    expect(screen.queryByTestId(`launcher-stub-${HOST_ID}`)).toBeNull()
  })

  // Same race as the New Tab entry point: the session IS created and the host
  // drops right after, so the launcher must stay on screen with the reason
  // rather than closing as if the launch had gone through cleanly.
  it('host drops after the create resolves: the launcher stays open with an error', async () => {
    useHostConfigStore.setState({
      byHost: { [HOST_ID]: { ...emptyHostConfigEntry('ready'), projects: [PROJECT], commands: [] } },
      ensureLoaded: vi.fn(async () => {}),
    })
    launch.mockImplementation(async () => {
      useHostStore.setState({ runtime: { [HOST_ID]: { status: 'disconnected' } } })
      return { status: 'created', session: { ...SESSIONS[0], code: 'new1' } }
    })
    real.value = true
    render(<SessionsSection hostId={HOST_ID} />)
    fireEvent.click(screen.getByText('New Session'))
    fireEvent.click(screen.getByTestId('launcher-project-name-p1'))
    expect(await screen.findByTestId('launcher-error')).toHaveTextContent('went offline')
    expect(screen.getByTestId('launcher')).toBeInTheDocument()
    expect(mockOpenSingletonTab).not.toHaveBeenCalled()
  })

  // Equivalent of the New Tab regression: the host may drop while the launcher
  // sits open, and the launcher must go dead with it.
  it('disables the launcher when the host goes offline after it opens', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    fireEvent.click(screen.getByText('New Session'))
    expect(screen.getByTestId(`launcher-stub-${HOST_ID}`)).toHaveAttribute('data-disabled', 'false')
    act(() => { useHostStore.setState({ runtime: { [HOST_ID]: { status: 'disconnected' } } }) })
    expect(screen.getByTestId(`launcher-stub-${HOST_ID}`)).toHaveAttribute('data-disabled', 'true')
  })
})
