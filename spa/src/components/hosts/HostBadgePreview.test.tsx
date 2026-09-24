import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { HostBadgePreview } from './HostBadgePreview'
import { useHostStore } from '../../stores/useHostStore'
import { useHostLookStore } from '../../stores/useHostLookStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { getIconPath } from '../../features/workspace/lib/icon-path-cache'

// WorkspaceIcon fetches /icons/<weight>.json for a real Phosphor name — stub the
// cache so icon-rendering tests don't depend on that async load. A real `vi.fn`
// (not a blanket "always return a path") so tests can assert which name/weight
// the preview actually asked for — e.g. that an invalid stored icon falls back
// to `DEFAULT_HOST_ICON` rather than silently dropping the badge's icon.
vi.mock('../../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: vi.fn((name: string, weight: string) => (name === 'Laptop' && weight === 'duotone' ? 'M0,0L10,10' : null)),
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

const HOST_ID = 'h1'
const BLUE = { color: '#3b82f6', alpha: 100 }
const RED = { color: '#ef4444', alpha: 100 }

beforeEach(() => {
  useHostLookStore.setState({ looks: {} })
  vi.clearAllMocks()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: {},
  })
  useUISettingsStore.setState({ hostBadgeSidebarEnabled: true, hostBadgeSidebarLineColor: 'host', hostBadgeSidebarBox: 16, hostBadgeSidebarInset: 2, hostBadgeSidebarRadius: 4 })
})

describe('HostBadgePreview', () => {
  it('renders normal, hover and active mock rows with the host name and the real badge', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE)
    render(<HostBadgePreview hostId={HOST_ID} mode="console" />)
    const normal = screen.getByTestId('host-badge-preview-normal')
    const hover = screen.getByTestId('host-badge-preview-hover')
    const active = screen.getByTestId('host-badge-preview-active')
    expect(normal).toHaveAttribute('data-active', 'false')
    expect(hover).toHaveAttribute('data-active', 'true')
    expect(active).toHaveAttribute('data-active', 'true')
    expect(normal.className).toContain('group')
    expect(hover.className).toContain('group')
    expect(active.className).toContain('group')
    expect(normal.textContent).toContain('mlab')
    const badge = screen.getByTestId('host-badge-preview-badge-normal')
    expect(badge).toHaveAttribute('data-host-badge')
    expect(badge.style.getPropertyValue('--hb-main')).toBe('rgba(59, 130, 246, 1)')
    expect(badge.style.getPropertyValue('--hb-middle')).toBe('rgba(59, 130, 246, 0.6)')
    expect(badge.style.background).toBe('rgba(59, 130, 246, 0.22)')
  })

  it('wraps the rows in a container over the same surface as the real sidebar', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE)
    render(<HostBadgePreview hostId={HOST_ID} mode="console" />)
    const normal = screen.getByTestId('host-badge-preview-normal')
    const surface = normal.closest('[data-testid="host-badge-preview-surface"]')
    expect(surface).not.toBeNull()
    expect(surface?.className).toContain('bg-surface-tertiary')
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
    expect(getIconPath).toHaveBeenCalledWith('Laptop', 'duotone')
  })

  it('falls back to the default icon when the stored icon is invalid', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE)
    useHostStore.setState({
      hosts: { [HOST_ID]: { ...useHostStore.getState().hosts[HOST_ID], icon: 'NotAnIcon' } },
    })
    render(<HostBadgePreview hostId={HOST_ID} mode="console" />)
    expect(getIconPath).toHaveBeenCalledWith('Desktop', 'regular')
  })

  it('mock rows use the real inline-tab row classes, not a copy', () => {
    useHostStore.getState().setHostColorLayer(HOST_ID, 'console', 'main', BLUE)
    render(<HostBadgePreview hostId={HOST_ID} mode="console" />)
    const normal = screen.getByTestId('host-badge-preview-normal')
    const hover = screen.getByTestId('host-badge-preview-hover')
    const active = screen.getByTestId('host-badge-preview-active')
    expect(normal.className).toContain('hover:bg-surface-hover')
    expect(normal.className).not.toContain('bg-surface-secondary')
    expect(hover.className).toContain('bg-surface-hover')
    expect(hover.className).toContain('text-text-primary')
    expect(hover.className).not.toContain('hover:')
    expect(active.className).toContain('bg-surface-active')
  })
})
