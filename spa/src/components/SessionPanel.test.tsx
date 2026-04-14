// spa/src/components/SessionPanel.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import SessionPanel from './SessionPanel'
import { useSessionStore } from '../stores/useSessionStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useHostStore } from '../stores/useHostStore'
import { compositeKey } from '../lib/composite-key'

vi.mock('../lib/host-api', async (importOriginal) => {
  const actual = await importOriginal() as Record<string, unknown>
  return { ...actual, listSessions: vi.fn().mockResolvedValue([]) }
})

const HOST_ID = 'test-host'
const HOST_B = 'host-b'

beforeEach(() => {
  cleanup()
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  useAgentStore.setState({ statuses: {}, lastEvents: {}, unread: {} })
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    activeHostId: HOST_ID,
  })
})

describe('SessionPanel', () => {
  it('shows empty state', () => {
    render(<SessionPanel />)
    expect(screen.getByText('No sessions')).toBeInTheDocument()
  })

  it('renders session list', () => {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
          { code: 'abc002', name: 'prod', cwd: '/tmp', mode: 'stream', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
      activeHostId: HOST_ID,
      activeCode: null,
    })
    render(<SessionPanel />)
    expect(screen.getByText('dev')).toBeInTheDocument()
    expect(screen.getByText('prod')).toBeInTheDocument()
  })

  it('highlights active session', () => {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
      activeHostId: HOST_ID,
      activeCode: 'abc001',
    })
    render(<SessionPanel />)
    const btn = screen.getByRole('button', { name: /dev/i })
    expect(btn.className).toContain('bg-surface-secondary')
  })

  it('sets active on click', () => {
    const setActive = vi.fn()
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
      activeHostId: HOST_ID,
      activeCode: null,
      setActive,
    })
    render(<SessionPanel />)
    fireEvent.click(screen.getByRole('button', { name: /dev/i }))
    expect(setActive).toHaveBeenCalledWith(HOST_ID, 'abc001')
  })

  it('shows terminal icon for term mode', () => {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
      activeHostId: HOST_ID,
      activeCode: null,
    })
    render(<SessionPanel />)
    // Terminal icon should be present (Phosphor Terminal icon)
    expect(screen.getByTestId('session-icon-abc001')).toBeInTheDocument()
  })

  it('shows agent status badge when agent is active', () => {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
      activeHostId: HOST_ID,
      activeCode: null,
    })
    // Set agent status with composite key
    const ck = compositeKey(HOST_ID, 'abc001')
    useAgentStore.setState({ statuses: { [ck]: 'idle' } })
    render(<SessionPanel />)
    expect(screen.getByTestId('status-badge')).toHaveAttribute('title', 'idle')
  })

  it('shows no badge when no agent status exists for session', () => {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'stream', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
      activeHostId: HOST_ID,
      activeCode: null,
    })
    // No agent status set
    render(<SessionPanel />)
    expect(screen.queryByTestId('status-badge')).toBeNull()
  })

  it('does not show host header for single host', () => {
    useSessionStore.setState({
      sessions: {
        [HOST_ID]: [
          { code: 'abc001', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
        ],
      },
      activeHostId: HOST_ID,
      activeCode: null,
    })
    render(<SessionPanel />)
    expect(screen.queryByTestId(`host-header-${HOST_ID}`)).toBeNull()
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
      activeHostId: HOST_ID,
      activeCode: null,
    })
    render(<SessionPanel />)
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
      activeHostId: HOST_ID,
      activeCode: null,
    })
    render(<SessionPanel />)
    // Click HOST_B header to collapse it
    fireEvent.click(screen.getByTestId(`host-header-${HOST_B}`))
    // HOST_B sessions should be hidden
    expect(screen.queryByText('air-dev')).toBeNull()
    // HOST_B header should show collapsed state
    expect(screen.getByTestId(`host-header-${HOST_B}`)).toHaveAttribute('aria-expanded', 'false')
    // HOST_ID sessions should still be visible
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
      activeHostId: HOST_ID,
      activeCode: null,
    })
    render(<SessionPanel />)
    const headerB = screen.getByTestId(`host-header-${HOST_B}`)
    // Click to collapse
    fireEvent.click(headerB)
    expect(screen.queryByText('air-dev')).toBeNull()
    // Click again to expand
    fireEvent.click(headerB)
    expect(screen.getByText('air-dev')).toBeInTheDocument()
    expect(headerB).toHaveAttribute('aria-expanded', 'true')
  })

  it('prevents collapsing the active host', () => {
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
      activeHostId: HOST_ID,
      activeCode: 'abc001',
    })
    render(<SessionPanel />)
    const headerA = screen.getByTestId(`host-header-${HOST_ID}`)
    // Try to collapse the active host
    fireEvent.click(headerA)
    // Sessions should still be visible
    expect(screen.getByText('dev')).toBeInTheDocument()
    expect(headerA).toHaveAttribute('aria-expanded', 'true')
  })

  it('auto-expands new active host even if it was previously collapsed', () => {
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
      activeHostId: HOST_ID,
      activeCode: 'abc001',
    })
    const { rerender } = render(<SessionPanel />)
    // Collapse HOST_B
    fireEvent.click(screen.getByTestId(`host-header-${HOST_B}`))
    expect(screen.queryByText('air-dev')).toBeNull()

    // Switch activeHostId to HOST_B
    useSessionStore.setState({ activeHostId: HOST_B, activeCode: 'xyz001' })
    rerender(<SessionPanel />)

    // HOST_B should auto-expand because it is now the active host
    expect(screen.getByText('air-dev')).toBeInTheDocument()
    expect(screen.getByTestId(`host-header-${HOST_B}`)).toHaveAttribute('aria-expanded', 'true')
  })
})
