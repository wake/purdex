// spa/src/components/hosts/SessionsSection.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { SessionsSection } from './SessionsSection'
import { useSessionStore } from '../../stores/useSessionStore'
import { useHostStore } from '../../stores/useHostStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { compositeKey } from '../../lib/composite-key'

const mockOpenSingletonTab = vi.fn(() => 'tab-1')
const mockSetActiveTab = vi.fn()
const mockAddTabToWorkspace = vi.fn()
const mockSetWorkspaceActiveTab = vi.fn()

vi.mock('../../stores/useTabStore', () => ({
  useTabStore: {
    getState: () => ({
      openSingletonTab: mockOpenSingletonTab,
      setActiveTab: mockSetActiveTab,
    }),
  },
}))

vi.mock('../../stores/useWorkspaceStore', () => ({
  useWorkspaceStore: {
    getState: () => ({
      activeWorkspaceId: null,
      addTabToWorkspace: mockAddTabToWorkspace,
      setWorkspaceActiveTab: mockSetWorkspaceActiveTab,
    }),
  },
}))

vi.mock('../../lib/host-api', () => ({
  hostFetch: vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve({}) }),
  renameSession: vi.fn().mockResolvedValue({ ok: true }),
}))

const HOST_ID = 'test-host'
const SESSIONS = [
  { code: 'abc', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
]

beforeEach(() => {
  cleanup()
  mockOpenSingletonTab.mockClear()
  mockSetActiveTab.mockClear()
  useSessionStore.setState({ sessions: { [HOST_ID]: SESSIONS } })
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } },
  })
  useAgentStore.setState({ statuses: {} })
})

describe('SessionsSection', () => {
  it('shows "No sessions" when sessions list is empty', () => {
    useSessionStore.setState({ sessions: { [HOST_ID]: [] } })
    render(<SessionsSection hostId={HOST_ID} />)
    expect(screen.getByText('No sessions on this host')).toBeInTheDocument()
  })

  it('renders session table with name, mode, cwd columns', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    // Column headers
    expect(screen.getByText('Session Name')).toBeInTheDocument()
    expect(screen.getByText('Mode')).toBeInTheDocument()
    expect(screen.getByText('CWD')).toBeInTheDocument()
    // Session data
    expect(screen.getByText('dev')).toBeInTheDocument()
    expect(screen.getByText('terminal')).toBeInTheDocument()
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
})
