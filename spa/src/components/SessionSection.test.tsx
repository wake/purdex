// spa/src/components/SessionSection.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup, act } from '@testing-library/react'
import { HostSessionSection } from './SessionSection'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useAgentStore } from '../stores/useAgentStore'
import { emptyHostConfigEntry, useHostConfigStore } from '../stores/useHostConfigStore'
import { compositeKey } from '../lib/composite-key'
import type { HostProject } from '../lib/host-config-api'

vi.mock('../hooks/useSessionWatch', () => ({
  useSessionWatch: vi.fn(),
}))

vi.mock('../lib/host-api', async (importOriginal) => {
  const actual = (await importOriginal()) as Record<string, unknown>
  return { ...actual, listSessions: vi.fn().mockResolvedValue([]) }
})

// The launcher owns its own behaviour (its suite covers it); here we only care
// that the block mounts it for the right host, hands it a live `disabled`
// verdict, and acts on its callbacks. `real.value` swaps the stub for the real
// component in the one test that needs the whole post-create path.
const launcherProps = vi.hoisted(() => ({
  current: null as null | { hostId: string; disabled: boolean; onLaunched: (s: unknown) => void; onCancel: () => void },
}))
const real = vi.hoisted(() => ({ value: false }))
const launch = vi.hoisted(() => vi.fn())
vi.mock('../lib/session-launch', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/session-launch')>()),
  launchSession: launch,
}))
vi.mock('./session-launcher/SessionLauncher', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./session-launcher/SessionLauncher')>()
  return {
    SessionLauncher: (props: { hostId: string; disabled: boolean; onLaunched: (s: unknown) => void; onCancel: () => void }) => {
      launcherProps.current = props
      if (real.value) return <actual.SessionLauncher {...props} onLaunched={props.onLaunched as never} />
      return <div data-testid={`launcher-stub-${props.hostId}`} data-disabled={String(props.disabled)} />
    },
  }
})

const HOST_ID = 'test-host'
const HOST_B = 'host-b'
const PROJECT: HostProject = { id: 'p1', name: 'Purdex', slug: 'purdex', path: '~/w/purdex' }
const mockOnSelect = vi.fn()

/** Render one block per host, mirroring how NewTabPage lays out `sessions:<hostId>` providers. */
function Blocks() {
  const hostOrder = useHostStore((s) => s.hostOrder)
  return <>{hostOrder.map((h) => <HostSessionSection key={h} hostId={h} onSelect={mockOnSelect} />)}</>
}

beforeEach(() => {
  cleanup()
  mockOnSelect.mockClear()
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    activeHostId: HOST_ID,
  })
  useAgentStore.setState({ statuses: {}, agentTypes: {}, subagents: {}, unread: {} })
  useUISettingsStore.setState({ tabIndicatorStyle: 'badge', ccIconVariant: 'bot', codexIconVariant: 'openai' })
  launcherProps.current = null
  real.value = false
  launch.mockReset()
})

