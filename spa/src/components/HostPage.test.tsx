// spa/src/components/HostPage.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { HostPage } from './HostPage'
import { useHostStore } from '../stores/useHostStore'
import type { Pane } from '../types/tab'

vi.mock('../lib/host-api', async (importOriginal) => {
  const actual = await importOriginal() as Record<string, unknown>
  return { ...actual, listSessions: vi.fn().mockResolvedValue([]) }
})

vi.mock('./hosts/HostSidebar', () => ({
  HostSidebar: (props: { selectedHostId: string; selectedSubPage: string }) => <div data-testid="host-sidebar" data-host={props.selectedHostId} data-subpage={props.selectedSubPage} />,
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
vi.mock('./hosts/UploadSection', () => ({
  UploadSection: (props: { hostId: string }) => <div data-testid="upload-section" data-host={props.hostId} />,
}))
vi.mock('./hosts/AddHostDialog', () => ({
  AddHostDialog: (props: { onClose: () => void }) => <div data-testid="add-host-dialog" onClick={props.onClose} />,
}))

const HOST_ID = 'test-host'

const hostPane: Pane = {
  id: 'pane-hosts',
  content: { kind: 'hosts' },
}

beforeEach(() => {
  cleanup()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    activeHostId: HOST_ID,
    runtime: {},
  })
})

describe('HostPage', () => {
  it('renders sidebar and content area', () => {
    render(<HostPage pane={hostPane} isActive />)
    expect(screen.getByTestId('host-sidebar')).toBeInTheDocument()
    // Default sub-page is overview
    expect(screen.getByTestId('overview-section')).toBeInTheDocument()
  })

  it('shows OverviewSection when overview sub-page selected', () => {
    render(<HostPage pane={hostPane} isActive />)
    const overview = screen.getByTestId('overview-section')
    expect(overview).toBeInTheDocument()
    expect(overview).toHaveAttribute('data-host', HOST_ID)
  })

  it('passes correct hostId to sidebar', () => {
    render(<HostPage pane={hostPane} isActive />)
    const sidebar = screen.getByTestId('host-sidebar')
    expect(sidebar).toHaveAttribute('data-host', HOST_ID)
    expect(sidebar).toHaveAttribute('data-subpage', 'overview')
  })

  it('shows "No host selected" when hostOrder is empty', () => {
    useHostStore.setState({
      hosts: {},
      hostOrder: [],
      activeHostId: null,
    })
    render(<HostPage pane={hostPane} isActive />)
    expect(screen.getByText('No host selected.')).toBeInTheDocument()
  })

  it('effectiveSelection falls back when selected host is deleted', () => {
    const SECOND_HOST = 'second-host'
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 },
        [SECOND_HOST]: { id: SECOND_HOST, name: 'Second Host', ip: '5.6.7.8', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, SECOND_HOST],
      activeHostId: HOST_ID,
    })

    const { rerender } = render(<HostPage pane={hostPane} isActive />)
    // Initially selected host is HOST_ID
    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', HOST_ID)

    // Simulate host deletion: remove HOST_ID, only SECOND_HOST remains
    useHostStore.setState({
      hosts: { [SECOND_HOST]: { id: SECOND_HOST, name: 'Second Host', ip: '5.6.7.8', port: 7860, order: 0 } },
      hostOrder: [SECOND_HOST],
      activeHostId: SECOND_HOST,
    })

    rerender(<HostPage pane={hostPane} isActive />)
    // effectiveSelection should fall back to the first remaining host
    expect(screen.getByTestId('host-sidebar')).toHaveAttribute('data-host', SECOND_HOST)
    expect(screen.getByTestId('overview-section')).toHaveAttribute('data-host', SECOND_HOST)
  })

  it('falls back to "No host selected" when all hosts are removed', () => {
    render(<HostPage pane={hostPane} isActive />)
    expect(screen.getByTestId('overview-section')).toBeInTheDocument()

    // Remove all hosts
    useHostStore.setState({
      hosts: {},
      hostOrder: [],
      activeHostId: null,
    })

    // Re-render to trigger effectiveSelection recalculation
    cleanup()
    render(<HostPage pane={hostPane} isActive />)
    expect(screen.getByText('No host selected.')).toBeInTheDocument()
  })
})
