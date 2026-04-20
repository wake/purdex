import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { Router } from 'wouter'
import { memoryLocation } from 'wouter/memory-location'
import { HostPage, resetLastHostSelection } from './HostPage'
import { useHostStore } from '../stores/useHostStore'
import type { Pane } from '../types/tab'

vi.mock('../lib/host-api', async (importOriginal) => {
  const actual = await importOriginal() as Record<string, unknown>
  return { ...actual, listSessions: vi.fn().mockResolvedValue([]) }
})

vi.mock('./hosts/HostSidebar', () => ({
  HostSidebar: (props: {
    selectedHostId: string
    selectedSubPage: string
    onSelect: (hostId: string, subPage: 'overview' | 'sessions' | 'hooks' | 'agents' | 'uploads' | 'logs') => void
  }) => (
    <div data-testid="host-sidebar" data-host={props.selectedHostId} data-subpage={props.selectedSubPage}>
      <button data-testid="select-test-host-logs" onClick={() => props.onSelect('test-host', 'logs')}>
        test-host logs
      </button>
      <button data-testid="select-second-host-sessions" onClick={() => props.onSelect('second-host', 'sessions')}>
        second-host sessions
      </button>
    </div>
  ),
}))
vi.mock('./hosts/OverviewSection', () => ({
  OverviewSection: (props: { hostId: string }) => <div data-testid="overview-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/SessionsSection', () => ({
  SessionsSection: (props: { hostId: string }) => <div data-testid="sessions-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/HooksSection', () => ({
  HooksSection: (props: { hostId: string }) => <div data-testid="hooks-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/AgentsSection', () => ({
  AgentsSection: (props: { hostId: string }) => <div data-testid="agents-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/UploadSection', () => ({
  UploadSection: (props: { hostId: string }) => <div data-testid="upload-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/LogsSection', () => ({
  LogsSection: (props: { hostId: string }) => <div data-testid="logs-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/AddHostDialog', () => ({
  AddHostDialog: (props: { onClose: () => void }) => <div data-testid="add-host-dialog" onClick={props.onClose} />,
}))

const TEST_HOST_ID = 'test-host'
const SECOND_HOST_ID = 'second-host'

const hostPane: Pane = {
  id: 'pane-hosts',
  content: { kind: 'hosts' },
}

function seedHosts(options?: { activeHostId?: string | null; hostOrder?: string[] }) {
  useHostStore.setState({
    hosts: {
      [TEST_HOST_ID]: { id: TEST_HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 },
      [SECOND_HOST_ID]: { id: SECOND_HOST_ID, name: 'Second Host', ip: '5.6.7.8', port: 7860, order: 1 },
    },
    hostOrder: options?.hostOrder ?? [TEST_HOST_ID, SECOND_HOST_ID],
    activeHostId: options?.activeHostId ?? TEST_HOST_ID,
    runtime: {},
  })
}

function renderHostPage(initialPath: string) {
  const mem = memoryLocation({ path: initialPath, record: true })
  const view = render(
    <Router hook={mem.hook}>
      <HostPage pane={hostPane} isActive />
    </Router>,
  )
  return { ...view, mem }
}

function currentPath(mem: ReturnType<typeof memoryLocation>) {
  return mem.history[mem.history.length - 1]
}

beforeEach(() => {
  resetLastHostSelection()
  seedHosts()
})

describe('HostPage', () => {
  it('mounts from a deep link on first paint', () => {
    renderHostPage('/hosts/test-host/logs')

    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'logs')
    expect(screen.getByTestId('logs-section')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(screen.queryByTestId('overview-section')).not.toBeInTheDocument()
  })

  it('remounts bare /hosts using the last valid selection', () => {
    const firstMount = renderHostPage('/hosts/test-host/logs')
    expect(screen.getByTestId('logs-section')).toHaveAttribute('data-host', TEST_HOST_ID)
    firstMount.unmount()

    const { mem } = renderHostPage('/hosts')

    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'logs')
    expect(screen.getByTestId('logs-section')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(currentPath(mem)).toBe('/hosts/test-host/logs')
  })

  it('canonicalizes an invalid host while preserving a valid subpage', () => {
    const { mem } = renderHostPage('/hosts/missing-host/logs')

    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'logs')
    expect(screen.getByTestId('logs-section')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(currentPath(mem)).toBe('/hosts/test-host/logs')
  })

  it('canonicalizes an extra hosts path while preserving a valid subpage', () => {
    const { mem } = renderHostPage('/hosts/test-host/logs/extra')

    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'logs')
    expect(screen.getByTestId('logs-section')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(currentPath(mem)).toBe('/hosts/test-host/logs')
  })

  it('preserves the current subpage when the selected host disappears', () => {
    const { rerender, mem } = renderHostPage('/hosts/test-host/logs')

    useHostStore.setState({
      hosts: {
        [SECOND_HOST_ID]: { id: SECOND_HOST_ID, name: 'Second Host', ip: '5.6.7.8', port: 7860, order: 0 },
      },
      hostOrder: [SECOND_HOST_ID],
      activeHostId: SECOND_HOST_ID,
    })

    rerender(
      <Router hook={mem.hook}>
        <HostPage pane={hostPane} isActive />
      </Router>,
    )

    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', SECOND_HOST_ID)
    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'logs')
    expect(screen.getByTestId('logs-section')).toHaveAttribute('data-host', SECOND_HOST_ID)
    expect(currentPath(mem)).toBe('/hosts/second-host/logs')
  })

  it('updates the URL when the sidebar selection changes', () => {
    const { mem } = renderHostPage('/hosts')

    fireEvent.click(screen.getByTestId('select-test-host-logs'))

    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-subpage', 'logs')
    expect(screen.getByTestId('logs-section')).toHaveAttribute('data-host', TEST_HOST_ID)
    expect(currentPath(mem)).toBe('/hosts/test-host/logs')
  })

  it('renders no-host state when the host list is empty', () => {
    useHostStore.setState({
      hosts: {},
      hostOrder: [],
      activeHostId: null,
    })

    renderHostPage('/hosts')

    expect(screen.getByText('No host selected.')).toBeInTheDocument()
  })
})