describe('SessionSection', () => {
  it('renders header + create button for a connected host with zero sessions', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected', tmuxState: 'ok' } } })
    render(<Blocks />)
    expect(screen.queryByText('No sessions available')).toBeNull()
    expect(screen.getByTestId(`new-session-${HOST_ID}`)).toBeInTheDocument()
  })

  it('renders nothing for a host id with no host record', () => {
    const { container } = render(<HostSessionSection hostId="gone" onSelect={mockOnSelect} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('disables the create button when the host tmux is unavailable', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected', tmuxState: 'unavailable' } } })
    render(<Blocks />)
    expect(screen.getByTestId(`new-session-${HOST_ID}`)).toBeDisabled()
  })

  it('disables the create button when the host has no runtime (offline)', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: {} }) // runtime undefined → Host-page rule treats as offline
    render(<Blocks />)
    expect(screen.getByTestId(`new-session-${HOST_ID}`)).toBeDisabled()
  })

  it('clicking the create button does not toggle collapse', () => {
    // multi-host so a collapse toggle exists
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1', port: 7860, order: 0 }, [HOST_B]: { id: HOST_B, name: 'air', ip: '2', port: 7860, order: 1 } },
      hostOrder: [HOST_ID, HOST_B], activeHostId: HOST_ID,
      runtime: { [HOST_ID]: { status: 'connected', tmuxState: 'ok' } },
    })
    useSessionStore.setState({ sessions: { [HOST_ID]: [{ code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal' }] } })
    render(<Blocks />)
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    expect(screen.getByTestId(`host-header-${HOST_ID}`)).toHaveAttribute('aria-expanded', 'true') // unchanged
    expect(screen.getByText('dev')).toBeInTheDocument()
  })

  it('renders session buttons', () => {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal' },
        ],
      },
    })
    render(<Blocks />)
    expect(screen.getByText('dev')).toBeInTheDocument()
  })

  it('shows the session pane title after the code', () => {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', pane_title: 'Reading memory' },
        ],
      },
    })
    render(<Blocks />)
    const row = screen.getByText('dev').closest('button') as HTMLElement
    expect(row).toHaveTextContent('Reading memory')
    // title trails the code within the row
    expect(row.textContent!.indexOf('abc001')).toBeLessThan(row.textContent!.indexOf('Reading memory'))
  })

  it('caps the name at half-width only when a pane title shares the row', () => {
    const LONG = 'a-very-long-session-name-that-would-overflow'
    // With a title: name is truncatable AND capped so the title keeps room.
    useSessionStore.setState({
      sessions: { [HOST_ID]: [
        { code: 'abc001', name: LONG, cwd: '/tmp', mode: 'terminal', pane_title: 'Reading memory' },
      ] },
    })
    const withTitle = render(<Blocks />)
    const capped = screen.getByText(LONG)
    expect(capped.className).toContain('truncate')
    expect(capped.className).toContain('max-w-[50%]')
    withTitle.unmount()

    // Without a title: name is still truncatable but NOT capped — it may use the
    // full remaining width instead of being stranded at half a row.
    useSessionStore.setState({
      sessions: { [HOST_ID]: [
        { code: 'abc001', name: LONG, cwd: '/tmp', mode: 'terminal' },
      ] },
    })
    render(<Blocks />)
    const uncapped = screen.getByText(LONG)
    expect(uncapped.className).toContain('truncate')
    expect(uncapped.className).not.toContain('max-w-[50%]')
  })

  it('renders the pane title at the same brightness as the code', () => {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', pane_title: 'Reading memory' },
        ],
      },
    })
    render(<Blocks />)
    const codeCls = screen.getByText('abc001').className
    const titleCls = screen.getByText('Reading memory').className
    // The title should use the same text colour token as the code.
    expect(codeCls).toContain('text-text-secondary')
    expect(titleCls).toContain('text-text-secondary')
    expect(titleCls).not.toContain('text-text-muted')
  })

  it('omits the title span when the session has no pane title', () => {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal' },
        ],
      },
    })
    render(<Blocks />)
    const row = screen.getByText('dev').closest('button') as HTMLElement
    expect(row).toHaveTextContent('dev')
    expect(row).toHaveTextContent('abc001')
  })

  it('calls onSelect when session is clicked', () => {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal' },
        ],
      },
    })
    render(<Blocks />)
    fireEvent.click(screen.getByText('dev'))
    expect(mockOnSelect).toHaveBeenCalledWith({
      kind: 'tmux-session',
      hostId: HOST_ID,
      sessionCode: 'abc001',
      mode: 'terminal',
      cachedName: 'dev',
      tmuxInstance: '',
    })
  })

  it('opens a pane carrying the selected session\'s generation', () => {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', tmux_instance: '222:2000' },
        ],
      },
    })
    render(<Blocks />)
    fireEvent.click(screen.getByText('dev'))
    expect(mockOnSelect).toHaveBeenCalledWith(expect.objectContaining({ tmuxInstance: '222:2000' }))
  })

  it('shows the collapse toggle even for a single host', () => {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal' },
        ],
      },
    })
    render(<Blocks />)
    const header = screen.getByTestId(`host-header-${HOST_ID}`)
    expect(header).toHaveAttribute('aria-expanded', 'true')
    fireEvent.click(header)
    expect(screen.queryByText('dev')).toBeNull()
    expect(screen.getByTestId(`new-session-${HOST_ID}`)).toBeInTheDocument()
  })

  it('styles the host header: bright caret, bold host name, filled + on the right edge', () => {
    useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected', tmuxState: 'ok' } } })
    render(<Blocks />)
    const header = screen.getByTestId(`host-header-${HOST_ID}`)
    const caret = header.querySelector('svg') as SVGElement
    expect(caret.getAttribute('class')).toContain('text-text-secondary')
    expect(caret.getAttribute('width')).toBe('12')
    const name = screen.getByText('mlab')
    expect(name.className).toContain('text-sm')
    expect(name.className).toContain('font-bold')
    expect(name.className).toContain('text-text-primary')
    const plus = screen.getByTestId(`new-session-${HOST_ID}`)
    // The "+" owns the right edge and reads as a real button: a solid accent
    // fill, not the old 15%-alpha tint.
    expect(plus.className).toContain('ml-auto')
    expect(plus.className).toContain('bg-accent')
    expect(plus.className).not.toContain('bg-accent/15')
    // "+" is last in the header row; the collapse toggle still ends with the name.
    expect(header.lastElementChild).toBe(name)
    expect(plus.parentElement!.lastElementChild).toBe(plus)
    expect(header.className).not.toContain('flex-1')
    expect(plus).not.toBeDisabled()
  })

  it('offline: the reconnecting label precedes the right-aligned +', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: { status: 'reconnecting' } } })
    render(<Blocks />)
    const plus = screen.getByTestId(`new-session-${HOST_ID}`)
    const row = plus.parentElement as HTMLElement
    const label = row.querySelector(':scope > span') as HTMLElement
    expect(label).not.toBeNull()
    // The label no longer claims the right edge — the "+" does.
    expect(label.className).not.toContain('ml-auto')
    const children = Array.from(row.children)
    expect(children.indexOf(label)).toBeLessThan(children.indexOf(plus))
    expect(row.lastElementChild).toBe(plus)
  })

  it('scopes j/k navigation to its own host block', () => {
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1', port: 7860, order: 0 }, [HOST_B]: { id: HOST_B, name: 'air', ip: '2', port: 7860, order: 1 } },
      hostOrder: [HOST_ID, HOST_B], activeHostId: HOST_ID,
    })
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [{ code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal' }],
        [HOST_B]: [{ code: 'xyz001', name: 'air-dev', cwd: '/tmp', mode: 'terminal' }],
      },
    })
    render(<Blocks />)
    const dev = screen.getByText('dev').closest('button') as HTMLElement
    dev.focus()
    fireEvent.keyDown(dev, { key: 'j' })
    expect(document.activeElement).toBe(dev) // last row of its block; does not jump into the next host
  })

  it('shows caret toggle on host header when multiple hosts', () => {
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 },
        [HOST_B]: { id: HOST_B, name: 'air', ip: '100.64.0.1', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, HOST_B],
      activeHostId: HOST_ID,
    })
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal' },
        ],
        [HOST_B]: [
          { code: 'xyz001', name: 'air-dev', cwd: '/tmp', mode: 'terminal' },
        ],
      },
    })
    render(<Blocks />)
    const headerA = screen.getByTestId(`host-header-${HOST_ID}`)
    const headerB = screen.getByTestId(`host-header-${HOST_B}`)
    expect(headerA).toBeInTheDocument()
    expect(headerB).toBeInTheDocument()
    expect(headerA).toHaveAttribute('aria-expanded', 'true')
    expect(headerB).toHaveAttribute('aria-expanded', 'true')
  })

  it('collapses host sessions on header click', () => {
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 },
        [HOST_B]: { id: HOST_B, name: 'air', ip: '100.64.0.1', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, HOST_B],
      activeHostId: HOST_ID,
    })
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal' },
        ],
        [HOST_B]: [
          { code: 'xyz001', name: 'air-dev', cwd: '/tmp', mode: 'terminal' },
        ],
      },
    })
    render(<Blocks />)
    fireEvent.click(screen.getByTestId(`host-header-${HOST_B}`))
    expect(screen.queryByText('air-dev')).toBeNull()
    expect(screen.getByTestId(`host-header-${HOST_B}`)).toHaveAttribute('aria-expanded', 'false')
    expect(screen.getByText('dev')).toBeInTheDocument()
  })

  it('expands collapsed host on second click', () => {
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 },
        [HOST_B]: { id: HOST_B, name: 'air', ip: '100.64.0.1', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, HOST_B],
      activeHostId: HOST_ID,
    })
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal' },
        ],
        [HOST_B]: [
          { code: 'xyz001', name: 'air-dev', cwd: '/tmp', mode: 'terminal' },
        ],
      },
    })
    render(<Blocks />)
    const headerB = screen.getByTestId(`host-header-${HOST_B}`)
    fireEvent.click(headerB)
    expect(screen.queryByText('air-dev')).toBeNull()
    fireEvent.click(headerB)
    expect(screen.getByText('air-dev')).toBeInTheDocument()
    expect(headerB).toHaveAttribute('aria-expanded', 'true')
  })

  it('allows collapsing any host including active', () => {
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 },
        [HOST_B]: { id: HOST_B, name: 'air', ip: '100.64.0.1', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, HOST_B],
      activeHostId: HOST_ID,
    })
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal' },
        ],
        [HOST_B]: [
          { code: 'xyz001', name: 'air-dev', cwd: '/tmp', mode: 'terminal' },
        ],
      },
    })
    render(<Blocks />)
    // SessionSection has no active host protection — any host can be collapsed
    const headerA = screen.getByTestId(`host-header-${HOST_ID}`)
    fireEvent.click(headerA)
    expect(screen.queryByText('dev')).toBeNull()
    expect(headerA).toHaveAttribute('aria-expanded', 'false')
  })

  it('keyboard nav skips collapsed host sessions', () => {
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 },
        [HOST_B]: { id: HOST_B, name: 'air', ip: '100.64.0.1', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, HOST_B],
      activeHostId: HOST_ID,
    })
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal' },
        ],
        [HOST_B]: [
          { code: 'xyz001', name: 'air-dev', cwd: '/tmp', mode: 'terminal' },
        ],
      },
    })
    render(<Blocks />)
    // Collapse HOST_B
    fireEvent.click(screen.getByTestId(`host-header-${HOST_B}`))
    // Only HOST_ID session buttons should be navigable
    const sessionButtons = screen.getAllByRole('button').filter((btn) => btn.hasAttribute('data-session-btn'))
    expect(sessionButtons).toHaveLength(1)
    expect(sessionButtons[0]).toHaveTextContent('dev')
  })

  it('renders the tab-style status indicator for a running-agent session', () => {
    useUISettingsStore.setState({ tabIndicatorStyle: 'dot' })
    const ck = compositeKey(HOST_ID, 'abc001')
    useAgentStore.setState({ statuses: { [ck]: 'running' }, agentTypes: { [ck]: 'cc' }, subagents: {}, unread: {} })
    useSessionStore.setState({
      sessions: { [HOST_ID]: [{ code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal' }] },
    })
    render(<Blocks />)
    // TabStatusIndicator renders a data-testid — assert the running indicator exists.
    expect(screen.getByTestId('tab-status-indicator')).toBeInTheDocument()
  })

  const LIVE = { status: 'connected', tmuxState: 'ok' } as const
  const made = (over: Partial<{ code: string; name: string }> = {}) =>
    ({ code: 'new001', name: 'built', cwd: '~', mode: 'terminal', ...over })

  it('+ opens the launcher for that host and toggles it closed', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: LIVE } })
    render(<Blocks />)
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    expect(screen.getByTestId(`launcher-stub-${HOST_ID}`)).toBeInTheDocument()
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    expect(screen.queryByTestId(`launcher-stub-${HOST_ID}`)).toBeNull()
  })

  it('a launched session attaches into the current pane with its generation and closes the launcher', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: LIVE } })
    render(<Blocks />)
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    act(() => launcherProps.current!.onLaunched({ ...made(), tmux_instance: '222:2000' }))
    expect(mockOnSelect).toHaveBeenCalledWith({ kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'new001', mode: 'terminal', cachedName: 'built', tmuxInstance: '222:2000' })
    expect(screen.queryByTestId(`launcher-stub-${HOST_ID}`)).toBeNull()
  })

  it('attaches with a blank generation when the daemon reported none', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: LIVE } })
    render(<Blocks />)
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    act(() => launcherProps.current!.onLaunched(made()))
    expect(mockOnSelect).toHaveBeenCalledWith(expect.objectContaining({ tmuxInstance: '' }))
  })

  // The whole path with the real launcher: the session IS created, and the host
  // drops before it can be attached. The launcher must stay on screen carrying
  // the reason instead of closing on a pane that never opened.
  it('host drops after the create resolves: the launcher stays open with an error and nothing attaches', async () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: LIVE } })
    useHostConfigStore.setState({
      byHost: { [HOST_ID]: { ...emptyHostConfigEntry('ready'), projects: [PROJECT], commands: [] } },
      ensureLoaded: vi.fn(async () => {}),
    })
    launch.mockImplementation(async () => {
      useHostStore.setState({ runtime: { [HOST_ID]: { status: 'disconnected' } } })
      return { status: 'created', session: made() }
    })
    real.value = true
    render(<Blocks />)
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    fireEvent.click(screen.getByTestId('launcher-project-name-p1'))
    expect(await screen.findByTestId('launcher-error')).toHaveTextContent('went offline')
    expect(screen.getByTestId('launcher')).toBeInTheDocument()
    expect(mockOnSelect).not.toHaveBeenCalled()
  })

  it('does not attach when the host went offline or was removed before the launch resolved', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: LIVE } })
    render(<Blocks />)
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    const { onLaunched } = launcherProps.current!
    act(() => { useHostStore.setState({ runtime: { [HOST_ID]: { status: 'disconnected' } } }) })
    act(() => onLaunched(made()))
    expect(mockOnSelect).not.toHaveBeenCalled()
  })

  // Ported from the removed inline form's regression test: a host that drops
  // while the create surface sits open must not stay launchable.
  it('disables the launcher when the host goes offline after it opens', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: LIVE } })
    render(<Blocks />)
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    expect(screen.getByTestId(`launcher-stub-${HOST_ID}`)).toHaveAttribute('data-disabled', 'false')
    act(() => { useHostStore.setState({ runtime: { [HOST_ID]: { status: 'disconnected' } } }) })
    expect(screen.getByTestId(`launcher-stub-${HOST_ID}`)).toHaveAttribute('data-disabled', 'true')
  })

  it('onCancel closes the launcher', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: LIVE } })
    render(<Blocks />)
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    act(() => launcherProps.current!.onCancel())
    expect(screen.queryByTestId(`launcher-stub-${HOST_ID}`)).toBeNull()
  })

  it('expands a collapsed host when its create button is clicked so the launcher is visible', () => {
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1', port: 7860, order: 0 }, [HOST_B]: { id: HOST_B, name: 'air', ip: '2', port: 7860, order: 1 } },
      hostOrder: [HOST_ID, HOST_B], activeHostId: HOST_ID,
      runtime: { [HOST_ID]: LIVE, [HOST_B]: LIVE },
    })
    useSessionStore.setState({ sessions: { [HOST_ID]: [], [HOST_B]: [] } })
    render(<Blocks />)
    fireEvent.click(screen.getByTestId(`host-header-${HOST_B}`)) // collapse HOST_B
    expect(screen.getByTestId(`host-header-${HOST_B}`)).toHaveAttribute('aria-expanded', 'false')
    fireEvent.click(screen.getByTestId(`new-session-${HOST_B}`)) // + must re-expand and show the launcher
    expect(screen.getByTestId(`launcher-stub-${HOST_B}`)).toBeInTheDocument()
    expect(screen.getByTestId(`host-header-${HOST_B}`)).toHaveAttribute('aria-expanded', 'true')
  })

})
