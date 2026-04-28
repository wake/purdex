// spa/src/components/hosts/SessionsSection.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { SessionsSection } from './SessionsSection'
import { useSessionStore } from '../../stores/useSessionStore'
import { useHostStore } from '../../stores/useHostStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { useQuickCommandStore } from '../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'
import { compositeKey } from '../../lib/composite-key'
import { clearModuleRegistry, registerModule } from '../../lib/module-registry'

const mockOpenSingletonTab = vi.fn(() => 'tab-1')
const mockSetActiveTab = vi.fn()
const mockInsertTab = vi.fn()
const mockFindWorkspaceByTab = vi.fn(() => null)

vi.mock('../../stores/useTabStore', () => ({
  useTabStore: {
    getState: () => ({
      openSingletonTab: mockOpenSingletonTab,
      setActiveTab: mockSetActiveTab,
      activeTabId: 'tab-host',
    }),
  },
}))

vi.mock('../../stores/useWorkspaceStore', () => {
  const store = Object.assign(
    (selector: (s: Record<string, unknown>) => unknown) =>
      selector({ workspaces: [], insertTab: mockInsertTab, findWorkspaceByTab: mockFindWorkspaceByTab }),
    {
      getState: () => ({
        insertTab: mockInsertTab,
        workspaces: [],
        findWorkspaceByTab: mockFindWorkspaceByTab,
      }),
    },
  )
  return { useWorkspaceStore: store }
})

vi.mock('../../lib/host-api', () => ({
  hostFetch: vi.fn().mockResolvedValue({ ok: true, json: () => Promise.resolve({}) }),
  renameSession: vi.fn().mockResolvedValue({ ok: true }),
  createSession: vi.fn(),
}))

vi.mock('../../lib/execute-command', () => ({
  executeCommand: vi.fn().mockResolvedValue(undefined),
}))

const HOST_ID = 'test-host'
const SESSIONS = [
  { code: 'abc', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
]

beforeEach(() => {
  cleanup()
  mockOpenSingletonTab.mockClear()
  mockSetActiveTab.mockClear()
  mockInsertTab.mockClear()
  mockFindWorkspaceByTab.mockReset()
  mockFindWorkspaceByTab.mockReturnValue(null)
  useSessionStore.setState({ sessions: { [HOST_ID]: SESSIONS } })
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } },
    activeHostId: HOST_ID,
  })
  useAgentStore.setState({ statuses: {} })
  // Reset Quick Commands store with explicit fields (feedback_zustand_harness_setstate.md)
  useQuickCommandStore.setState({ global: [], byHost: {}, bindings: {} })
  // Module registry — quick-commands needs to be a known disableable module so
  // <CommandSlot> / v1 <QuickCommandMenu> module-enabled gates pass.
  clearModuleRegistry()
  registerModule({ id: 'quick-commands', name: 'Quick Commands', disableable: true })
  // Reset module enabled overrides
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
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

describe('SessionsSection — v1 QuickCommandMenu removal (Phase 1c, Finding 4)', () => {
  beforeEach(() => {
    // Real store + real module-registry: feed a global command so the v1 menu's
    // useCommands() returns a non-empty list and the trigger button would
    // render IF the integration were still wired. Removing that wiring is what
    // this RED → GREEN test gates.
    useQuickCommandStore.setState({
      global: [{ id: 'cmd-row', name: 'RowCmd', command: 'echo r' }],
      byHost: {},
      bindings: {},
    })
  })

  it('does NOT render v1 QuickCommandMenu inside session rows (Phase 1c — moved to new-session adjacency)', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    // v1 QuickCommandMenu trigger uses title="Quick Commands"; testing-library
    // falls back to title for accessible name when aria-label is absent.
    expect(screen.queryAllByRole('button', { name: /quick commands/i }).length).toBe(0)
    // Double-safeguard via title queryByTitle (covers any aria fallback edge cases).
    expect(screen.queryByTitle('Quick Commands')).toBeNull()
  })
})
