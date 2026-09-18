import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { HostBadgePreview } from './HostBadgePreview'
import { useHostStore } from '../../stores/useHostStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'

// WorkspaceIcon fetches /icons/<weight>.json for a real Phosphor name — stub the
// cache so the icon-rendering test doesn't depend on that async load.
vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => 'M0,0L10,10',
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

const HOST_ID = 'h1'
const BLUE = { color: '#3b82f6', alpha: 100 }
const RED = { color: '#ef4444', alpha: 100 }

beforeEach(() => {
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: {},
  })
  useUISettingsStore.setState({ hostBadgeSidebarEnabled: true, hostBadgeSidebarLineColor: 'host', hostBadgeSidebarBox: 16, hostBadgeSidebarInset: 2, hostBadgeSidebarRadius: 4 })
})

describe('HostBadgePreview', () => {
  it('renders a normal and an active mock row with the host name and the real badge', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE)
    render(<HostBadgePreview hostId={HOST_ID} mode="console" />)
    const normal = screen.getByTestId('host-badge-preview-normal')
    const active = screen.getByTestId('host-badge-preview-active')
    expect(normal).toHaveAttribute('data-active', 'false')
    expect(active).toHaveAttribute('data-active', 'true')
    expect(normal.className).toContain('group')
    expect(active.className).toContain('group')
    expect(normal.textContent).toContain('mlab')
    const badge = screen.getByTestId('host-badge-preview-badge-normal')
    expect(badge).toHaveAttribute('data-host-badge')
    expect(badge.style.getPropertyValue('--hb-main')).toBe('rgba(59, 130, 246, 1)')
    expect(badge.style.getPropertyValue('--hb-middle')).toBe('rgba(59, 130, 246, 0.6)')
    expect(badge.style.background).toBe('rgba(59, 130, 246, 0.22)')
  })

  it('follows the selected mode', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE)
    useHostStore.getState().setHostColorLayer(HOST_ID, 'terminal', 'main', RED)
    render(<HostBadgePreview hostId={HOST_ID} mode="terminal" />)
    expect(screen.getByTestId('host-badge-preview-badge-active').style.getPropertyValue('--hb-main')).toBe('rgba(239, 68, 68, 1)')
  })

  it('uses the sidebar badge geometry and line color settings', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE)
    useUISettingsStore.setState({ hostBadgeSidebarBox: 20, hostBadgeSidebarRadius: 6, hostBadgeSidebarLineColor: 'neutral' })
    render(<HostBadgePreview hostId={HOST_ID} mode="console" />)
    const badge = screen.getByTestId('host-badge-preview-badge-normal')
    expect(badge.style.width).toBe('20px')
    expect(badge.style.borderRadius).toBe('6px')
    expect(badge.style.color).toBe('var(--text-muted)')
  })

  it('shows the rows without a badge plus a caption when the host has neither color nor icon', () => {
    render(<HostBadgePreview hostId={HOST_ID} mode="console" />)
    expect(screen.getByTestId('host-badge-preview-normal')).toBeInTheDocument()
    expect(screen.queryByTestId('host-badge-preview-badge-normal')).toBeNull()
    expect(screen.getByText('No color or icon set')).toBeInTheDocument()
  })

  it('renders the host icon in the badge', () => {
    useHostStore.getState().setHostIcon(HOST_ID, 'Laptop', 'duotone')
    render(<HostBadgePreview hostId={HOST_ID} mode="console" />)
    expect(screen.getByTestId('host-badge-preview-badge-normal').querySelector('svg')).not.toBeNull()
  })
})
