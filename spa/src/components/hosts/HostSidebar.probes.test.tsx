// F-2d: the 「觀察主機」 group of the Hosts page's sidebar.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react'
import { HostSidebar } from './HostSidebar'
import { useHostStore } from '../../stores/useHostStore'
import { useFleetStore } from '../../lib/fleet/store'
import { clearContributions } from '../../lib/settings-contribution-registry'
import { clearModuleRegistry } from '../../lib/module-registry'
import { registerBuiltinModules } from '../../lib/register-modules'

vi.mock('../../lib/host-api', async (importOriginal) => {
  const actual = await importOriginal() as Record<string, unknown>
  return { ...actual, listSessions: vi.fn().mockResolvedValue([]) }
})

const HOST_ID = 'test-host'
const probe = (id: string, name: string, status: 'ok' | 'offline' | 'error') => ({
  id, kind: 'probe', alias: id, name, icon: 'Desktop', os: '', status, error: null, last_ok_at: null, fast: null, slow: null,
})
const props = (extra: Record<string, unknown> = {}) => ({ selectedHostId: HOST_ID, selectedSubPage: 'overview' as const, onSelect: vi.fn(), onAddHost: vi.fn(), ...extra })

beforeEach(() => {
  cleanup(); clearContributions(); clearModuleRegistry(); registerBuiltinModules()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'Test Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: {},
  })
  useFleetStore.setState({
    byHost: { [HOST_ID]: { gen: 1, support: 'yes', term: 0 } } as never,
    snapshot: {
      hostId: HOST_ID, term: 1, order: ['p1', 'p2', 'p3'],
      hosts: { p1: probe('p1', 'air19', 'ok'), p2: probe('p2', 'mserv', 'offline'), p3: probe('p3', 'nas', 'error') },
    } as never,
  })
})
afterEach(() => { useFleetStore.getState().reset(); clearContributions(); clearModuleRegistry() })

describe('HostSidebar — observed hosts', () => {
  it('lists them under 「觀察主機」 after the paired hosts, with a status dot each', () => {
    render(<HostSidebar {...props()} />)
    const group = screen.getByTestId('probe-group')
    expect(group.textContent).toContain('Observed hosts')
    expect(within(screen.getByTestId('probe-item-p1')).getByTestId('probe-status').getAttribute('data-status')).toBe('ok')
    expect(within(screen.getByTestId('probe-item-p2')).getByTestId('probe-status').getAttribute('data-status')).toBe('offline')
    expect(within(screen.getByTestId('probe-item-p3')).getByTestId('probe-status').getAttribute('data-status')).toBe('error')
    expect(screen.getByText('Test Host').compareDocumentPosition(group) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('selecting a row selects probe:<id> with no sub-page', () => {
    const p = props()
    render(<HostSidebar {...p} />)
    fireEvent.click(screen.getByTestId('probe-item-p2'))
    expect(p.onSelect).toHaveBeenCalledWith('probe:p2', '')
  })

  it('the selected row is marked and the others are not', () => {
    render(<HostSidebar {...props({ selectedHostId: '', selectedProbeId: 'p1' })} />)
    expect(screen.getByTestId('probe-item-p1').getAttribute('aria-current')).toBe('true')
    expect(screen.getByTestId('probe-item-p2').getAttribute('aria-current')).toBeNull()
  })

  it('the heading collapses and opens the group, also while a row is selected', () => {
    render(<HostSidebar {...props({ selectedHostId: '', selectedProbeId: 'p1' })} />)
    fireEvent.click(screen.getByTestId('probe-group-toggle'))
    expect(screen.queryByTestId('probe-item-p1')).toBeNull()
    fireEvent.click(screen.getByTestId('probe-group-toggle'))
    expect(screen.getByTestId('probe-item-p1')).toBeTruthy()
  })

  it('the + on the heading adds one', () => {
    const onAddProbe = vi.fn()
    render(<HostSidebar {...props({ onAddProbe })} />)
    fireEvent.click(screen.getByTestId('probe-group-add'))
    expect(onAddProbe).toHaveBeenCalledTimes(1)
  })

  it('a paired host still collapses when its selected row is clicked (unchanged)', () => {
    render(<HostSidebar {...props()} />)
    expect(screen.getByText('Overview')).toBeTruthy()
    fireEvent.click(screen.getByText('Test Host'))
    expect(screen.queryByText('Overview')).toBeNull()
  })

  it('without any fleet daemon or probe host the group is not shown', () => {
    useFleetStore.getState().reset()
    render(<HostSidebar {...props()} />)
    expect(screen.queryByTestId('probe-group')).toBeNull()
  })
})
