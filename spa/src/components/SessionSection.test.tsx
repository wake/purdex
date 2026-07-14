// spa/src/components/SessionSection.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react'
import { SessionSection } from './SessionSection'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useAgentStore } from '../stores/useAgentStore'
import { compositeKey } from '../lib/composite-key'
import * as hostApi from '../lib/host-api'

vi.mock('../hooks/useSessionWatch', () => ({
  useSessionWatch: vi.fn(),
}))

vi.mock('../lib/host-api', async (importOriginal) => {
  const actual = (await importOriginal()) as Record<string, unknown>
  return { ...actual, listSessions: vi.fn().mockResolvedValue([]), createSession: vi.fn() }
})

const HOST_ID = 'test-host'
const HOST_B = 'host-b'
const mockOnSelect = vi.fn()

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
  vi.mocked(hostApi.createSession).mockReset()
})

describe('SessionSection', () => {
  it('renders header + create button for a connected host with zero sessions', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected', tmuxState: 'ok' } } })
    render(<SessionSection onSelect={mockOnSelect} />)
    expect(screen.queryByText('No sessions available')).toBeNull()
    expect(screen.getByTestId(`new-session-${HOST_ID}`)).toBeInTheDocument()
  })

  it('shows the global empty message only when there are no hosts', () => {
    useHostStore.setState({ hosts: {}, hostOrder: [], activeHostId: null })
    render(<SessionSection onSelect={mockOnSelect} />)
    expect(screen.getByText('No sessions available')).toBeInTheDocument()
  })

  it('disables the create button when the host tmux is unavailable', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: { status: 'connected', tmuxState: 'unavailable' } } })
    render(<SessionSection onSelect={mockOnSelect} />)
    expect(screen.getByTestId(`new-session-${HOST_ID}`)).toBeDisabled()
  })

  it('disables the create button when the host has no runtime (offline)', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: {} }) // runtime undefined → Host-page rule treats as offline
    render(<SessionSection onSelect={mockOnSelect} />)
    expect(screen.getByTestId(`new-session-${HOST_ID}`)).toBeDisabled()
  })

  it('clicking the create button does not toggle collapse', () => {
    // multi-host so a collapse toggle exists
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1', port: 7860, order: 0 }, [HOST_B]: { id: HOST_B, name: 'air', ip: '2', port: 7860, order: 1 } },
      hostOrder: [HOST_ID, HOST_B], activeHostId: HOST_ID,
      runtime: { [HOST_ID]: { status: 'connected', tmuxState: 'ok' } },
    })
    useSessionStore.setState({ sessions: { [HOST_ID]: [{ code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false }] } })
    render(<SessionSection onSelect={mockOnSelect} />)
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    expect(screen.getByTestId(`host-header-${HOST_ID}`)).toHaveAttribute('aria-expanded', 'true') // unchanged
    expect(screen.getByText('dev')).toBeInTheDocument()
  })

  it('renders session buttons', () => {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
    })
    render(<SessionSection onSelect={mockOnSelect} />)
    expect(screen.getByText('dev')).toBeInTheDocument()
  })

  it('calls onSelect when session is clicked', () => {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
    })
    render(<SessionSection onSelect={mockOnSelect} />)
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

  it('does not show host header for single host', () => {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
    })
    render(<SessionSection onSelect={mockOnSelect} />)
    expect(screen.queryByTestId(`host-header-${HOST_ID}`)).toBeNull()
    expect(screen.getByTestId(`new-session-${HOST_ID}`)).toBeInTheDocument()
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
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
        [HOST_B]: [
          { code: 'xyz001', name: 'air-dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
    })
    render(<SessionSection onSelect={mockOnSelect} />)
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
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
        [HOST_B]: [
          { code: 'xyz001', name: 'air-dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
    })
    render(<SessionSection onSelect={mockOnSelect} />)
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
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
        [HOST_B]: [
          { code: 'xyz001', name: 'air-dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
    })
    render(<SessionSection onSelect={mockOnSelect} />)
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
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
        [HOST_B]: [
          { code: 'xyz001', name: 'air-dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
    })
    render(<SessionSection onSelect={mockOnSelect} />)
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
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
        [HOST_B]: [
          { code: 'xyz001', name: 'air-dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
    })
    render(<SessionSection onSelect={mockOnSelect} />)
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
      sessions: { [HOST_ID]: [{ code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false }] },
    })
    render(<SessionSection onSelect={mockOnSelect} />)
    // TabStatusIndicator renders a data-testid — assert the running indicator exists.
    expect(screen.getByTestId('tab-status-indicator')).toBeInTheDocument()
  })

  const LIVE = { status: 'connected', tmuxState: 'ok' } as const
  const made = (over: Partial<{ code: string; name: string }> = {}) =>
    ({ code: 'new001', name: 'built', cwd: '~', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false, ...over })

  it('creates a session and attaches it into the current pane', async () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: LIVE } })
    vi.mocked(hostApi.createSession).mockResolvedValue(made())
    render(<SessionSection onSelect={mockOnSelect} />)
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    fireEvent.change(screen.getByPlaceholderText('Session Name'), { target: { value: 'built' } })
    fireEvent.click(screen.getByText('Create'))
    await waitFor(() => expect(hostApi.createSession).toHaveBeenCalledWith(HOST_ID, 'built', '~', 'terminal'))
    await waitFor(() => expect(mockOnSelect).toHaveBeenCalledWith({ kind: 'tmux-session', hostId: HOST_ID, sessionCode: 'new001', mode: 'terminal', cachedName: 'built', tmuxInstance: '' }))
  })

  it('does not attach when the created session has a blank code', async () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: LIVE } })
    vi.mocked(hostApi.createSession).mockResolvedValue(made({ code: '' }))
    render(<SessionSection onSelect={mockOnSelect} />)
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    fireEvent.change(screen.getByPlaceholderText('Session Name'), { target: { value: 'built' } })
    fireEvent.click(screen.getByText('Create'))
    await screen.findByText('Create failed') // inline error, form stays open
    expect(screen.getByPlaceholderText('Session Name')).toBeInTheDocument()
    expect(mockOnSelect).not.toHaveBeenCalled()
  })

  it('does not attach when the host is removed while creating', async () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: LIVE } })
    vi.mocked(hostApi.createSession).mockImplementation(async () => {
      useHostStore.setState({ hosts: {}, hostOrder: [], activeHostId: null, runtime: {} }) // host vanishes mid-flight
      return made()
    })
    render(<SessionSection onSelect={mockOnSelect} />)
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    fireEvent.change(screen.getByPlaceholderText('Session Name'), { target: { value: 'built' } })
    fireEvent.click(screen.getByText('Create'))
    await waitFor(() => expect(hostApi.createSession).toHaveBeenCalled())
    await waitFor(() => expect(mockOnSelect).not.toHaveBeenCalled())
  })

  it('submits create only once on a double click', async () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    useHostStore.setState({ runtime: { [HOST_ID]: LIVE } })
    let resolve!: (v: ReturnType<typeof made>) => void
    vi.mocked(hostApi.createSession).mockReturnValue(new Promise((r) => { resolve = r }))
    render(<SessionSection onSelect={mockOnSelect} />)
    fireEvent.click(screen.getByTestId(`new-session-${HOST_ID}`))
    fireEvent.change(screen.getByPlaceholderText('Session Name'), { target: { value: 'built' } })
    const createBtn = screen.getByText('Create')
    fireEvent.click(createBtn)
    fireEvent.click(createBtn) // second click while in-flight must be a no-op
    resolve(made())
    await waitFor(() => expect(mockOnSelect).toHaveBeenCalled())
    expect(hostApi.createSession).toHaveBeenCalledTimes(1)
  })
})
