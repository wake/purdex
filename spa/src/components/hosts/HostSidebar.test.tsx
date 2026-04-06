// spa/src/components/hosts/HostSidebar.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { HostSidebar } from './HostSidebar'
import { useHostStore } from '../../stores/useHostStore'

vi.mock('../../lib/host-api', async (importOriginal) => {
  const actual = await importOriginal() as Record<string, unknown>
  return { ...actual, listSessions: vi.fn().mockResolvedValue([]) }
})

const HOST_ID = 'test-host'
const HOST_B = 'host-b'

beforeEach(() => {
  cleanup()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: {},
  })
})

describe('HostSidebar', () => {
  const defaultProps = {
    selectedHostId: HOST_ID,
    selectedSubPage: 'overview' as const,
    onSelect: vi.fn(),
    onAddHost: vi.fn(),
  }

  beforeEach(() => {
    defaultProps.onSelect = vi.fn()
    defaultProps.onAddHost = vi.fn()
  })

  it('renders host names from store', () => {
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 },
        [HOST_B]: { id: HOST_B, name: 'Second Host', ip: '5.6.7.8', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, HOST_B],
    })
    render(<HostSidebar {...defaultProps} />)
    expect(screen.getByText('Test Host')).toBeInTheDocument()
    expect(screen.getByText('Second Host')).toBeInTheDocument()
  })

  it('shows StatusIcon green for connected runtime', () => {
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      runtime: { [HOST_ID]: { status: 'connected' } },
    })
    render(<HostSidebar {...defaultProps} />)
    // The host button contains a Circle SVG with text-green-400
    const hostButton = screen.getByText('Test Host').closest('button')!
    const svgs = hostButton.querySelectorAll('svg')
    const greenIcon = Array.from(svgs).find((svg) => svg.classList.contains('text-green-400'))
    expect(greenIcon).toBeTruthy()
  })

  it('shows StatusIcon grey for undefined runtime', () => {
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      runtime: {},
    })
    render(<HostSidebar {...defaultProps} />)
    const hostButton = screen.getByText('Test Host').closest('button')!
    const svgs = hostButton.querySelectorAll('svg')
    const mutedIcon = Array.from(svgs).find((svg) => svg.classList.contains('text-text-muted'))
    expect(mutedIcon).toBeTruthy()
  })

  it('shows StatusIcon red for disconnected runtime', () => {
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      runtime: { [HOST_ID]: { status: 'disconnected' } },
    })
    render(<HostSidebar {...defaultProps} />)
    const hostButton = screen.getByText('Test Host').closest('button')!
    const svgs = hostButton.querySelectorAll('svg')
    const redIcon = Array.from(svgs).find((svg) => svg.classList.contains('text-red-400'))
    expect(redIcon).toBeTruthy()
  })

  it('clicking a collapsed host expands it and shows sub-pages', () => {
    // HOST_B is not selectedHostId, so it starts collapsed
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 },
        [HOST_B]: { id: HOST_B, name: 'Second Host', ip: '5.6.7.8', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, HOST_B],
      runtime: {},
    })
    render(<HostSidebar {...defaultProps} />)

    // Second host should be collapsed — sub-pages not visible for it
    // The selected host (HOST_ID) starts expanded, so its sub-pages are visible
    const overviewButtons = screen.getAllByText('Overview')
    expect(overviewButtons).toHaveLength(1) // only from HOST_ID

    // Click the collapsed host
    fireEvent.click(screen.getByText('Second Host'))

    // Now both hosts should show sub-pages
    const allOverview = screen.getAllByText('Overview')
    expect(allOverview).toHaveLength(2)
  })

  it('clicking "Add Host" button calls onAddHost', () => {
    render(<HostSidebar {...defaultProps} />)
    fireEvent.click(screen.getByText('Add Host'))
    expect(defaultProps.onAddHost).toHaveBeenCalledTimes(1)
  })

  it('sub-page items are clickable and call onSelect', () => {
    render(<HostSidebar {...defaultProps} />)
    // The selected host is expanded by default, so sub-pages are visible
    fireEvent.click(screen.getByText('Sessions'))
    expect(defaultProps.onSelect).toHaveBeenCalledWith(HOST_ID, 'sessions')

    fireEvent.click(screen.getByText('Hooks'))
    expect(defaultProps.onSelect).toHaveBeenCalledWith(HOST_ID, 'hooks')

    fireEvent.click(screen.getByText('Uploads'))
    expect(defaultProps.onSelect).toHaveBeenCalledWith(HOST_ID, 'uploads')
  })

  it('shows Hosts title', () => {
    render(<HostSidebar {...defaultProps} />)
    expect(screen.getByText('Hosts')).toBeInTheDocument()
  })

  it('auto-expands new selectedHostId on prop change (e.g. host deletion fallback)', () => {
    useHostStore.setState({
      hosts: {
        [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 },
        [HOST_B]: { id: HOST_B, name: 'Second Host', ip: '5.6.7.8', port: 7860, order: 1 },
      },
      hostOrder: [HOST_ID, HOST_B],
      runtime: {},
    })

    // Initial: HOST_ID selected and expanded, HOST_B collapsed
    const { rerender } = render(<HostSidebar {...defaultProps} />)
    expect(screen.getAllByText('Overview')).toHaveLength(1) // only HOST_ID expanded

    // Collapse HOST_ID first by clicking it, then HOST_B remains collapsed
    fireEvent.click(screen.getByText('Test Host'))
    // Now both should be collapsed — no sub-pages visible
    expect(screen.queryAllByText('Overview')).toHaveLength(0)

    // Simulate host deletion fallback: selectedHostId changes to HOST_B
    rerender(<HostSidebar {...defaultProps} selectedHostId={HOST_B} />)

    // HOST_B should now be auto-expanded, showing its sub-pages
    // With no useEffect, HOST_B stays collapsed → Overview count stays 0 → test fails
    expect(screen.getAllByText('Overview')).toHaveLength(1)
    expect(screen.getAllByText('Sessions')).toHaveLength(1)
  })
})
